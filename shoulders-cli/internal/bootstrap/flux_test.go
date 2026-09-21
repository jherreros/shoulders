package bootstrap

import (
	"strings"
	"testing"

	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"sigs.k8s.io/yaml"
)

func TestFluxKustomizationsManifestMediumKeepsCurrentPaths(t *testing.T) {
	manifest := renderFluxKustomizationsManifest(t, config.ProfileMedium)
	assertYAMLDocuments(t, manifest)

	for _, want := range []string{
		"path: ./2-addons/manifests/helm-repositories",
		"path: ./2-addons/manifests/helm-releases",
		"path: ./2-addons/manifests/crossplane",
		"name: policy-reporter",
		"name: trivy-dashboard",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("expected medium manifest to contain %q\n%s", want, manifest)
		}
	}
	if !strings.Contains(manifest, "name: crossplane") || !strings.Contains(manifest, "path: ./2-addons/manifests/crossplane") || !strings.Contains(manifest, "name: shoulders-platform-config") {
		t.Fatalf("expected crossplane kustomization to use postBuild substitution\n%s", manifest)
	}
	assertBefore(t, manifest, "name: headlamp", "name: helm-releases")
}

func TestFluxKustomizationsManifestSmallUsesCorePaths(t *testing.T) {
	manifest := renderFluxKustomizationsManifest(t, config.ProfileSmall)
	assertYAMLDocuments(t, manifest)

	for _, want := range []string{
		"path: ./2-addons/profiles/small/helm-repositories",
		"path: ./2-addons/profiles/small/namespaces",
		"path: ./2-addons/profiles/small/helm-releases",
		"path: ./2-addons/profiles/small/crossplane",
		"path: ./2-addons/profiles/small/gateway",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("expected small manifest to contain %q\n%s", want, manifest)
		}
	}
	for _, omitted := range []string{"kind: Kustomization\nmetadata:\n  name: policy-reporter", "kind: Kustomization\nmetadata:\n  name: trivy-dashboard"} {
		if strings.Contains(manifest, omitted) {
			t.Fatalf("expected small manifest to omit %q\n%s", omitted, manifest)
		}
	}
}

func TestFluxKustomizationsManifestLargeUsesLargeHelmReleases(t *testing.T) {
	manifest := renderFluxKustomizationsManifest(t, config.ProfileLarge)
	assertYAMLDocuments(t, manifest)

	if !strings.Contains(manifest, "path: ./2-addons/profiles/large/helm-releases") {
		t.Fatalf("expected large manifest to use profile helm-releases path\n%s", manifest)
	}
	if !strings.Contains(manifest, "name: policy-reporter") {
		t.Fatalf("expected large manifest to keep policy-reporter\n%s", manifest)
	}
}

func TestFluxKustomizationsManifestOCIMirrorsGitPaths(t *testing.T) {
	for _, profile := range []string{config.ProfileSmall, config.ProfileMedium, config.ProfileLarge} {
		gitManifest := string(fluxKustomizationsManifestForSource(".", profile, "GitRepository"))
		ociManifest := string(fluxKustomizationsManifestForSource(".", profile, "OCIRepository"))
		assertYAMLDocuments(t, ociManifest)

		if !strings.Contains(ociManifest, "kind: OCIRepository") {
			t.Fatalf("expected OCI manifest for profile %s to use OCIRepository sourceRef\n%s", profile, ociManifest)
		}
		if strings.Contains(ociManifest, "kind: GitRepository") {
			t.Fatalf("expected OCI manifest for profile %s to have no GitRepository sourceRef\n%s", profile, ociManifest)
		}
		// Same paths, only the sourceRef kind differs.
		normalize := func(manifest string) string {
			manifest = strings.ReplaceAll(manifest, "kind: OCIRepository", "kind: SOURCEREF")
			return strings.ReplaceAll(manifest, "kind: GitRepository", "kind: SOURCEREF")
		}
		if normalize(gitManifest) != normalize(ociManifest) {
			t.Fatalf("expected OCI and git manifests for profile %s to differ only in sourceRef kind", profile)
		}
	}
}

