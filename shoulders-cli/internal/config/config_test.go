package config

import (
	"os"
	"path/filepath"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestConfigSaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	enabled := false
	cfg := &Config{
		CurrentWorkspace: "team-a",
		Cluster: ClusterConfig{
			Provider: ProviderExisting,
			Context:  "demo-context",
		},
		Platform: PlatformConfig{
			Cilium: CiliumConfig{Enabled: &enabled},
		},
	}
	if err := Save(cfg); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if loaded.CurrentWorkspace != "team-a" {
		t.Fatalf("expected workspace team-a, got %s", loaded.CurrentWorkspace)
	}
	if loaded.Provider() != ProviderExisting {
		t.Fatalf("expected existing provider, got %s", loaded.Provider())
	}
	if loaded.Cluster.Context != "demo-context" {
		t.Fatalf("expected demo-context, got %s", loaded.Cluster.Context)
	}
	if loaded.CiliumEnabled() {
		t.Fatalf("expected cilium disabled after load")
	}
	if loaded.FluxPathPrefix() != "." {
		t.Fatalf("expected default flux path prefix '.', got %s", loaded.FluxPathPrefix())
	}
	if loaded.Profile() != ProfileMedium {
		t.Fatalf("expected default platform profile %s, got %s", ProfileMedium, loaded.Profile())
	}

	configPath, err := Path()
	if err != nil {
		t.Fatalf("path failed: %v", err)
	}
	expected := filepath.Join(tmpDir, ".shoulders", "config.yaml")
	if configPath != expected {
		t.Fatalf("expected config path %s, got %s", expected, configPath)
	}
}

func TestLoadDefaultsWithoutConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	loaded, err := Load()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if loaded.Provider() != ProviderVind {
		t.Fatalf("expected vind provider, got %s", loaded.Provider())
	}
	if loaded.ClusterName() != DefaultClusterName {
		t.Fatalf("expected default cluster name %s, got %s", DefaultClusterName, loaded.ClusterName())
	}
	if !loaded.CiliumEnabled() {
		t.Fatalf("expected cilium enabled by default")
	}
	if loaded.DexHost() != DefaultDexHost {
		t.Fatalf("expected default dex host %s, got %s", DefaultDexHost, loaded.DexHost())
	}
	if loaded.Profile() != DefaultPlatformProfile {
		t.Fatalf("expected default profile %s, got %s", DefaultPlatformProfile, loaded.Profile())
	}
}

func TestLoadWithOverridesWithoutConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "missing.yaml")

	loaded, err := LoadWithOverrides([]string{
		"cluster.provider=existing",
		"cluster.context=kind-dev",
		"cluster.kubeconfig=/tmp/kubeconfig",
		"platform.profile=small",
		"platform.domain=lvh.me",
		"platform.cilium.version=1.20.0",
		"platform.flux.gitRepository.url=https://example.com/shoulders.git",
		"platform.flux.gitRepository.branch=feature/config",
		"platform.flux.pathPrefix=platform",
	}, configPath)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if loaded.Provider() != ProviderExisting {
		t.Fatalf("expected existing provider, got %s", loaded.Provider())
	}
	if loaded.CiliumEnabled() {
		t.Fatalf("expected cilium disabled by existing provider default")
	}
	if loaded.Cluster.Context != "kind-dev" {
		t.Fatalf("expected context override, got %s", loaded.Cluster.Context)
	}
	if loaded.Cluster.Kubeconfig != "/tmp/kubeconfig" {
		t.Fatalf("expected kubeconfig override, got %s", loaded.Cluster.Kubeconfig)
	}
	if loaded.Profile() != ProfileSmall {
		t.Fatalf("expected small profile, got %s", loaded.Profile())
	}
	if loaded.GrafanaHost() != "grafana.lvh.me" {
		t.Fatalf("expected custom grafana host, got %s", loaded.GrafanaHost())
	}
	if loaded.CiliumVersion() != "1.20.0" {
		t.Fatalf("expected cilium version override, got %s", loaded.CiliumVersion())
	}
	if loaded.FluxRepositoryURL() != "https://example.com/shoulders.git" {
		t.Fatalf("expected flux url override, got %s", loaded.FluxRepositoryURL())
	}
	if loaded.FluxRepositoryBranch() != "feature/config" {
		t.Fatalf("expected flux branch override, got %s", loaded.FluxRepositoryBranch())
	}
	if loaded.FluxPathPrefix() != "platform" {
		t.Fatalf("expected flux path prefix override, got %s", loaded.FluxPathPrefix())
	}
}

func TestLoadWithOverridesWinsOverConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "shoulders.yaml")
	content := []byte("current_workspace: team-a\ncluster:\n  provider: vind\n  name: from-file\nplatform:\n  profile: medium\n  cilium:\n    enabled: true\n")
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	loaded, err := LoadWithOverrides([]string{
		"current_workspace=team-b",
		"cluster.name=from-set",
		"platform.profile=large",
		"platform.cilium.enabled=false",
	}, configPath)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if loaded.CurrentWorkspace != "team-b" {
		t.Fatalf("expected workspace override, got %s", loaded.CurrentWorkspace)
	}
	if loaded.ClusterName() != "from-set" {
		t.Fatalf("expected cluster name override, got %s", loaded.ClusterName())
	}
	if loaded.Profile() != ProfileLarge {
		t.Fatalf("expected large profile override, got %s", loaded.Profile())
	}
	if loaded.CiliumEnabled() {
		t.Fatalf("expected cilium disabled by override")
	}
}

func TestLoadWithOverridesRejectsInvalidEntries(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "missing.yaml")

	entries := []string{
		"cluster.provider",
		"=existing",
		"cluster.unknown=value",
		"platform.cilium.enabled=maybe",
	}
	for _, entry := range entries {
		t.Run(entry, func(t *testing.T) {
			_, err := LoadWithOverrides([]string{entry}, configPath)
			if err == nil {
				t.Fatalf("expected override %q to fail", entry)
			}
		})
	}
}

func TestConfigProfileRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfg := &Config{Platform: PlatformConfig{Profile: " SMALL "}}
	if err := Save(cfg); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if loaded.Profile() != ProfileSmall {
		t.Fatalf("expected profile %s, got %s", ProfileSmall, loaded.Profile())
	}
	if loaded.Platform.Profile != ProfileSmall {
		t.Fatalf("expected persisted normalized profile %s, got %s", ProfileSmall, loaded.Platform.Profile)
	}
}

func TestConfigInvalidProfile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "shoulders.yaml")
	content := []byte("platform:\n  profile: tiny\n")
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatalf("expected invalid profile error")
	}
}

func TestLoadAppliesProviderSpecificDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "shoulders.yaml")
	content := []byte("cluster:\n  provider: existing\n")
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if loaded.Provider() != ProviderExisting {
		t.Fatalf("expected existing provider, got %s", loaded.Provider())
	}
	if loaded.CiliumEnabled() {
		t.Fatalf("expected cilium disabled for existing cluster defaults")
	}
}

func TestCustomDomainHosts(t *testing.T) {
	cfg := &Config{Platform: PlatformConfig{Domain: "lvh.me"}}
	cfg.ApplyDefaults()

	if cfg.DexHost() != "dex.lvh.me" {
		t.Fatalf("expected custom dex host, got %s", cfg.DexHost())
	}
	if cfg.GrafanaHost() != "grafana.lvh.me" {
		t.Fatalf("expected custom grafana host, got %s", cfg.GrafanaHost())
	}
	if cfg.HeadlampHost() != "headlamp.lvh.me" {
		t.Fatalf("expected custom headlamp host, got %s", cfg.HeadlampHost())
	}
}

func TestExampleYAMLIsValid(t *testing.T) {
	content, err := ExampleYAML(ProviderExisting)
	if err != nil {
		t.Fatalf("example generation failed: %v", err)
	}

	var cfg Config
	if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatalf("example YAML should parse: %v", err)
	}
	if cfg.Cluster.Provider != ProviderExisting {
		t.Fatalf("expected existing provider in example, got %s", cfg.Cluster.Provider)
	}
	if cfg.Profile() != ProfileMedium {
		t.Fatalf("expected medium profile in example, got %s", cfg.Profile())
	}
	if cfg.FluxPathPrefix() != "." {
		t.Fatalf("expected default path prefix '.', got %s", cfg.FluxPathPrefix())
	}
}
