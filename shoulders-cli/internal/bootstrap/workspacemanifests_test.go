package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// TestUserSpaceManifestsAreDeliverable guards the gap Phase 0 found: the
// directory existed, the compositions existed, and nothing delivered one to the
// other.
//
// The failure was silent — no error, no log line, just a sample tenant that had
// never run. These assertions are cheap and would have caught it: every file the
// kustomization lists exists, and every tenant resource is namespaced where the
// Workspace created the namespace.
func TestUserSpaceManifestsAreDeliverable(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	for _, dir := range []string{"workspaces", "team-a"} {
		base := filepath.Join(repoRoot, "3-user-space", dir)
		raw, err := os.ReadFile(filepath.Join(base, "kustomization.yaml"))
		if err != nil {
			t.Fatalf("read %s/kustomization.yaml: %v", dir, err)
		}
		var kustomization struct {
			Resources []string `yaml:"resources"`
		}
		if err := yaml.Unmarshal(raw, &kustomization); err != nil {
			t.Fatalf("parse %s/kustomization.yaml: %v", dir, err)
		}
		if len(kustomization.Resources) == 0 {
			t.Errorf("%s/kustomization.yaml delivers nothing", dir)
		}
		for _, resource := range kustomization.Resources {
			if _, err := os.Stat(filepath.Join(base, resource)); err != nil {
				t.Errorf("%s/kustomization.yaml lists %q, which does not exist", dir, resource)
			}
		}
	}

	// The tenant's resources must be in the namespace the Workspace owns.
	entries, err := os.ReadDir(filepath.Join(repoRoot, "3-user-space", "team-a"))
	if err != nil {
		t.Fatalf("read the sample tenant: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") || entry.Name() == "kustomization.yaml" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(repoRoot, "3-user-space", "team-a", entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		var parsed struct {
			Metadata struct {
				Namespace string `yaml:"namespace"`
			} `yaml:"metadata"`
		}
		if err := yaml.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		if parsed.Metadata.Namespace != "team-a" {
			t.Errorf("%s is in namespace %q; the sample tenant's resources belong in team-a",
				entry.Name(), parsed.Metadata.Namespace)
		}
	}

	// The webapp host has to come from the platform. A hardcoded host is how the
	// sample ends up pointing at a name that resolves nowhere.
	raw, err := os.ReadFile(filepath.Join(repoRoot, "3-user-space", "team-a", "webapp.yaml"))
	if err != nil {
		t.Fatalf("read the sample webapp: %v", err)
	}
	var webapp struct {
		Spec struct {
			Host string `yaml:"host"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(raw, &webapp); err != nil {
		t.Fatalf("parse the sample webapp: %v", err)
	}
	if !strings.Contains(webapp.Spec.Host, "${SHOULDERS_WORKLOAD_DOMAIN}") {
		t.Errorf("the sample webapp's host is %q; it must be qualified by the platform's workload domain",
			webapp.Spec.Host)
	}
}
