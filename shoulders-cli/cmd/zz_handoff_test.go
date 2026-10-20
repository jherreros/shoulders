package cmd

// THROWAWAY validation of the Cilium hand-off: the CLI bootstraps Cilium, then
// Flux adopts the release from the repo's own HelmRelease without erasing the
// values the CLI discovered.
//
// This is the decisive test for the architecture where GitOps owns Cilium. It
// deliberately applies 2-addons/manifests/helm-releases/cilium.yaml verbatim
// rather than a paraphrase, because the failure mode being guarded is a
// mismatch between the Go code and that file.
//
//	SHOULDERS_CILIUM_HANDOFF=1 go test ./cmd/ -run TestCiliumHandOffToFlux -v -timeout 30m

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
)

const handoffCluster = "handoff"

func TestCiliumHandOffToFlux(t *testing.T) {
	if os.Getenv("SHOULDERS_CILIUM_HANDOFF") != "1" {
		t.Skip("set SHOULDERS_CILIUM_HANDOFF=1 to run the cilium hand-off test")
	}
	ctx := context.Background()

	repoRoot := filepath.Join("..", "..")
	manifest := filepath.Join(repoRoot, "2-addons", "manifests", "helm-releases", "cilium.yaml")
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("cannot find the GitOps manifest at %s: %v", manifest, err)
	}

	cluster := clusterNamed(handoffCluster, config.RoleHub, true,
		config.PortMapping{HTTP: 80, HTTPS: 443})
	cluster.PlatformConfig.Cilium.Version = config.DefaultCiliumVersion
	selectCluster(cluster)
	plan, err := newTopologyPlan([]*config.ResolvedCluster{cluster})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	// --- 1. Bootstrap, exactly as `shoulders up` does ---------------------
	if err := ensureClusterExists(ctx, cluster, plan); err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	mesh := &bootstrap.ClusterMeshOptions{
		Enabled: true, ClusterName: handoffCluster, ClusterID: cluster.ClusterID(),
		Peers: map[string]string{}, Domain: "mesh.cilium.io", APIServerPort: 32379,
		ServiceType: "NodePort", TLSAuthMode: "legacy", TLSCertMethod: "helm",
		ExtraDNSNames: []string{handoffCluster + ".mesh.cilium.io"},
	}
	options := bootstrap.CiliumOptionsForProfile(cluster.Profile)
	options.ContextName = targetClusterContextName()
	options.Mesh = mesh

	t.Log("bootstrapping Cilium via the CLI")
	if err := bootstrap.EnsureCilium(kubeconfig, cluster.CiliumVersion(), options); err != nil {
		t.Fatalf("cli cilium install: %v", err)
	}

	before := helmValues(t)
	if !strings.Contains(before, "kubeProxyReplacement: true") {
		t.Fatalf("the CLI install did not enable kube-proxy replacement:\n%s", before)
	}
	if !strings.Contains(before, "clustermesh") {
		t.Fatalf("the CLI install did not enable the mesh:\n%s", before)
	}

	// --- 2. The ConfigMap the hand-off depends on --------------------------
	rendered, err := exec.Command("kubectl", "--context", targetClusterContextName(),
		"-n", "kube-system", "get", "configmap", bootstrap.CiliumRuntimeValuesName,
		"-o", "jsonpath={.data.values\\.yaml}").CombinedOutput()
	if err != nil {
		t.Fatalf("the CLI did not publish %s, so Flux has nothing to adopt with:\n%v\n%s",
			bootstrap.CiliumRuntimeValuesName, err, rendered)
	}
	if !strings.Contains(string(rendered), "k8sServiceHost") {
		t.Fatalf("published values are missing k8sServiceHost:\n%s", rendered)
	}
	if !strings.Contains(string(rendered), "clustermesh") {
		t.Fatalf("published values are missing clustermesh:\n%s", rendered)
	}
	t.Logf("published runtime values:\n%s", rendered)

	// --- 3. Install Flux and apply the repo's own HelmRelease ---------------
	t.Log("installing flux controllers")
	export, err := exec.Command("flux", "install", "--export").CombinedOutput()
	if err != nil {
		t.Fatalf("flux install --export: %v\n%s", err, export)
	}
	if out, err := kubectlApply(ctx, export); err != nil {
		t.Fatalf("apply flux manifests: %v\n%s", err, out)
	}

	t.Log("applying the repo's HelmRepository for cilium")
	repoDir := filepath.Join(repoRoot, "2-addons", "manifests", "helm-repositories")
	repoManifests, err := filepath.Glob(filepath.Join(repoDir, "*.yaml"))
	if err != nil || len(repoManifests) == 0 {
		t.Fatalf("no HelmRepository manifests in %s", repoDir)
	}
	for _, path := range repoManifests {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(body), "name: cilium") {
			continue
		}
		if out, err := kubectlApply(ctx, body); err != nil {
			t.Fatalf("apply %s: %v\n%s", path, err, out)
		}
	}

	t.Log("applying 2-addons/manifests/helm-releases/cilium.yaml verbatim")
	body, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if out, err := kubectlApply(ctx, body); err != nil {
		t.Fatalf("apply cilium.yaml: %v\n%s", err, out)
	}

	// --- 4. Let Flux adopt it ----------------------------------------------
	t.Log("asking flux to reconcile")
	out, _ := exec.Command("flux", "reconcile", "helmrelease", "cilium",
		"-n", "kube-system", "--with-source", "--timeout", "5m").CombinedOutput()
	t.Logf("flux reconcile:\n%s", out)

	status := waitForHelmReleaseReady(t, "cilium", 8*time.Minute)
	t.Logf("HelmRelease became Ready after adoption")
	dumpHelmRelease(t, status)

	// --- 5. The whole point: the config survived adoption ------------------
	after := helmValues(t)
	t.Logf("values after flux adoption:\n%s", after)

	if !strings.Contains(after, "kubeProxyReplacement: true") {
		t.Errorf("flux adoption removed kubeProxyReplacement:\n%s", after)
	}
	if !strings.Contains(after, "clustermesh") {
		t.Errorf("flux adoption removed the clustermesh block:\n%s", after)
	}
	if !strings.Contains(after, "k8sServiceHost") {
		t.Errorf("flux adoption removed k8sServiceHost:\n%s", after)
	}
	// Count the exact key, not a substring: the chart also has
	// `k8sServiceHostRef`, which a naive strings.Count would double-count.
	if got := strings.Count(after, "\nk8sServiceHost:"); got != 1 {
		t.Errorf("k8sServiceHost is set %d times, want exactly 1; a second source of truth will drift", got)
	}
	if !strings.Contains(after, "1.20.2") && !strings.Contains(after, "version") {
		t.Log("chart version not visible in values (informational)")
	}

	// And the datapath still works after Flux took ownership.
	if out, err := exec.Command("cilium", "--context", targetClusterContextName(),
		"status", "--wait", "5m").CombinedOutput(); err != nil {
		t.Errorf("cilium is not healthy after flux adoption: %v\n%s", err, out)
	}
}

