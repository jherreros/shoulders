package airgap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func testMeta() *BundleMeta {
	return &BundleMeta{
		Format:       BundleFormatVersion,
		ShouldersRef: "abc123",
		Charts: []ChartEntry{
			{Release: "dex", Chart: "dex", Version: "0.24.1", File: "dex-0.24.1.tgz", OCIRepo: "chart-dex"},
			{Release: "headlamp", Chart: "headlamp", Version: "0.43.0", File: "headlamp-0.43.0.tgz", OCIRepo: "chart-headlamp"},
		},
	}
}

func TestStageManifestsRewrites(t *testing.T) {
	extractDir := bundleExtractFixture(t, repoRoot(t))
	staged, cleanup, err := StageManifests(testMeta(), extractDir, "registry.test:5000", true)
	if err != nil {
		t.Fatalf("stage failed: %v", err)
	}
	defer cleanup()

	// Release chart specs repointed at the generated HelmRepositories.
	for _, tc := range []struct{ file, release, repo string }{
		{"manifests/helm-releases/dex.yaml", "dex", "chart-dex"},
		{"manifests/helm-releases/headlamp.yaml", "headlamp", "chart-headlamp"},
	} {
		content, err := os.ReadFile(filepath.Join(staged, tc.file))
		if err != nil {
			t.Fatalf("read staged %s: %v", tc.file, err)
		}
		if !strings.Contains(string(content), "kind: HelmRepository") || !strings.Contains(string(content), "name: "+tc.repo) {
			t.Fatalf("staged %s does not reference %s:\n%s", tc.file, tc.repo, content)
		}
		if strings.Contains(string(content), "kind: OCIRepository") {
			t.Fatalf("staged %s uses unsupported OCIRepository chart sourceRef", tc.file)
		}
	}

	// Generated chart sources exist with the registry host.
	generated, err := os.ReadFile(filepath.Join(staged, "manifests", "helm-repositories", "bundle-oci-charts.yaml"))
	if err != nil {
		t.Fatalf("read generated chart sources: %v", err)
	}
	for _, want := range []string{
		"kind: HelmRepository",
		"name: chart-dex",
		"type: oci",
		"url: oci://registry.test:5000/charts",
		"insecure: true",
	} {
		if !strings.Contains(string(generated), want) {
			t.Fatalf("generated sources missing %q:\n%s", want, generated)
		}
	}

	// The generated file is registered in the Kustomization resources
	// (explicit list — unregistered files are silently ignored by Flux).
	kustomization, err := os.ReadFile(filepath.Join(staged, "manifests", "helm-repositories", "kustomization.yaml"))
	if err != nil {
		t.Fatalf("read staged kustomization: %v", err)
	}
	if !strings.Contains(string(kustomization), "bundle-oci-charts.yaml") {
		t.Fatalf("staged kustomization does not reference bundle-oci-charts.yaml:\n%s", kustomization)
	}

	// Headlamp plugin sidecar disabled.
	headlamp, err := os.ReadFile(filepath.Join(staged, "manifests", "helm-releases", "headlamp.yaml"))
	if err != nil {
		t.Fatalf("read staged headlamp: %v", err)
	}
	docs, err := splitYAMLDocsFromBytes(headlamp)
	if err != nil {
		t.Fatalf("split staged headlamp: %v", err)
	}
	found := false
	for _, doc := range docs {
		var parsed map[string]interface{}
		if err := yaml.Unmarshal(doc, &parsed); err != nil {
			t.Fatalf("parse staged headlamp: %v", err)
		}
		if parsed["kind"] != "HelmRelease" {
			continue
		}
		values := parsed["spec"].(map[string]interface{})["values"].(map[string]interface{})
		plugins := values["pluginsManager"].(map[string]interface{})
		if enabled, _ := plugins["enabled"].(bool); enabled {
			t.Fatalf("expected pluginsManager.enabled=false in staged headlamp")
		}
		found = true
	}
	if !found {
		t.Fatalf("no HelmRelease found in staged headlamp")
	}
}

// bundleExtractFixture builds a faithful bundle-extract directory: the real
// 2-addons tree packed as manifests.tar.gz, mirroring Vendor/AssembleBundle
// layout. Regression cover for installs failing when StageManifests looked
// for an unpacked 2-addons directory instead of the nested archive.
func bundleExtractFixture(t *testing.T, root string) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("tar", "-czf", filepath.Join(dir, BundleManifestsTar), "-C", root, "2-addons")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pack manifests fixture: %v\n%s", err, out)
	}
	return dir
}

func TestStageManifestsRequiresPackedArchive(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := StageManifests(testMeta(), dir, "registry.test:5000", true); err == nil {
		t.Fatalf("expected error when manifests.tar.gz is absent")
	}
}

// Git-backed releases (garage) reference a repository path as chart name.
// The rewrite must replace it with the vendored chart name/version or the
// HelmChart reconciliation fails with an invalid chart reference.
func TestRewriteReleaseChartReplacesPathChart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garage.yaml")
	content := `apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: garage
  namespace: garage
spec:
  chart:
    spec:
      chart: ./script/helm/garage
      sourceRef:
        kind: GitRepository
        name: garage
        namespace: flux-system
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rewrite := RewriteEntry{Release: "garage", Chart: "garage", Version: "0.9.3", OCIRepo: "chart-garage"}
	if err := rewriteReleaseChart(path, rewrite); err != nil {
		t.Fatalf("rewrite failed: %v", err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"chart: garage",
		"version: 0.9.3",
		"kind: HelmRepository",
		"name: chart-garage",
	} {
		if !strings.Contains(string(updated), want) {
			t.Fatalf("rewritten release missing %q:\n%s", want, updated)
		}
	}
	if strings.Contains(string(updated), "./script/helm/garage") {
		t.Fatalf("rewritten release still references the git path:\n%s", updated)
	}
}

func splitYAMLDocsFromBytes(content []byte) ([][]byte, error) {
	parts := strings.Split(string(content), "\n---")
	docs := make([][]byte, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" || trimmed == "---" {
			continue
		}
		docs = append(docs, []byte(trimmed))
	}
	return docs, nil
}
