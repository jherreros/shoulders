package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/loader"
	helmcli "helm.sh/helm/v4/pkg/cli"
	helmkube "helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/storage/driver"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

const (
	ciliumRepoURL   = "https://helm.cilium.io/"
	ciliumChartName = "cilium"
)

func EnsureCilium(kubeconfigPath, version string) error {
	settings := helmcli.New()
	if kubeconfigPath != "" {
		settings.KubeConfig = kubeconfigPath
	}
	settings.SetNamespace("kube-system")

	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(settings.RESTClientGetter(), settings.Namespace(), "secret"); err != nil {
		return err
	}

	chartPath, err := locateChart(settings, version)
	if err != nil {
		return err
	}

	chart, err := loader.Load(chartPath)
	if err != nil {
		return err
	}

	// Determine the API server address as seen from inside the cluster.
	// kubeProxyReplacement needs this to take over service routing.
	apiHost, apiPort, err := getAPIServerEndpoint(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("determine api server endpoint: %w", err)
	}

	values := map[string]interface{}{
		"image": map[string]interface{}{
			"pullPolicy": "IfNotPresent",
		},
		"ipam": map[string]interface{}{
			"mode": "kubernetes",
		},
		"kubeProxyReplacement": true,
		"k8sServiceHost":       apiHost,
		"k8sServicePort":       apiPort,
		"gatewayAPI": map[string]interface{}{
			"enabled": true,
			"hostNetwork": map[string]interface{}{
				"enabled": true,
			},
		},
		"envoy": map[string]interface{}{
			"securityContext": map[string]interface{}{
				"capabilities": map[string]interface{}{
					"envoy":                 []interface{}{"NET_ADMIN", "SYS_ADMIN", "NET_BIND_SERVICE"},
					"keepCapNetBindService": true,
				},
			},
		},
		"extraConfig": map[string]interface{}{
			"api-rate-limit": "endpoint-create=rate-limit:100/s,rate-burst:50",
		},
		"prometheus": map[string]interface{}{
			"enabled": true,
			"serviceMonitor": map[string]interface{}{
				"enabled": false,
			},
		},
		"dashboards": map[string]interface{}{
			"enabled": true,
			"annotations": map[string]interface{}{
				"grafana_folder": "Cilium",
			},
		},
		"hubble": map[string]interface{}{
			"relay": map[string]interface{}{
				"enabled": true,
			},
			"ui": map[string]interface{}{
				"enabled": true,
			},
			"metrics": map[string]interface{}{
				"enableOpenMetrics": true,
				"enabled": []interface{}{
					"dns",
					"drop",
					"tcp",
					"flow",
					"port-distribution",
					"icmp",
					"httpV2:exemplars=true;labelsContext=source_ip,source_namespace,source_workload,destination_ip,destination_namespace,destination_workload,traffic_direction",
				},
				"dashboards": map[string]interface{}{
					"enabled": true,
					"annotations": map[string]interface{}{
						"grafana_folder": "Cilium",
					},
				},
			},
		},
	}

	if releaseExists(actionConfig, ciliumChartName) {
		upgrade := action.NewUpgrade(actionConfig)
		upgrade.Namespace = settings.Namespace()
		upgrade.Version = version
		upgrade.WaitStrategy = helmkube.LegacyStrategy
		if _, err = upgrade.Run(ciliumChartName, chart, values); err != nil {
			return err
		}
		return RestartCiliumWorkloads(kubeconfigPath)
	}

	install := action.NewInstall(actionConfig)
	install.ReleaseName = ciliumChartName
	install.Namespace = settings.Namespace()
	install.CreateNamespace = true
	install.Version = version
	install.WaitStrategy = helmkube.LegacyStrategy
	if _, err = install.Run(chart, values); err != nil {
		return err
	}

	// Wait for the Cilium DaemonSet to become ready so the CNI config is
	// written before any other pods are restarted.
	clientset, err := kube.NewClientset(kubeconfigPath)
	if err != nil {
		return err
	}
	if err := waitForCiliumDaemonSet(context.Background(), clientset); err != nil {
		return fmt.Errorf("waiting for cilium daemonset: %w", err)
	}
	return nil
}

