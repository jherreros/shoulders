package bootstrap

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// Cilium is installed by the CLI because Flux cannot reconcile anything until a
// CNI is running. It is then *adopted* by Flux from the HelmRelease in
// 2-addons/manifests/helm-releases/cilium.yaml — a bootstrap-then-hand-off
// pattern, and the endorsed one (see fluxcd/flux2 discussion #3070). The
// HelmRelease is named `cilium` in `kube-system`, exactly like the CLI's Helm
// release, so helm-controller takes it over rather than creating a second one.
//
// That hand-off creates a problem the static manifest cannot solve on its own:
// some Cilium values are discovered at install time and differ per cluster.
//
//	k8sServiceHost / k8sServicePort  read from the cluster's own API endpoint
//	cluster.name / cluster.id         this cluster's identity in the mesh
//	clustermesh.*                     the peer list, which is per topology
//
// So the CLI writes them to a ConfigMap, and the HelmRelease references it with
// valuesFrom.
//
// The split is by key ownership rather than by precedence. flux merges valuesFrom
// first and then lets **inline** values overwrite them, so an overlapping key in
// 2-addons/manifests/helm-releases/cilium.yaml would silently win over what the
// CLI discovered. Keeping the two key sets disjoint removes the question instead
// of answering it.
const (
	// CiliumRuntimeValuesName is the ConfigMap the CLI writes and the GitOps
	// HelmRelease reads. The name is part of the contract between the CLI and
	// 2-addons: changing one without the other silently disables the hand-off.
	CiliumRuntimeValuesName = "shoulders-cilium-cluster"

	runtimeValuesKey = "values.yaml"
)

// ciliumRuntimeKeys are the top-level value keys that are computed at install
// time and therefore cannot live in the GitOps manifest.
//
// Everything else — ipam, gatewayAPI, prometheus, dashboards, hubble, envoy,
// image — stays in 2-addons/manifests/helm-releases/cilium.yaml. Keeping the
// split this way means the two sides do not overlap, so there is no precedence
// question to get wrong and no duplicated values to drift apart.
var ciliumRuntimeKeys = []string{
	"kubeProxyReplacement",
	"k8sServiceHost",
	"k8sServicePort",
	"extraConfig",
	"cluster",
	"clustermesh",
	"routingMode",
	"protocol",
	"encryption",
}

// CiliumRuntimeValues extracts the install-time-discovered subset of a Cilium
// values map.
//
// It filters rather than rebuilding, so the ConfigMap and the Helm install are
// guaranteed to be the same values by construction rather than by two functions
// staying in agreement.
func CiliumRuntimeValues(values map[string]interface{}) map[string]interface{} {
	runtime := make(map[string]interface{}, len(ciliumRuntimeKeys))
	for _, key := range ciliumRuntimeKeys {
		if value, ok := values[key]; ok {
			runtime[key] = value
		}
	}
	return runtime
}

// CiliumRuntimeValuesChanged reports whether applying values would rewrite the
// runtime ConfigMap.
//
// After the first install Flux owns the Helm release, so the CLI stops running
// Helm actions. The agent still reads encryption and policy settings from the
// generated ConfigMap at startup, and Flux's upgrade does not necessarily roll
// the DaemonSet (the pod template is unchanged). A changed document therefore
// needs an explicit rolling restart; this predicate is what tells the caller it
// is needed.
func CiliumRuntimeValuesChanged(ctx context.Context, clientset kubernetes.Interface, values map[string]interface{}) (bool, error) {
	if len(values) == 0 {
		return false, nil
	}
	document, err := yaml.Marshal(values)
	if err != nil {
		return false, fmt.Errorf("render cilium runtime values: %w", err)
	}
	current, err := clientset.CoreV1().ConfigMaps("kube-system").Get(ctx, CiliumRuntimeValuesName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", CiliumRuntimeValuesName, err)
	}
	return current.Data[runtimeValuesKey] != string(document), nil
}

// ApplyCiliumRuntimeValues writes the runtime values ConfigMap.
//
// It is idempotent and never removes keys it does not manage beyond the whole
// document, so a monocluster install that later gains mesh settings replaces the
// document rather than accumulating stale peers.
func ApplyCiliumRuntimeValues(ctx context.Context, clientset kubernetes.Interface, values map[string]interface{}) error {
	if len(values) == 0 {
		return nil
	}
	document, err := yaml.Marshal(values)
	if err != nil {
		return fmt.Errorf("render cilium runtime values: %w", err)
	}

	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CiliumRuntimeValuesName,
			Namespace: "kube-system",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "shoulders",
			},
		},
		Data: map[string]string{runtimeValuesKey: string(document)},
	}

	secrets := clientset.CoreV1().ConfigMaps("kube-system")
	_, err = secrets.Update(ctx, desired, metav1.UpdateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("update %s: %w", CiliumRuntimeValuesName, err)
	}
	if _, err := secrets.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create %s: %w", CiliumRuntimeValuesName, err)
	}
	return nil
}

// desiredCiliumConfig returns the cilium-config keys that must match before the
// agents are restarted. Encryption keys are handled separately: the chart omits
// them when they are off, so Helm's merge leaves the old value behind.
func desiredCiliumConfig(values map[string]interface{}) map[string]string {
	desired := map[string]string{}
	if mesh, ok := values["clustermesh"].(map[string]interface{}); ok {
		if local, ok := mesh["policyDefaultLocalCluster"].(bool); ok {
			desired["policy-default-local-cluster"] = fmt.Sprintf("%t", local)
		}
	}
	return desired
}

// WaitForCiliumConfig waits for the cilium-config keys to reflect the desired
// values. It is called after the Helm release has a new revision; a short poll
// covers API server propagation.
func WaitForCiliumConfig(ctx context.Context, clientset kubernetes.Interface, desired map[string]string, timeout time.Duration) error {
	if len(desired) == 0 {
		return nil
	}
	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		cm, err := clientset.CoreV1().ConfigMaps("kube-system").Get(ctx, "cilium-config", metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		for key, want := range desired {
			if cm.Data[key] != want {
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("waiting for cilium-config to reflect the new values: %w", err)
	}
	return nil
}

// ApplyCiliumEncryptionConfig writes the encryption switches to cilium-config.
//
// This exists because of a Helm merge detail: when encryption is off the chart
// omits enable-wireguard/encrypt-node, and a three-way merge over a ConfigMap's
// data map does not remove keys that disappeared from the manifest. An upgrade
// from wireguard to off (or from node encryption on to off) would otherwise keep
// the old switch forever. Writing the desired values explicitly makes the live
// object authoritative, and the agent still needs the rolling restart that
// follows.
func ApplyCiliumEncryptionConfig(ctx context.Context, clientset kubernetes.Interface, values map[string]interface{}) error {
	desired := map[string]string{
		"enable-wireguard": "false",
		"encrypt-node":     "false",
	}
	if encryption, ok := values["encryption"].(map[string]interface{}); ok {
		enabled, _ := encryption["enabled"].(bool)
		if enabled && encryption["type"] == "wireguard" {
			desired["enable-wireguard"] = "true"
			if node, ok := encryption["nodeEncryption"].(bool); ok {
				desired["encrypt-node"] = fmt.Sprintf("%t", node)
			}
		}
	}
	cm, err := clientset.CoreV1().ConfigMaps("kube-system").Get(ctx, "cilium-config", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("read cilium-config: %w", err)
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	changed := false
	for key, want := range desired {
		if cm.Data[key] != want {
			cm.Data[key] = want
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if _, err := clientset.CoreV1().ConfigMaps("kube-system").Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update cilium-config encryption settings: %w", err)
	}
	return nil
}