func TestFluxOCIRepositoryManifest(t *testing.T) {
	manifest := string(fluxOCIRepositoryManifest("oci://registry.example.com/shoulders/addons", "local-abc123-1234567890", false))
	assertYAMLDocuments(t, manifest)
	for _, want := range []string{
		"kind: OCIRepository",
		"url: \"oci://registry.example.com/shoulders/addons\"",
		"tag: \"local-abc123-1234567890\"",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("expected OCI repository manifest to contain %q\n%s", want, manifest)
		}
	}
	if strings.Contains(manifest, "insecure: true") {
		t.Fatalf("expected secure OCI manifest to omit insecure\n%s", manifest)
	}

	insecureManifest := string(fluxOCIRepositoryManifest("oci://shoulders-registry.flux-system.svc.cluster.local:5000/shoulders/addons", "local-abc123", true))
	assertYAMLDocuments(t, insecureManifest)
	if !strings.Contains(insecureManifest, "insecure: true") {
		t.Fatalf("expected insecure OCI manifest to set insecure: true\n%s", insecureManifest)
	}
}

func TestFluxSourceManifestDispatch(t *testing.T) {
	git := FluxSource{Kind: config.FluxSourceGit, GitURL: "https://example.com/repo.git", GitBranch: "main"}
	if manifest := string(fluxSourceManifest(git)); !strings.Contains(manifest, "kind: GitRepository") {
		t.Fatalf("expected git source manifest to be a GitRepository\n%s", manifest)
	}
	oci := FluxSource{Kind: config.FluxSourceOCI, OCIURL: "oci://example.com/repo", OCITag: "v1"}
	if manifest := string(fluxSourceManifest(oci)); !strings.Contains(manifest, "kind: OCIRepository") {
		t.Fatalf("expected oci source manifest to be an OCIRepository\n%s", manifest)
	}
}

func TestFluxSourceFromConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	src := FluxSourceFromConfig(cfg, config.ProfileMedium)
	if src.Kind != config.FluxSourceGit {
		t.Fatalf("expected default source git, got %q", src.Kind)
	}
	if src.PathPrefix != "." || src.Profile != config.ProfileMedium {
		t.Fatalf("unexpected default source fields: %+v", src)
	}

	cfg.Platform.Flux.Source = "oci"
	cfg.Platform.Flux.OCIRepository.URL = "oci://example.com/shoulders"
	cfg.Platform.Flux.OCIRepository.Tag = "v1"
	src = FluxSourceFromConfig(cfg, config.ProfileSmall)
	if src.Kind != config.FluxSourceOCI || src.OCIURL != "oci://example.com/shoulders" || src.OCITag != "v1" {
		t.Fatalf("unexpected OCI source fields: %+v", src)
	}
}

func renderFluxKustomizationsManifest(t *testing.T, profile string) string {
	t.Helper()
	return string(fluxKustomizationsManifest(".", profile))
}

func assertYAMLDocuments(t *testing.T, manifest string) {
	t.Helper()
	for _, document := range strings.Split(strings.TrimSpace(manifest), "\n---\n") {
		var parsed map[string]interface{}
		if err := yaml.Unmarshal([]byte(document), &parsed); err != nil {
			t.Fatalf("generated document should be valid YAML: %v\n%s", err, document)
		}
	}
}

func assertBefore(t *testing.T, text, first, second string) {
	t.Helper()
	firstIndex := strings.Index(text, first)
	secondIndex := strings.Index(text, second)
	if firstIndex == -1 || secondIndex == -1 || firstIndex > secondIndex {
		t.Fatalf("expected %q to appear before %q\n%s", first, second, text)
	}
}

func TestFluxPlatformConfigManifestIncludesProfileDefaults(t *testing.T) {
	manifest := string(fluxPlatformConfigManifest(config.ProfileSmall, PublicDomainConfig{}))
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(manifest), &parsed); err != nil {
		t.Fatalf("platform config manifest should be valid YAML: %v\n%s", err, manifest)
	}

	for _, want := range []string{
		`SHOULDERS_PROFILE: "small"`,
		`SHOULDERS_POSTGRES_INSTANCES: "1"`,
		`SHOULDERS_KAFKA_REPLICAS: "1"`,
		`SHOULDERS_PROMETHEUS_RETENTION: "6h"`,
		`SHOULDERS_PROMETHEUS_RETENTION_SIZE: "512MiB"`,
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("expected platform config to contain %q\n%s", want, manifest)
		}
	}
}