func UninstallCilium(kubeconfigPath string) error {
	settings := helmcli.New()
	if kubeconfigPath != "" {
		settings.KubeConfig = kubeconfigPath
	}
	settings.SetNamespace("kube-system")

	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(settings.RESTClientGetter(), settings.Namespace(), "secret"); err != nil {
		return err
	}

	uninstall := action.NewUninstall(actionConfig)
	uninstall.WaitStrategy = helmkube.LegacyStrategy
	_, err := uninstall.Run(ciliumChartName)
	if err != nil && err != driver.ErrReleaseNotFound {
		return err
	}
	return nil
}

func RestartCiliumWorkloads(kubeconfigPath string) error {
	clientset, err := kube.NewClientset(kubeconfigPath)
	if err != nil {
		return err
	}

	ctx := context.Background()
	restartedAt := time.Now().UTC().Format(time.RFC3339Nano)

	daemonSet, err := clientset.AppsV1().DaemonSets("kube-system").Get(ctx, "cilium", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get cilium daemonset: %w", err)
	}
	if daemonSet.Spec.Template.Annotations == nil {
		daemonSet.Spec.Template.Annotations = map[string]string{}
	}
	daemonSet.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = restartedAt
	if _, err := clientset.AppsV1().DaemonSets("kube-system").Update(ctx, daemonSet, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("restart cilium daemonset: %w", err)
	}

	deployment, err := clientset.AppsV1().Deployments("kube-system").Get(ctx, "cilium-operator", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get cilium operator deployment: %w", err)
	}
	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = map[string]string{}
	}
	deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = restartedAt
	if _, err := clientset.AppsV1().Deployments("kube-system").Update(ctx, deployment, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("restart cilium operator deployment: %w", err)
	}

	if err := waitForCiliumDaemonSet(ctx, clientset); err != nil {
		return err
	}
	return waitForCiliumOperator(ctx, clientset)
}

func waitForCiliumDaemonSet(ctx context.Context, clientset *kubernetes.Clientset) error {
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		daemonSet, err := clientset.AppsV1().DaemonSets("kube-system").Get(ctx, "cilium", metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}

		desired := daemonSet.Status.DesiredNumberScheduled
		return daemonSet.Status.ObservedGeneration >= daemonSet.Generation &&
				daemonSet.Status.UpdatedNumberScheduled == desired &&
				daemonSet.Status.NumberAvailable == desired,
			nil
	})
}

func waitForCiliumOperator(ctx context.Context, clientset *kubernetes.Clientset) error {
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		deployment, err := clientset.AppsV1().Deployments("kube-system").Get(ctx, "cilium-operator", metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}

		replicas := int32(1)
		if deployment.Spec.Replicas != nil {
			replicas = *deployment.Spec.Replicas
		}

		return deployment.Status.ObservedGeneration >= deployment.Generation &&
				deployment.Status.UpdatedReplicas == replicas &&
				deployment.Status.AvailableReplicas == replicas,
			nil
	})
}

func locateChart(settings *helmcli.EnvSettings, version string) (string, error) {
	chartPathOptions := &action.ChartPathOptions{
		RepoURL: ciliumRepoURL,
		Version: version,
	}
	return chartPathOptions.LocateChart(ciliumChartName, settings)
}

func releaseExists(cfg *action.Configuration, name string) bool {
	get := action.NewGet(cfg)
	_, err := get.Run(name)
	return err == nil
}

// getAPIServerEndpoint returns the API server host and port as seen from
// inside the cluster by reading the kubernetes endpoints.
func getAPIServerEndpoint(kubeconfigPath string) (string, int32, error) {
	clientset, err := kube.NewClientset(kubeconfigPath)
	if err != nil {
		return "", 0, err
	}

	ep, err := clientset.CoreV1().Endpoints("default").Get(context.Background(), "kubernetes", metav1.GetOptions{})
	if err != nil {
		return "", 0, fmt.Errorf("get kubernetes endpoints: %w", err)
	}

	for _, subset := range ep.Subsets {
		if len(subset.Addresses) > 0 && len(subset.Ports) > 0 {
			return subset.Addresses[0].IP, subset.Ports[0].Port, nil
		}
	}
	return "", 0, fmt.Errorf("no kubernetes endpoint addresses found")
}

