package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/flux"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/loader"
	helmcli "helm.sh/helm/v4/pkg/cli"
	helmkube "helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/release"
	releasecommon "helm.sh/helm/v4/pkg/release/common"
	"helm.sh/helm/v4/pkg/storage/driver"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

const (
	ciliumRepoURL   = "https://helm.cilium.io/"
	ciliumChartName = "cilium"
)

type CiliumOptions struct {
	// ContextName pins the Helm release to a specific kubeconfig context.
	//
	// This is REQUIRED in a multi-cluster install. The rest of the CLI resolves
	// its target cluster through the kube context override in this package,
	// but Helm ignores that override and uses the ambient current-context. Left
	// unset, a topology install writes into whichever cluster the kubeconfig
	// happens to point at while reading the API endpoint from another, which
	// surfaces as an operator that cannot verify the API server certificate.
	ContextName string

	// Mesh carries the ClusterMesh settings for this cluster. Nil (or
	// Enabled=false) installs Cilium as a standalone cluster.
	Mesh *ClusterMeshOptions
}

// ClusterMeshOptions describes one cluster's place in a Cilium ClusterMesh.
//
// Every value here was established by measurement; see
// docs/multi-cluster-evidence.md. The defaults below are what a vCluster-in-
// Docker topology requires and are NOT the chart defaults:
//
//   - ServiceType NodePort: vCluster can only expose LoadBalancer with sudo,
//     and clustermesh-apiserver would never become ready otherwise.
//   - TLSAuthMode legacy: the chart default (migration) creates per-cluster etcd
//     users while still requiring CN=remote, which the Helm-generated certs do
//     not satisfy; peers reject each other with "tls: bad certificate".
//   - TLSCertMethod helm: certgen needs a CronJob and cert-manager a controller,
//     neither of which is practical inside a vCluster.
//   - Certificates are reached by DNS name, so the peer address must be a name
//     covered by ExtraDNSNames, not a bare IP.
type ClusterMeshOptions struct {
	Enabled bool

	// ClusterName and ClusterID identify this cluster to its peers. ID must be
	// unique across the mesh and 1-255.
	ClusterName string
	ClusterID   int

	// Peers maps each remote cluster name to its ClusterMesh API endpoint.
	// Endpoints are "<name>.<Domain>" or a resolvable address.
	Peers map[string]string

	// Domain is the DNS suffix for mesh API servers. Defaults to
	// "mesh.cilium.io".
	Domain string

	// APIServerPort is the port remote clusters dial. Defaults to 32379, the
	// NodePort vCluster assigns for clustermesh-apiserver.
	APIServerPort int

	// ServiceType defaults to NodePort.
	ServiceType string

	// TLSAuthMode defaults to "legacy"; see the type comment.
	TLSAuthMode string

	// TLSCertMethod defaults to "helm".
	TLSCertMethod string

	// ExtraDNSNames are added to the generated server certificate's SANs.
	ExtraDNSNames []string

	// Encryption enables transparent inter-node encryption. "wireguard" is the
	// only supported value; empty disables it.
	Encryption string
}

func (m *ClusterMeshOptions) port() int {
	if m.APIServerPort != 0 {
		return m.APIServerPort
	}
	return 32379
}

func (m *ClusterMeshOptions) domain() string {
	if m.Domain != "" {
		return m.Domain
	}
	return "mesh.cilium.io"
}

func (m *ClusterMeshOptions) serviceType() string {
	if m.ServiceType != "" {
		return m.ServiceType
	}
	return "NodePort"
}

func (m *ClusterMeshOptions) tlsAuthMode() string {
	if m.TLSAuthMode != "" {
		return m.TLSAuthMode
	}
	return "legacy"
}

func (m *ClusterMeshOptions) tlsCertMethod() string {
	if m.TLSCertMethod != "" {
		return m.TLSCertMethod
	}
	return "helm"
}

