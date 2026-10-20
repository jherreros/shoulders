package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestDesugarsLegacySingleCluster is decision D2: an existing monocluster
// config file must keep working, unchanged, and resolve to a one-cluster
// topology labelled hub.
func TestDesugarsLegacySingleCluster(t *testing.T) {
	path := writeConfig(t, `
cluster:
  name: shoulders
  provider: vind
platform:
  profile: small
  cilium:
    enabled: true
    version: "1.19.2"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("legacy config failed to load: %v", err)
	}

	topology, err := cfg.Topology()
	if err != nil {
		t.Fatalf("topology: %v", err)
	}
	if len(topology.Clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(topology.Clusters))
	}
	cluster := topology.Clusters[0]
	if cluster.Name != "shoulders" {
		t.Fatalf("name = %q", cluster.Name)
	}
	if cluster.Role != RoleHub {
		t.Fatalf("role = %q, want %q", cluster.Role, RoleHub)
	}
	if cluster.Profile != ProfileSmall {
		t.Fatalf("profile = %q", cluster.Profile)
	}
	if cluster.Provider != ProviderVind {
		t.Fatalf("provider = %q", cluster.Provider)
	}
	if !cluster.CiliumEnabled() {
		t.Fatal("cilium should be inherited and enabled")
	}
	if cluster.CiliumVersion() != "1.19.2" {
		t.Fatalf("cilium version = %q", cluster.CiliumVersion())
	}
	if cluster.Ports.HTTP != 80 || cluster.Ports.HTTPS != 443 {
		t.Fatalf("default ports = %d/%d, want 80/443", cluster.Ports.HTTP, cluster.Ports.HTTPS)
	}
	if len(topology.Hubs()) != 1 {
		t.Fatalf("expected 1 hub, got %d", len(topology.Hubs()))
	}
}

// TestDesugarPreservesLegacyDefaults compares a legacy config against the same
// config with an explicit one-entry clusters list: they must resolve
// identically. That is the whole promise of the shim.
func TestDesugarPreservesLegacyDefaults(t *testing.T) {
	legacy := `
cluster:
  name: shoulders
  provider: existing
  context: kind-dev
platform:
  profile: medium
  domain: "example.test"
  flux:
    source: git
    pathPrefix: "."
`
	explicit := `
platform:
  profile: medium
  domain: "example.test"
  flux:
    source: git
    pathPrefix: "."
clusters:
  - name: shoulders
    role: hub
    provider: existing
    context: kind-dev
`
	a, err := Load(writeConfig(t, legacy))
	if err != nil {
		t.Fatalf("legacy: %v", err)
	}
	b, err := Load(writeConfig(t, explicit))
	if err != nil {
		t.Fatalf("explicit: %v", err)
	}

	ta, err := a.Topology()
	if err != nil {
		t.Fatal(err)
	}
	tb, err := b.Topology()
	if err != nil {
		t.Fatal(err)
	}
	ca, cb := ta.Clusters[0], tb.Clusters[0]
	if ca.Name != cb.Name || ca.Role != cb.Role || ca.Provider != cb.Provider || ca.Context != cb.Context {
		t.Fatalf("identity mismatch:\n legacy: %+v\n explicit: %+v", ca, cb)
	}
	if ca.Profile != cb.Profile || ca.Domain != cb.Domain || ca.FluxPathPrefix() != cb.FluxPathPrefix() {
		t.Fatalf("platform mismatch:\n legacy: %+v\n explicit: %+v", ca.PlatformConfig, cb.PlatformConfig)
	}
	if ca.Ports != cb.Ports {
		t.Fatalf("ports mismatch: %+v vs %+v", ca.Ports, cb.Ports)
	}
}

// TestOverlayInheritsSharedPlatform is the "overlay on shared platform" model:
// an entry states only what differs.
func TestOverlayInheritsSharedPlatform(t *testing.T) {
	path := writeConfig(t, `
platform:
  profile: small
  domain: "example.test"
  flux:
    source: git
    gitRepository:
      url: "https://github.com/jherreros/shoulders.git"
      branch: main
    pathPrefix: "."
clusters:
  - name: hub
    role: hub
    domain: "hub.example.test"
  - name: spoke-a
    role: spoke
    ports: { http: 8080, https: 8443 }
    domain: "spoke-a.example.test"
    flux:
      pathPrefix: "clusters/spoke-a"
  - name: spoke-b
    role: spoke
    ports: { http: 8081, https: 8444 }
    domain: "spoke-b.example.test"
    flux:
      pathPrefix: "clusters/spoke-b"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	topology, err := cfg.Topology()
	if err != nil {
		t.Fatalf("topology: %v", err)
	}
	if len(topology.Clusters) != 3 {
		t.Fatalf("expected 3 clusters, got %d", len(topology.Clusters))
	}

	hub, _ := topology.Get("hub")
	spokeA, _ := topology.Get("spoke-a")
	spokeB, _ := topology.Get("spoke-b")

	// Shared values inherited everywhere.
	for name, cluster := range map[string]*ResolvedCluster{"hub": hub, "spoke-a": spokeA, "spoke-b": spokeB} {
		if cluster.Profile != ProfileSmall {
			t.Fatalf("%s: profile = %q, want inherited small", name, cluster.Profile)
		}
		// The domain is per cluster: two clusters may not share one, because
		// the platform hostnames would collide.
		wantDomain := map[string]string{
			"hub": "hub.example.test", "spoke-a": "spoke-a.example.test", "spoke-b": "spoke-b.example.test",
		}[name]
		if cluster.Domain != wantDomain {
			t.Fatalf("%s: domain = %q, want %q", name, cluster.Domain, wantDomain)
		}
		if cluster.FluxRepositoryURL() != "https://github.com/jherreros/shoulders.git" {
			t.Fatalf("%s: flux url = %q, want inherited", name, cluster.FluxRepositoryURL())
		}
		if cluster.FluxRepositoryBranch() != "main" {
			t.Fatalf("%s: flux branch = %q, want inherited", name, cluster.FluxRepositoryBranch())
		}
	}

	// Per-cluster overrides applied. The flux path is only set when the
	// operator configures one; the tier difference between clusters is
	// expressed as delete patches on the flux Kustomizations, not as a
	// different path.
	if hub.FluxPathPrefix() != "." {
		t.Fatalf("hub pathPrefix = %q", hub.FluxPathPrefix())
	}
	if spokeA.FluxPathPrefix() != "clusters/spoke-a" {
		t.Fatalf("spoke-a pathPrefix = %q", spokeA.FluxPathPrefix())
	}
	if spokeB.FluxPathPrefix() != "clusters/spoke-b" {
		t.Fatalf("spoke-b pathPrefix = %q", spokeB.FluxPathPrefix())
	}
}

// TestOverlayDistinguishesUnsetFromFalse is why Insecure became a pointer: an
// entry must be able to turn off a platform-level true.
func TestOverlayDistinguishesUnsetFromFalse(t *testing.T) {
	path := writeConfig(t, `
platform:
  flux:
    source: oci
    ociRepository:
      url: "oci://registry.example/shoulders"
      tag: "v1"
      insecure: true
clusters:
  - name: hub
    role: hub
    domain: "hub.example.test"
  - name: spoke-a
    role: spoke
    ports: { http: 8080, https: 8443 }
    flux:
      ociRepository:
        insecure: false
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	topology, _ := cfg.Topology()

	hub, _ := topology.Get("hub")
	spokeA, _ := topology.Get("spoke-a")

	if !hub.FluxOCIInsecure() {
		t.Fatal("hub should inherit insecure: true")
	}
	if spokeA.FluxOCIInsecure() {
		t.Fatal("spoke-a explicitly set insecure: false and must not inherit true")
	}
	// Both still inherit the rest of the block.
	for name, cluster := range map[string]*ResolvedCluster{"hub": hub, "spoke-a": spokeA} {
		if cluster.FluxOCIURL() != "oci://registry.example/shoulders" {
			t.Fatalf("%s: oci url = %q, want inherited", name, cluster.FluxOCIURL())
		}
		if cluster.FluxOCITag() != "v1" {
			t.Fatalf("%s: oci tag = %q, want inherited", name, cluster.FluxOCITag())
		}
	}
}

func TestCiliumEnabledCanBeOverriddenPerCluster(t *testing.T) {
	path := writeConfig(t, `
platform:
  cilium:
    enabled: true
    version: "1.19.2"
clusters:
  - name: hub
    role: hub
    domain: "hub.example.test"
  - name: spoke-a
    role: spoke
    ports: { http: 8080, https: 8443 }
    cilium:
      enabled: false
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	topology, _ := cfg.Topology()
	hub, _ := topology.Get("hub")
	spokeA, _ := topology.Get("spoke-a")

	if !hub.CiliumEnabled() {
		t.Fatal("hub should inherit cilium enabled")
	}
	if spokeA.CiliumEnabled() {
		t.Fatal("spoke-a explicitly disabled cilium")
	}
	if spokeA.CiliumVersion() != "1.19.2" {
		t.Fatalf("spoke-a should still inherit the version, got %q", spokeA.CiliumVersion())
	}
}

// TestRolesAreLabelsOnly is the guard for decision D1. A hub and a spoke with
// otherwise identical configuration must resolve to identical values, ports
// included. If a future change makes a role imply behaviour, this fails.
//
// Both clusters use `provider: existing` so that the shared default ports are
// legal: no vind container is created, so nothing binds them.
func TestRolesAreLabelsOnly(t *testing.T) {
	path := writeConfig(t, `
platform:
  profile: small
clusters:
  - { name: alpha, role: hub, provider: existing, domain: "alpha.example.test" }
  - { name: beta, role: spoke, provider: existing }
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	topology, _ := cfg.Topology()
	alpha, _ := topology.Get("alpha")
	beta, _ := topology.Get("beta")

	// The domain is excluded: the hub owns it (it is the topology's public
	// surface) and the spoke does not declare one. That is the topology's
	// rule, not the role's, so it is asserted separately below.
	alphaConfig, betaConfig := alpha.PlatformConfig, beta.PlatformConfig
	alphaConfig.Domain, betaConfig.Domain = "", ""
	if alphaConfig != betaConfig {
		t.Fatalf("role changed platform config:\n hub:   %+v\n spoke: %+v", alphaConfig, betaConfig)
	}
	if alpha.Domain != "alpha.example.test" {
		t.Fatalf("hub domain = %q, want the topology's own", alpha.Domain)
	}
	if beta.Domain != "" {
		t.Fatalf("spoke domain = %q, want none: the hub owns the domain", beta.Domain)
	}
	if alpha.Ports != beta.Ports {
		t.Fatalf("role changed ports:\n hub:   %+v\n spoke: %+v", alpha.Ports, beta.Ports)
	}
	if alpha.MeshEnabled() != beta.MeshEnabled() {
		t.Fatal("role changed mesh enablement")
	}
	if alpha.CiliumEnabled() != beta.CiliumEnabled() {
		t.Fatal("role changed cilium enablement")
	}
	// The flux path is explicit configuration, never inferred from the role.
	if alpha.FluxPathPrefix() != "." || beta.FluxPathPrefix() != "." {
		t.Fatalf("role changed the flux path: alpha=%q beta=%q",
			alpha.FluxPathPrefix(), beta.FluxPathPrefix())
	}
	if alpha.CiliumVersion() != beta.CiliumVersion() {
		t.Fatal("role changed cilium version")
	}
	if alpha.ProfileSpec() != beta.ProfileSpec() {
		t.Fatal("role changed the profile spec")
	}
}

// TestPortsOnlyConflictForVind is the provider-aware port rule: two existing
// clusters may share a port mapping, two vind clusters may not.
func TestPortsOnlyConflictForVind(t *testing.T) {
	path := writeConfig(t, `
clusters:
  - { name: dev, role: hub, provider: existing, domain: "dev.example.test" }
  - { name: staging, role: spoke, provider: existing }
`)
	if _, err := Load(path); err != nil {
		t.Fatalf("existing clusters should be allowed to share ports: %v", err)
	}

	vindConflict := `
clusters:
  - { name: a, role: hub }
  - { name: b, role: spoke }
`
	if _, err := Load(writeConfig(t, vindConflict)); err == nil {
		t.Fatal("two vind clusters must not be allowed to share host ports")
	}
}

func TestPerClusterPortsAreExplicit(t *testing.T) {
	path := writeConfig(t, `
clusters:
  - name: hub
    role: hub
    domain: "hub.example.test"
  - name: spoke-a
    role: spoke
    ports:
      http: 8080
      https: 8443
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	topology, err := cfg.Topology()
	if err != nil {
		t.Fatalf("topology: %v", err)
	}
	hub, _ := topology.Get("hub")
	spokeA, _ := topology.Get("spoke-a")
	if hub.Ports.HTTP != 80 || hub.Ports.HTTPS != 443 {
		t.Fatalf("hub ports = %+v", hub.Ports)
	}
	if spokeA.Ports.HTTP != 8080 || spokeA.Ports.HTTPS != 8443 {
		t.Fatalf("spoke-a ports = %+v", spokeA.Ports)
	}
}

func TestTopologyValidation(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		contains string
	}{
		{
			name: "duplicate names",
			body: `
clusters:
  - { name: a, role: hub }
  - { name: a, role: spoke }
`,
			contains: "duplicate cluster name",
		},
		{
			name: "unknown role",
			body: `
clusters:
  - { name: a, role: middle }
`,
			contains: "unknown role",
		},
		{
			name: "no hub",
			body: `
clusters:
  - { name: a, role: spoke }
`,
			contains: "exactly one hub",
		},
		{
			name: "two hubs",
			body: `
clusters:
  - { name: a, role: hub, domain: "a.example.test" }
  - { name: b, role: hub, domain: "b.example.test", ports: { http: 8080, https: 8443 } }
`,
			contains: "exactly one hub",
		},
		{
			name: "missing name",
			body: `
clusters:
  - { role: hub }
`,
			contains: "no name",
		},
		{
			name: "bad profile",
			body: `
platform: { profile: enormous }
clusters:
  - { name: a, role: hub }
`,
			contains: "unsupported platform profile",
		},
		{
			name: "duplicate host port",
			body: `
clusters:
  - { name: a, role: hub, ports: { http: 80, https: 443 } }
  - { name: b, role: spoke, ports: { http: 80, https: 8443 } }
`,
			contains: "both bind host http port 80",
		},
		{
			name: "port out of range",
			body: `
clusters:
  - { name: a, role: hub, ports: { http: 99999, https: 443 } }
`,
			contains: "out of range",
		},
		{
			name: "http equals https",
			body: `
clusters:
  - { name: a, role: hub, ports: { http: 8080, https: 8080 } }
`,
			contains: "must differ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.body))
			if err == nil {
				t.Fatal("expected a validation error")
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("error %q should contain %q", err, tc.contains)
			}
		})
	}
}

func TestTopologyAcceptsValidConfig(t *testing.T) {
	path := writeConfig(t, `
platform:
  profile: small
clusters:
  - { name: hub, role: hub, domain: "hub.example.test" }
  - { name: spoke-a, role: spoke, ports: { http: 8080, https: 8443 } }
  - { name: edge, role: edge, ports: { http: 8181, https: 8182 } }
`)
	if _, err := Load(path); err != nil {
		t.Fatalf("valid topology rejected: %v", err)
	}
}

func TestResolveClusterAndDefaultCluster(t *testing.T) {
	path := writeConfig(t, `
platform: { profile: small }
clusters:
  - { name: hub, role: hub, domain: "hub.example.test" }
  - { name: spoke-a, role: spoke, ports: { http: 8080, https: 8443 } }
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	spoke, err := cfg.ResolveCluster("spoke-a")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if spoke.Ports.HTTP != 8080 {
		t.Fatalf("resolved ports = %+v", spoke.Ports)
	}

	if _, err := cfg.ResolveCluster("nope"); err == nil {
		t.Fatal("expected an error for an unknown cluster")
	}

	// With more than one cluster the default is the single hub.
	def, err := cfg.DefaultCluster()
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if def.Name != "hub" {
		t.Fatalf("default cluster = %q, want hub", def.Name)
	}
}

func TestDefaultClusterSingleEntry(t *testing.T) {
	cfg, err := Load(writeConfig(t, "cluster: { name: solo }\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	def, err := cfg.DefaultCluster()
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if def.Name != "solo" {
		t.Fatalf("default = %q", def.Name)
	}
}

// TestMeshOverlayIsPerCluster proves the mesh block can be turned on for one
// cluster without affecting another.
func TestMeshOverlayIsPerCluster(t *testing.T) {
	path := writeConfig(t, `
platform:
  profile: small
  mesh:
    enabled: true
    network: shoulders-mesh
clusters:
  - { name: hub, role: hub, domain: "hub.example.test" }
  - { name: spoke-a, role: spoke, ports: { http: 8080, https: 8443 }, mesh: { podCIDRBase: 100 } }
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	topology, _ := cfg.Topology()
	hub, _ := topology.Get("hub")
	spokeA, _ := topology.Get("spoke-a")

	if !hub.MeshEnabled() || !spokeA.MeshEnabled() {
		t.Fatal("both clusters should have the mesh enabled")
	}
	if hub.MeshNetwork() != "shoulders-mesh" || spokeA.MeshNetwork() != "shoulders-mesh" {
		t.Fatal("network should be inherited by both")
	}
	if hub.MeshPodCIDRBase() == spokeA.MeshPodCIDRBase() {
		t.Fatalf("expected spoke-a's base override to differ, both are %d", hub.MeshPodCIDRBase())
	}
	if hub.ClusterPodCIDR() == spokeA.ClusterPodCIDR() {
		t.Fatalf("clusters must not share a pod CIDR: both got %s", hub.ClusterPodCIDR())
	}
}

func TestTopologySurvivesSaveRoundTrip(t *testing.T) {
	path := writeConfig(t, `
platform: { profile: small }
clusters:
  - { name: hub, role: hub, domain: "hub.example.test" }
  - { name: spoke-a, role: spoke, ports: { http: 8080, https: 8443 }, flux: { pathPrefix: "clusters/spoke-a" } }
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := Save(cfg, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	topology, err := reloaded.Topology()
	if err != nil {
		t.Fatalf("topology after round trip: %v", err)
	}
	if len(topology.Clusters) != 2 {
		t.Fatalf("expected 2 clusters after round trip, got %d", len(topology.Clusters))
	}
	spokeA, _ := topology.Get("spoke-a")
	if spokeA.Ports.HTTP != 8080 || spokeA.Ports.HTTPS != 8443 {
		t.Fatalf("ports lost in round trip: %+v", spokeA.Ports)
	}
	if spokeA.FluxPathPrefix() != "clusters/spoke-a" {
		t.Fatalf("pathPrefix lost in round trip: %q", spokeA.FluxPathPrefix())
	}
	if spokeA.Role != RoleSpoke {
		t.Fatalf("role lost in round trip: %q", spokeA.Role)
	}
}

// TestLegacyConfigRoundTripIsUnchanged guards the shim from rewriting existing
// files into the new shape.
func TestLegacyConfigRoundTripIsUnchanged(t *testing.T) {
	path := writeConfig(t, `
cluster:
  name: shoulders
  provider: vind
platform:
  profile: small
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := Save(cfg, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(saved), "clusters:") {
		t.Fatalf("saving a legacy config must not synthesise a clusters block:\n%s", saved)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("legacy config failed to reload after save: %v", err)
	}
}

// TestTopologyRejectsSharedDomain guards the collision that two clusters
// sharing a platform.domain would cause.
//
// The platform hostnames become HTTPRoute hostnames, so a shared domain means
// one hostname for two clusters — of which only one is reachable — and two Dex
// instances publishing the same OIDC issuer URL with different signing keys,
// so a token minted by one is rejected by the other.
func TestTopologyRejectsSharedDomain(t *testing.T) {
	path := writeConfig(t, `
platform:
  profile: small
  domain: "example.test"
clusters:
  - name: hub
    role: hub
  - name: spoke-a
    role: spoke
    ports: { http: 8080, https: 8443 }
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected a shared platform.domain to be rejected")
	}
	if !strings.Contains(err.Error(), "example.test") {
		t.Fatalf("the error should name the colliding domain, got %v", err)
	}
	if !strings.Contains(err.Error(), "OIDC issuer") {
		t.Fatalf("the error should explain the consequence, got %v", err)
	}
}