// PatchCiliumHelmRelease removes kind-specific k8sServiceHost/Port and
// kubeProxyReplacement from the Flux-managed Cilium HelmRelease. This is
// needed because the Git repo may still contain values targeting a kind
// cluster, while vind uses a different API server topology.
func PatchCiliumHelmRelease(kubeconfigPath string) error {
	client, err := kube.NewDynamicClient(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}

	gvr := kube.HelmReleaseGVR()
	ctx := context.Background()

	hr, err := client.Resource(gvr).Namespace("kube-system").Get(ctx, "cilium", metav1.GetOptions{})
	if err != nil {
		// HelmRelease may not exist yet (e.g. if Flux hasn't reconciled)
		return nil
	}

	vals, found, err := unstructuredNestedMap(hr.Object, "spec", "values")
	if err != nil || !found {
		return nil
	}

	changed := false
	for _, key := range []string{"k8sServiceHost", "k8sServicePort", "kubeProxyReplacement"} {
		if _, ok := vals[key]; ok {
			delete(vals, key)
			changed = true
		}
	}
	if !changed {
		return nil
	}

	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"values": vals,
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}

	_, err = client.Resource(gvr).Namespace("kube-system").Patch(
		ctx, "cilium", types.MergePatchType, patchBytes, metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("patch cilium helmrelease: %w", err)
	}

	return nil
}

// SuspendCiliumHelmRelease patches the Flux helm-releases Kustomization to
// add an inline patch that suspends the Cilium HelmRelease. This prevents
// the Flux helm-controller from overriding the CLI-managed Cilium
// installation with potentially incompatible values from the Git repo.
func SuspendCiliumHelmRelease(kubeconfigPath string) error {
	client, err := kube.NewDynamicClient(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "kustomize.toolkit.fluxcd.io",
		Version:  "v1",
		Resource: "kustomizations",
	}
	ctx := context.Background()

	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"patches": []interface{}{
				map[string]interface{}{
					"target": map[string]interface{}{
						"kind": "HelmRelease",
						"name": "cilium",
					},
					"patch": `apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: cilium
spec:
  suspend: true`,
				},
			},
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}

	_, err = client.Resource(gvr).Namespace("flux-system").Patch(
		ctx, "helm-releases", types.MergePatchType, patchBytes, metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("patch helm-releases kustomization: %w", err)
	}

	return nil
}

// RestartStuckPods deletes all pods stuck in Pending phase across the
// cluster (excluding flux-system). This is needed after Flux reconciliation
// because the initial pod flood can overwhelm Cilium's endpoint API (429
// rate limiting), leaving pods in exponential backoff. Deleting them lets
// the owning controllers recreate them immediately.
func RestartStuckPods(kubeconfigPath string) error {
	clientset, err := kube.NewClientset(kubeconfigPath)
	if err != nil {
		return err
	}

	ctx := context.Background()
	pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "status.phase=Pending",
	})
	if err != nil {
		return fmt.Errorf("list pods: %w", err)
	}

	for _, pod := range pods.Items {
		if pod.Namespace == "flux-system" {
			continue
		}
		_ = clientset.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
	}

	return nil
}

// unstructuredNestedMap safely retrieves a nested map from an unstructured object.
func unstructuredNestedMap(obj map[string]interface{}, fields ...string) (map[string]interface{}, bool, error) {
	current := obj
	for _, field := range fields {
		val, ok := current[field]
		if !ok {
			return nil, false, nil
		}
		m, ok := val.(map[string]interface{})
		if !ok {
			return nil, false, fmt.Errorf("field %q is not a map", field)
		}
		current = m
	}
	return current, true, nil
}
