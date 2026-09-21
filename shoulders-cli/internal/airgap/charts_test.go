package airgap

import (
	"os"
	"path/filepath"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../../")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "2-addons", "manifests", "helm-releases")); err != nil {
		t.Skipf("not inside the shoulders checkout: %v", err)
	}
	return root
}

func TestChartInventoryCoversAllReleases(t *testing.T) {
	entries, err := ChartInventory(repoRoot(t))
	if err != nil {
		t.Fatalf("inventory failed: %v", err)
	}
	if len(entries) < 15 {
		t.Fatalf("expected at least 15 chart entries, got %d", len(entries))
	}
	byRelease := map[string]ChartEntry{}
	for _, entry := range entries {
		if entry.Release != "" {
			byRelease[entry.Release] = entry
		}
		if entry.Version == "" || entry.Version == "latest" {
			// The git-backed garage chart gets its version from Chart.yaml
			// at vendor time.
			if entry.Chart == "garage" && entry.Version == "" {
				continue
			}
			t.Fatalf("chart %s has unpinned version %q", entry.Chart, entry.Version)
		}
		if entry.Release != "" && entry.OCIRepo == "" {
			t.Fatalf("release %s is missing its OCI repo name", entry.Release)
		}
	}
	for _, want := range []string{"cilium", "crossplane", "strimzi", "garage", "headlamp", "kube-prometheus-stack", "kyverno", "dex", "loki", "tempo"} {
		if _, ok := byRelease[want]; !ok {
			t.Fatalf("expected release %s in inventory", want)
		}
	}
	garage := byRelease["garage"]
	if garage.RepoURL == "" {
		t.Fatalf("expected garage entry to carry its git URL")
	}
	foundCLI := false
	for _, entry := range entries {
		if entry.Release == "" && entry.Chart == "cilium" {
			foundCLI = true
		}
	}
	if !foundCLI {
		t.Fatalf("expected CLI-managed cilium chart entry")
	}
}

func TestOCIRepoNameIsDNSSafe(t *testing.T) {
	for input, want := range map[string]string{
		"kube-prometheus-stack":       "chart-kube-prometheus-stack",
		"trivy-operator-polr-adapter": "chart-trivy-operator-polr-adapter",
		"Garage_Admin":                "chart-garage-admin",
	} {
		if got := ociRepoName(input); got != want {
			t.Fatalf("ociRepoName(%q) = %q, want %q", input, got, want)
		}
	}
}