// meshValues renders the chart values for ClusterMesh.
//
// `policyDefaultLocalCluster: true` is Cilium 1.19's default and the safe
// choice: a policy that does not name a cluster selects endpoints in its own
// cluster only. Every stateful dependency in this topology (CNPG, Garage,
// Prometheus, Dex) is reached locally or without policy, so nothing needs the
// old mesh-wide scope. A future cross-cluster rule (for example workspace
// telemetry to a hub Loki) must name the hub explicitly with
// `io.cilium.k8s.policy.cluster: <hub>`. See docs/multi-cluster-evidence.md
// §19 — the earlier assumption that `false` was required came from a test whose
// namespace selector already allowed the traffic.
func (m *ClusterMeshOptions) meshValues() map[string]interface{} {
	clusters := map[string]interface{}{}
	for name, address := range m.Peers {
		if name == m.ClusterName {
			// Cilium ignores the local cluster; omitting it avoids a
			// self-connection.
			continue
		}
		clusters[name] = map[string]interface{}{
			"address": address,
			"port":    m.port(),
		}
	}

	extraDNSNames := []interface{}{m.ClusterName + "." + m.domain()}
	for _, name := range m.ExtraDNSNames {
		extraDNSNames = append(extraDNSNames, name)
	}

	values := map[string]interface{}{
		"cluster": map[string]interface{}{
			"name": m.ClusterName,
			"id":   m.ClusterID,
		},
		"routingMode": "tunnel",
		"protocol":    "vxlan",
		"clustermesh": map[string]interface{}{
			"useAPIServer":              true,
			"policyDefaultLocalCluster": true,
			"apiserver": map[string]interface{}{
				"service": map[string]interface{}{
					"type": m.serviceType(),
				},
				"tls": map[string]interface{}{
					"authMode": m.tlsAuthMode(),
					"auto": map[string]interface{}{
						"enabled":              true,
						"method":               m.tlsCertMethod(),
						"certValidityDuration": 365,
						"server": map[string]interface{}{
							"extraDnsNames": extraDNSNames,
						},
					},
				},
			},
			"config": map[string]interface{}{
				"enabled":  true,
				"clusters": clusters,
			},
		},
	}
	if m.Encryption == "wireguard" {
		// Pod-to-pod traffic is encrypted; node encryption is deliberately off.
		//
		// Cilium opts control-plane nodes out of node encryption by default.
		// In a mixed topology that is not a local detail: an opted-out control
		// plane and an encryption-enabled worker drop each other's host-bound
		// TCP. Measured on a cold install, the hub's ClusterMesh KVStoreMesh
		// could not reach the spoke's NodePort (so the hub side of the mesh
		// stayed 0/1) and the spoke's telemetry could not reach the hub's
		// Gateway. Both recovered when node encryption was disabled. Pod
		// traffic is still WireGuard, and the ClusterMesh control plane is
		// already mTLS, so turning host encryption off costs little and removes
		// a scheduling-dependent failure.
		values["encryption"] = map[string]interface{}{
			"enabled":        true,
			"type":           "wireguard",
			"nodeEncryption": false,
		}
	}
	return values
}

// CiliumOptionsForProfile returns the options for a profile.
//
// The profile no longer decides Cilium's observability here. Flux merges inline
// values over valuesFrom, so anything set in
// 2-addons/manifests/helm-releases/cilium.yaml would win over whatever the CLI
// computed — a `small` cluster would get the hubble UI and Cilium dashboards
// its profile deliberately omits. The profile now expresses that in git, in
// 2-addons/profiles/<profile>/helm-releases/cilium.yaml, alongside the other
// per-profile patches. See docs/multi-cluster-target.md R5.
func CiliumOptionsForProfile(profile string) CiliumOptions {
	return CiliumOptions{}
}

func EnsureCilium(kubeconfigPath, version string, options CiliumOptions) error {
	return EnsureCiliumWithChart(kubeconfigPath, version, options, "")
}

