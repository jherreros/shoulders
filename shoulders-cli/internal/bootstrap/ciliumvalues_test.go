package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/yaml"
)

// The hand-off contract spans two repositories-in-one: the Go code that writes
// the ConfigMap and the YAML that references it. These tests pin both halves so
// renaming one side without the other cannot silently disable the hand-off — in
// which case Flux would adopt Cilium and strip the mesh config, with no error.

const gitOpsCiliumManifestPath = "../../../2-addons/manifests/helm-releases/cilium.yaml"

func TestCiliumRuntimeValuesCarryOnlyRuntimeKeys(t *testing.T) {
	// A values map shaped like the one EnsureCilium builds.
	values := map[string]interface{}{
		"image":                map[string]interface{}{"pullPolicy": "IfNotPresent"},
		"ipam":                 map[string]interface{}{"mode": "kubernetes"},
		"kubeProxyReplacement": true,
		"k8sServiceHost":       "10.96.0.1",
		"k8sServicePort":       8443,
		"extraConfig":          map[string]interface{}{"api-rate-limit": "endpoint-create=rate-limit:100/s"},
		"gatewayAPI":           map[string]interface{}{"enabled": true},
		"hubble":               map[string]interface{}{"relay": map[string]interface{}{"enabled": true}},
	}

	mesh := (&ClusterMeshOptions{
		Enabled: true, ClusterName: "hub", ClusterID: 7,
		Peers: map[string]string{"spoke-a": "spoke-a.mesh.cilium.io"},
	}).meshValues()
	for k, v := range mesh {
		values[k] = v
	}

	runtime := CiliumRuntimeValues(values)

	// Everything the mesh and the API endpoint need must be present, or Flux
	// would adopt the release and erase it.
	for _, key := range []string{
		"kubeProxyReplacement", "k8sServiceHost", "k8sServicePort",
		"extraConfig", "cluster", "clustermesh", "routingMode", "protocol",
	} {
		if _, ok := runtime[key]; !ok {
			t.Errorf("runtime values are missing %q; Flux would drop it on adoption", key)
		}
	}

	// Anything that belongs in git must NOT be duplicated here: duplicated
	// values are a second source of truth that will drift.
	for _, key := range []string{"image", "ipam", "gatewayAPI", "hubble", "prometheus", "envoy"} {
		if _, ok := runtime[key]; ok {
			t.Errorf("%q is owned by 2-addons and must not be duplicated in the ConfigMap", key)
		}
	}
}

// Encryption is computed per topology, exactly like the peer list, so it must
// travel in the ConfigMap or Flux adoption would strip it and silently downgrade
// the mesh to plaintext.
func TestCiliumRuntimeValuesCarryWireGuard(t *testing.T) {
	options := &ClusterMeshOptions{
		Enabled: true, ClusterName: "hub", ClusterID: 7,
		Encryption: "wireguard",
	}
	values := options.meshValues()
	runtime := CiliumRuntimeValues(values)
	if _, ok := runtime["encryption"]; !ok {
		t.Fatal("runtime values are missing encryption; Flux would drop it on adoption")
	}
}

// Flux stops the CLI from running Helm after the first install, and Helm does
// not roll the agent when only the ConfigMap changes. This predicate is the
// signal to restart: same values must be a no-op, a changed document (for
// example encryption being enabled) must restart.
func TestCiliumRuntimeValuesChanged(t *testing.T) {
	ctx := context.Background()
	clientset := fake.NewSimpleClientset()
	values := CiliumRuntimeValues(meshOptionsForTest(nil).meshValues())

	changed, err := CiliumRuntimeValuesChanged(ctx, clientset, values)
	if err != nil {
		t.Fatalf("read missing config: %v", err)
	}
	if !changed {
		t.Fatal("missing ConfigMap must count as a change")
	}
	if err := ApplyCiliumRuntimeValues(ctx, clientset, values); err != nil {
		t.Fatalf("apply: %v", err)
	}
	changed, err = CiliumRuntimeValuesChanged(ctx, clientset, values)
	if err != nil {
		t.Fatalf("read existing config: %v", err)
	}
	if changed {
		t.Fatal("re-applying identical values must not restart Cilium")
	}

	options := meshOptionsForTest(nil)
	options.Encryption = "wireguard"
	changed, err = CiliumRuntimeValuesChanged(ctx, clientset, CiliumRuntimeValues(options.meshValues()))
	if err != nil {
		t.Fatalf("read changed config: %v", err)
	}
	if !changed {
		t.Fatal("enabling encryption must restart Cilium")
	}
}