func kubectlApply(ctx context.Context, body []byte) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl", "--context", targetClusterContextName(),
		"apply", "-f", "-")
	cmd.Stdin = strings.NewReader(string(body))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func helmValues(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("helm", "get", "values", "cilium", "-n", "kube-system",
		"--kube-context", targetClusterContextName(), "-a").CombinedOutput()
	if err != nil {
		t.Fatalf("helm get values: %v\n%s", err, out)
	}
	return string(out)
}

// waitForHelmReleaseReady polls until the Ready condition is true.
//
// Polling matters: helm-controller retries a failed action on its interval, and
// checking Ready once immediately after a reconcile reports a transient failure
// as a permanent one.
func waitForHelmReleaseReady(t *testing.T, name string, timeout time.Duration) *unstructured.Unstructured {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last *unstructured.Unstructured
	for {
		obj, err := readHelmRelease(name)
		if err == nil {
			last = obj
			if conditionTrue(obj, "Ready") {
				return obj
			}
		}
		if time.Now().After(deadline) {
			if last != nil {
				dumpHelmRelease(t, last)
			}
			t.Fatalf("HelmRelease %q was not Ready within %s; flux did not adopt the CLI's release cleanly", name, timeout)
		}
		time.Sleep(15 * time.Second)
	}
}

func readHelmRelease(name string) (*unstructured.Unstructured, error) {
	out, err := exec.Command("kubectl", "--context", targetClusterContextName(),
		"-n", "kube-system", "get", "helmrelease", name, "-o", "json").Output()
	if err != nil {
		return nil, err
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: parsed}, nil
}

func conditionTrue(obj *unstructured.Unstructured, conditionType string) bool {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, raw := range conditions {
		condition, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if condition["type"] == conditionType && condition["status"] == "True" {
			return true
		}
	}
	return false
}

func dumpHelmRelease(t *testing.T, obj *unstructured.Unstructured) {
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, raw := range conditions {
		if condition, ok := raw.(map[string]interface{}); ok {
			t.Logf("condition %v=%v: %v %v", condition["type"], condition["status"],
				condition["reason"], condition["message"])
		}
	}
}
