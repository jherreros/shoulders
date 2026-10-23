package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
)

func writeTargetTestConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

const twoClusterConfig = `
platform: { profile: small }
clusters:
  - { name: hub, role: hub, domain: "hub.example.test" }
  - { name: spoke-a, role: spoke, ports: { http: 8080, https: 8443 } }
`

// resolveForTest isolates the globals that resolveTargetCluster touches.
func resolveForTest(t *testing.T, configBody, clusterName string) (*config.ResolvedCluster, error) {
	t.Helper()
	cfg, err := config.Load(writeTargetTestConfig(t, configBody))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	prevName, prevKubeconfig := targetClusterName, kubeconfig
	t.Cleanup(func() { targetClusterName, kubeconfig = prevName, prevKubeconfig })

	targetClusterName = clusterName
	kubeconfig = ""
	return resolveTargetCluster(cfg)
}

func currentContext(t *testing.T) string {
	t.Helper()
	ctx, err := kube.CurrentContext(kubeconfig)
	if err != nil {
		t.Fatalf("current context: %v", err)
	}
	return ctx
}

func TestResolveTargetClusterDefaultsToHub(t *testing.T) {
	got, err := resolveForTest(t, twoClusterConfig, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Name != "hub" {
		t.Fatalf("default cluster = %q, want hub", got.Name)
	}
	// vind clusters resolve to the prefixed context name.
	if ctx := currentContext(t); ctx != "vcluster-docker_hub" {
		t.Fatalf("context override = %q, want vcluster-docker_hub", ctx)
	}
}

func TestResolveTargetClusterHonoursFlag(t *testing.T) {
	got, err := resolveForTest(t, twoClusterConfig, "spoke-a")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Name != "spoke-a" {
		t.Fatalf("cluster = %q", got.Name)
	}
	// The resolved cluster carries its own ports and overlay.
	if got.Ports.HTTP != 8080 || got.Ports.HTTPS != 8443 {
		t.Fatalf("ports = %+v", got.Ports)
	}
	if got.Profile != config.ProfileSmall {
		t.Fatalf("profile = %q, want inherited", got.Profile)
	}
	if ctx := currentContext(t); ctx != "vcluster-docker_spoke-a" {
		t.Fatalf("context override = %q", ctx)
	}
}

func TestResolveTargetClusterRejectsUnknown(t *testing.T) {
	if _, err := resolveForTest(t, twoClusterConfig, "nope"); err == nil {
		t.Fatal("expected an error for a cluster not in the topology")
	}
}

func TestResolveTargetClusterExistingProviderUsesConfiguredContext(t *testing.T) {
	body := `
clusters:
  - { name: dev, role: hub, provider: existing, context: kind-dev, domain: "dev.example.test" }
  - { name: prod, role: spoke, provider: existing, context: kind-prod, ports: { http: 8080, https: 8443 } }
`
	got, err := resolveForTest(t, body, "prod")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Context != "kind-prod" {
		t.Fatalf("context = %q", got.Context)
	}
	if ctx := currentContext(t); ctx != "kind-prod" {
		t.Fatalf("context override = %q", ctx)
	}
}

// TestResolveTargetClusterLegacySingleCluster is the regression guard for
// decision D2: a monocluster config must resolve to the same cluster and the
// same context as it did before the topology existed.
func TestResolveTargetClusterLegacySingleCluster(t *testing.T) {
	got, err := resolveForTest(t, "cluster: { name: shoulders, provider: existing, context: kind-shoulders }\n", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Name != "shoulders" {
		t.Fatalf("cluster = %q", got.Name)
	}
	if got.Role != config.RoleHub {
		t.Fatalf("role = %q, want hub", got.Role)
	}
	if ctx := currentContext(t); ctx != "kind-shoulders" {
		t.Fatalf("context override = %q", ctx)
	}
}

func TestResolveTargetClusterLegacyVindUsesPrefixedContext(t *testing.T) {
	got, err := resolveForTest(t, "cluster: { name: solo, provider: vind }\n", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.ProviderOrDefault() != config.ProviderVind {
		t.Fatalf("provider = %q", got.ProviderOrDefault())
	}
	if ctx := currentContext(t); ctx != "vcluster-docker_solo" {
		t.Fatalf("context override = %q", ctx)
	}
}