func TestCiliumRuntimeValuesOmitMeshWhenNotMeshed(t *testing.T) {
	values := map[string]interface{}{
		"kubeProxyReplacement": true,
		"k8sServiceHost":       "10.96.0.1",
		"k8sServicePort":       8443,
	}
	runtime := CiliumRuntimeValues(values)
	if _, ok := runtime["clustermesh"]; ok {
		t.Error("a cluster that is not meshed must not publish a clustermesh block")
	}
	// With mesh absent there is nothing mesh-related to write at all.
	if _, ok := runtime["cluster"]; ok {
		t.Error("cluster identity is only meaningful for a mesh participant")
	}
}

func TestApplyCiliumRuntimeValuesWritesAndIsIdempotent(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	ctx := context.Background()
	values := map[string]interface{}{"k8sServiceHost": "10.96.0.1", "k8sServicePort": 8443}

	if err := ApplyCiliumRuntimeValues(ctx, clientset, values); err != nil {
		t.Fatalf("create: %v", err)
	}
	read := func() string {
		t.Helper()
		cm, err := clientset.CoreV1().ConfigMaps("kube-system").Get(ctx, CiliumRuntimeValuesName, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		return cm.Data[runtimeValuesKey]
	}
	if !strings.Contains(read(), "k8sServiceHost") {
		t.Fatalf("value not written: %q", read())
	}

	// Re-applying must update in place, not fail on an existing object, and must
	// not leave a stale peer behind.
	values["cluster"] = map[string]interface{}{"name": "hub", "id": 7}
	if err := ApplyCiliumRuntimeValues(ctx, clientset, values); err != nil {
		t.Fatalf("update: %v", err)
	}
	body := read()
	if !strings.Contains(body, "hub") {
		t.Fatalf("updated value missing: %q", body)
	}

	// A follow-up run on a cluster that lost its mesh must not leave the old
	// peer in place: the document is replaced wholesale.
	if err := ApplyCiliumRuntimeValues(ctx, clientset, map[string]interface{}{"k8sServiceHost": "10.96.0.1"}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if strings.Contains(read(), "cluster") {
		t.Fatalf("stale mesh identity survived a replacement: %q", read())
	}
}

func TestApplyCiliumRuntimeValuesSkipsWhenEmpty(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	if err := ApplyCiliumRuntimeValues(context.Background(), clientset, nil); err != nil {
		t.Fatalf("empty values must be a no-op, got %v", err)
	}
	cm, err := clientset.CoreV1().ConfigMaps("kube-system").Get(context.Background(), CiliumRuntimeValuesName, metav1.GetOptions{})
	if err == nil {
		t.Fatalf("an empty document should not have been created: %+v", cm)
	}
}

// The rendered document must be valid Helm values, because helm-controller
// parses it with the same YAML loader.
func TestCiliumRuntimeValuesRenderValidYAML(t *testing.T) {
	mesh := (&ClusterMeshOptions{
		Enabled: true, ClusterName: "hub", ClusterID: 211,
		Peers: map[string]string{"spoke-a": "spoke-a.mesh.cilium.io"},
	}).meshValues()
	values := map[string]interface{}{"k8sServiceHost": "10.96.0.1", "k8sServicePort": 8443}
	for k, v := range mesh {
		values[k] = v
	}

	document, err := yaml.Marshal(CiliumRuntimeValues(values))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var parsed map[string]interface{}
	if err := yaml.Unmarshal(document, &parsed); err != nil {
		t.Fatalf("rendered document is not valid YAML: %v\n%s", err, document)
	}
	if _, ok := parsed["clustermesh"]; !ok {
		t.Fatalf("clustermesh missing from rendered document:\n%s", document)
	}
}

// TestGitOpsCiliumManifestReferencesTheConfigMap checks the other half of the
// contract: the manifest must name the ConfigMap the CLI writes, must mark it
// optional so a non-CLI install still works, and must not set any of the keys
// the CLI owns.
func TestGitOpsCiliumManifestReferencesTheConfigMap(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(gitOpsCiliumManifestPath))
	if err != nil {
		t.Skipf("manifest not present at %s; skipping", gitOpsCiliumManifestPath)
	}

	var release struct {
		Spec struct {
			ValuesFrom []struct {
				Kind     string `yaml:"kind"`
				Name     string `yaml:"name"`
				Optional *bool  `yaml:"optional"`
			} `yaml:"valuesFrom"`
			Values map[string]interface{} `yaml:"values"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(raw, &release); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	found := false
	for _, ref := range release.Spec.ValuesFrom {
		if ref.Kind != "ConfigMap" || ref.Name != CiliumRuntimeValuesName {
			continue
		}
		found = true
		if ref.Optional == nil || !*ref.Optional {
			t.Error("the valuesFrom entry must be optional: true, or a non-CLI install fails on a missing ConfigMap")
		}
	}
	if !found {
		t.Fatalf("the manifest does not reference ConfigMap %q; Flux would adopt Cilium and strip the mesh config",
			CiliumRuntimeValuesName)
	}

	// Disjointness. flux merges inline values *over* valuesFrom, so any overlap
	// would let git silently override what the CLI discovered at runtime.
	for _, key := range ciliumRuntimeKeys {
		if _, ok := release.Spec.Values[key]; ok {
			t.Errorf("the manifest sets %q inline, but the CLI owns that key; inline values win over valuesFrom", key)
		}
	}
}

// A cheap structural guard: the ConfigMap must live in the HelmRelease's
// namespace, because flux only resolves values references in the same namespace.
func TestCiliumRuntimeValuesAreAppliedToKubeSystem(t *testing.T) {
	if got := (&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: CiliumRuntimeValuesName, Namespace: "kube-system"},
	}).Namespace; got != "kube-system" {
		t.Fatalf("namespace = %q; flux resolves values references in the HelmRelease's own namespace", got)
	}
}

func TestDesiredCiliumConfigTracksPolicyScope(t *testing.T) {
	values := map[string]interface{}{
		"clustermesh": map[string]interface{}{"policyDefaultLocalCluster": true},
	}
	if got := desiredCiliumConfig(values)["policy-default-local-cluster"]; got != "true" {
		t.Errorf("desired policy-default-local-cluster = %q, want true", got)
	}
}

// Turning node encryption off makes the chart omit encrypt-node, and Helm does
// not delete keys that disappear from a ConfigMap manifest. The live key must be
// written back explicitly or the agents keep the old mixed-encryption mode.
func TestApplyCiliumEncryptionConfigClearsStaleNodeEncryption(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cilium-config", Namespace: "kube-system"},
		Data: map[string]string{
			"enable-wireguard": "true",
			"encrypt-node":     "true",
		},
	})
	values := map[string]interface{}{
		"encryption": map[string]interface{}{
			"enabled": true, "type": "wireguard", "nodeEncryption": false,
		},
	}
	if err := ApplyCiliumEncryptionConfig(context.Background(), clientset, values); err != nil {
		t.Fatalf("ApplyCiliumEncryptionConfig: %v", err)
	}
	cm, err := clientset.CoreV1().ConfigMaps("kube-system").Get(context.Background(), "cilium-config", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cm.Data["enable-wireguard"] != "true" {
		t.Errorf("enable-wireguard = %q, want true", cm.Data["enable-wireguard"])
	}
	if cm.Data["encrypt-node"] != "false" {
		t.Errorf("encrypt-node = %q, want false", cm.Data["encrypt-node"])
	}
}

func TestApplyCiliumEncryptionConfigDisablesWireGuard(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cilium-config", Namespace: "kube-system"},
		Data: map[string]string{
			"enable-wireguard": "true",
			"encrypt-node":     "true",
		},
	})
	if err := ApplyCiliumEncryptionConfig(context.Background(), clientset, map[string]interface{}{}); err != nil {
		t.Fatalf("ApplyCiliumEncryptionConfig: %v", err)
	}
	cm, err := clientset.CoreV1().ConfigMaps("kube-system").Get(context.Background(), "cilium-config", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cm.Data["enable-wireguard"] != "false" || cm.Data["encrypt-node"] != "false" {
		t.Errorf("encryption keys = %q/%q, want false/false", cm.Data["enable-wireguard"], cm.Data["encrypt-node"])
	}
}