// EnsureCiliumWithChart installs Cilium like EnsureCilium but loads the chart
// from a local .tgz file when chartFile is set (airgap installs), instead of
// resolving it from the Helm repository cache.
func EnsureCiliumWithChart(kubeconfigPath, version string, options CiliumOptions, chartFile string) error {
	settings := helmcli.New()
	if kubeconfigPath != "" {
		settings.KubeConfig = kubeconfigPath
	}
	if options.ContextName != "" {
		settings.KubeContext = options.ContextName
	}
	settings.SetNamespace("kube-system")

	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(settings.RESTClientGetter(), settings.Namespace(), "secret"); err != nil {
		return err
	}

	clientset, err := kube.NewClientset(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("create clientset: %w", err)
	}

	chartPath := chartFile
	if chartPath == "" {
		located, err := locateChart(settings, version)
		if err != nil {
			return err
		}
		chartPath = located
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

	values := ciliumValues(apiHost, apiPort, options)

	// Publish the runtime-discovered subset so Flux adopts this release with the
	// same values instead of replacing it with the manifest's set. This runs
	// before the install/upgrade, because helm-controller reads valuesFrom at
	// reconcile time and must not briefly see a release it would fight over.
	runtimeValues := CiliumRuntimeValues(values)
	valuesChanged, err := CiliumRuntimeValuesChanged(context.Background(), clientset, runtimeValues)
	if err != nil {
		return err
	}

	// Capture the Helm revision before the ConfigMap write. Reading it after the
	// write races the helm-controller: a fast watch can bump the revision before
	// the read, and then waiting for a newer one would only time out. A fresh
	// install has no release yet; the owned branch below is the only consumer.
	previousRevision := 0
	if valuesChanged {
		if revision, revisionErr := ciliumReleaseRevision(actionConfig); revisionErr == nil {
			previousRevision = revision
		}
	}

	if err := ApplyCiliumRuntimeValues(context.Background(), clientset, runtimeValues); err != nil {
		return err
	}

	// A second `up` over an existing topology must not reinstall Cilium. From
	// the first install onwards flux owns the release, and helm-controller is
	// the writer of its objects; running the Helm action again makes the two
	// fight over field ownership. Measured on a re-run of `up`:
	//
	//   conflict occurred while applying object
	//   kube-system/clustermesh-apiserver-admin-cert /v1, Kind=Secret:
	//   Apply failed with 2 conflicts: conflicts with "helm-controller"
	//
	// The values above are still published, because that is how a changed mesh
	// setting reaches flux. If the document actually changed, flux has to apply
	// it before the agents are restarted: an agent only reads encryption and
	// policy settings at startup, and Helm does not roll the DaemonSet when only
	// the ConfigMap changes.
	//
	// Ordering is load-bearing. Restarting immediately re-reads the old
	// ConfigMap. And a key that disappears from the chart's manifest is not
	// deleted by Helm's three-way merge: turning node encryption off leaves
	// encrypt-node: "true" behind in the live ConfigMap. Both were observed on
	// the cold topology run; the helpers below force the Helm release, wait for
	// its new revision, and then reconcile the keys the chart omits.
	if owned, err := fluxOwnsCiliumRelease(kubeconfigPath); err != nil {
		return err
	} else if owned {
		if valuesChanged {
			if previousRevision == 0 {
				previousRevision, err = ciliumReleaseRevision(actionConfig)
				if err != nil {
					return fmt.Errorf("read the Cilium release revision: %w", err)
				}
			}
			dynamicClient, err := kube.NewDynamicClient(kubeconfigPath)
			if err != nil {
				return fmt.Errorf("create dynamic client: %w", err)
			}
			if err := flux.RequestHelmReleaseReconcile(context.Background(), dynamicClient, settings.Namespace(), ciliumChartName, time.Now()); err != nil {
				return fmt.Errorf("request the Cilium HelmRelease reconcile: %w", err)
			}
			if err := waitForCiliumRevision(context.Background(), actionConfig, previousRevision, 4*time.Minute); err != nil {
				return err
			}
			if err := WaitForCiliumConfig(context.Background(), clientset, desiredCiliumConfig(values), 2*time.Minute); err != nil {
				return err
			}
			if err := ApplyCiliumEncryptionConfig(context.Background(), clientset, values); err != nil {
				return err
			}
			return RestartCiliumWorkloads(kubeconfigPath)
		}
		return nil
	}

	switch inspectRelease(actionConfig, ciliumChartName) {
	case releaseDeployed:
		upgrade := action.NewUpgrade(actionConfig)
		upgrade.Namespace = settings.Namespace()
		upgrade.Version = version
		upgrade.WaitStrategy = helmkube.LegacyStrategy
		if _, err = upgrade.Run(ciliumChartName, chart, values); err != nil {
			return err
		}
		return RestartCiliumWorkloads(kubeconfigPath)
	case releaseAbsent:
		// fall through to a fresh install
	default:
		// The release exists but never finished: pending-install, failed, or
		// pending-upgrade/rollback. Upgrading it cannot work (Helm holds a lock
		// on the pending revision) and leaving it means a permanently broken CNI
		// that every subsequent `up` silently skips. Roll it back so the install
		// below starts clean.
		if err := discardRelease(actionConfig, settings.Namespace()); err != nil {
			return fmt.Errorf("clear the unfinished cilium release before reinstalling: %w", err)
		}
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
	if err := waitForCiliumDaemonSet(context.Background(), clientset); err != nil {
		return fmt.Errorf("waiting for cilium daemonset: %w", err)
	}
	return nil
}

// UninstallCilium removes the Cilium release from one cluster.
//
// contextName pins the target the same way CiliumOptions.ContextName does on
// install; see that field for why an unset context is unsafe in a multi-cluster
// install.
func UninstallCilium(kubeconfigPath, contextName string) error {
	settings := helmcli.New()
	if kubeconfigPath != "" {
		settings.KubeConfig = kubeconfigPath
	}
	if contextName != "" {
		settings.KubeContext = contextName
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

// releaseState is the health of an existing Helm release, as far as EnsureCilium
// needs to distinguish.
type releaseState int

const (
	// releaseAbsent means there is no release with this name.
	releaseAbsent releaseState = iota
	// releaseDeployed means the last operation succeeded.
	releaseDeployed
	// releaseUnfinished means a release exists but never completed: it is in a
	// pending-* state, or failed.
	releaseUnfinished
)

// ciliumReleaseRevision returns the Helm revision of the Cilium release.
//
// It is how the CLI distinguishes "flux has applied the new values" from
// "flux has not reconciled yet" after the runtime ConfigMap changes.
func ciliumReleaseRevision(cfg *action.Configuration) (int, error) {
	rel, err := action.NewGet(cfg).Run(ciliumChartName)
	if err != nil {
		return 0, err
	}
	accessor, err := release.NewAccessor(rel)
	if err != nil {
		return 0, err
	}
	return accessor.Version(), nil
}

// waitForCiliumRevision waits until the Helm release is deployed at a revision
// newer than previous. Flux's HelmRelease upgrade is what bumps it.
func waitForCiliumRevision(ctx context.Context, cfg *action.Configuration, previous int, timeout time.Duration) error {
	err := wait.PollUntilContextTimeout(ctx, 3*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		rel, err := action.NewGet(cfg).Run(ciliumChartName)
		if err != nil {
			if errors.Is(err, driver.ErrReleaseNotFound) {
				return false, nil
			}
			return false, err
		}
		accessor, err := release.NewAccessor(rel)
		if err != nil {
			return false, nil
		}
		return accessor.Version() > previous && accessor.Status() == string(releasecommon.StatusDeployed), nil
	})
	if err != nil {
		return fmt.Errorf("waiting for the Cilium HelmRelease to apply a new revision: %w", err)
	}
	return nil
}

// inspectRelease reports a release's state rather than merely testing for its existence.
//
// The status matters. A release abandoned in pending-install by an aborted
// install still exists, but it is not a working CNI: its DaemonSet may never have
// become ready. Treating "exists" as "installed" makes EnsureCilium a permanent
// no-op on exactly the clusters that need help, and leaves Helm holding a lock
// that blocks any retry with a misleading "another operation is in progress".
func inspectRelease(cfg *action.Configuration, name string) releaseState {
	get := action.NewGet(cfg)
	rel, err := get.Run(name)
	if err != nil {
		return releaseAbsent
	}
	accessor, err := release.NewAccessor(rel)
	if err != nil {
		return releaseUnfinished
	}
	// Only a deployed release means the last operation actually completed.
	if accessor.Status() == string(releasecommon.StatusDeployed) {
		return releaseDeployed
	}
	return releaseUnfinished
}

// discardRelease removes a release that never completed, so the next install is
// not blocked by the pending revision.
func discardRelease(cfg *action.Configuration, namespace string) error {
	uninstall := action.NewUninstall(cfg)
	// Nothing to wait for: the release is not running.
	uninstall.WaitStrategy = helmkube.HookOnlyStrategy
	uninstall.KeepHistory = false
	if _, err := uninstall.Run(ciliumChartName); err != nil && !errors.Is(err, driver.ErrReleaseNotFound) {
		return err
	}
	return nil
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

func nestedSlice(obj map[string]interface{}, fields ...string) ([]interface{}, bool) {
	current := obj
	for index, field := range fields {
		val, ok := current[field]
		if !ok {
			return nil, false
		}
		if index == len(fields)-1 {
			slice, ok := val.([]interface{})
			return slice, ok
		}
		next, ok := val.(map[string]interface{})
		if !ok {
			return nil, false
		}
		current = next
	}
	return nil, false
}

// ciliumValues builds the Helm values for the Cilium release.
//
// It is a function rather than an inline literal so that the values the CLI
// installs, the values it publishes for flux to adopt, and the values the
// tests assert on are all the same map. When these were separate literals they
// drifted, and the drift was invisible until a live cluster was configured
// wrongly.
//
// Ownership is split with the git manifest: the keys here that are discovered
// at install time (the API server endpoint, and the mesh identity and peer
// list) are published for flux in the `shoulders-cilium-cluster` ConfigMap,
// and everything else lives in 2-addons/manifests/helm-releases/cilium.yaml.
// The two sets must stay disjoint, because flux merges the manifest's inline
// values OVER the published ones.
func ciliumValues(apiHost string, apiPort int32, options CiliumOptions) map[string]interface{} {
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
	}

	if options.Mesh != nil && options.Mesh.Enabled {
		for key, value := range options.Mesh.meshValues() {
			values[key] = value
		}
	}
	return values
}

// fluxOwnsCiliumRelease reports whether flux already reconciles Cilium in this
// cluster. The presence of the HelmRelease is the signal: the CLI creates the
// release before flux exists, so it is absent on a fresh cluster and present
// from the first reconcile onwards.
func fluxOwnsCiliumRelease(kubeconfigPath string) (bool, error) {
	client, err := kube.NewDynamicClient(kubeconfigPath)
	if err != nil {
		return false, fmt.Errorf("create dynamic client: %w", err)
	}
	_, err = client.Resource(schema.GroupVersionResource{
		Group:    "helm.toolkit.fluxcd.io",
		Version:  "v2",
		Resource: "helmreleases",
	}).Namespace("kube-system").Get(context.Background(), ciliumChartName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get the cilium helmrelease: %w", err)
	}
	return true, nil
}
