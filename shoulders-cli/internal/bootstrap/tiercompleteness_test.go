package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// TestTierExclusionsCoverEveryPlatformManifest checks that the tier exclusion
// list is complete.
//
// The exclusion lists are hand-maintained, and the failure mode of an entry
// that is missing is subtle: the spoke reconciles a resource whose namespace
// it does not create, and flux reports
//
//	Secret/dex/dex-tls not found: namespaces "dex" not found
//
// which looks like a manifest bug rather than a missing exclusion. This test
// reads the manifests themselves, so an addon added to the platform tier
// without an exclusion fails here rather than in a cluster.
//
// The workload tier is what the spoke keeps. The list below is the set of
// resources a spoke legitimately runs; anything in the shared manifests that is
// not in it must be excluded.
func TestTierExclusionsCoverEveryPlatformManifest(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	// What a spoke legitimately runs, keyed by "kind/name".
	kept := map[string]bool{
		"HelmRelease/cnpg":          true,
		"HelmRelease/crossplane":    true,
		"HelmRelease/garage":        true,
		"HelmRelease/cilium":        true,
		"HelmRelease/strimzi":       true,
		"Secret/garage-admin-token": true,
		"Service/garage-admin":      true,
		"Job/garage-layout-init":    true,
		// The spoke keeps its own gateway for workload ingress (D7), and its
		// own Headlamp.
		"Gateway/cilium-gateway": true,
		"HelmRelease/headlamp":   true,
		"HTTPRoute/headlamp":     true,
		// Policy is per cluster. The Workspace composition creates a Kyverno
		// ClusterPolicy, so a spoke without Kyverno never reconciles its first
		// Workspace.
		"HelmRelease/kyverno":          true,
		"HelmRelease/kyverno-policies": true,
		// The spoke's own telemetry path: its object state, pushed to the hub.
		"HelmRelease/kube-state-metrics": true,
		"HelmRelease/alloy-agent":        true,
	}

	excluded := map[string]bool{}
	for _, resources := range platformTierResources {
		for _, ref := range resources {
			excluded[ref.kind+"/"+ref.name] = true
		}
	}

	// Every manifest in the helm-releases and gateway directories must be
	// either kept or excluded.
	for _, dir := range []string{"helm-releases", "gateway"} {
		base := filepath.Join(repoRoot, "2-addons", "manifests", dir)
		entries, err := os.ReadDir(base)
		if err != nil {
			t.Fatalf("read %s: %v", base, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
				continue
			}
			if entry.Name() == "kustomization.yaml" {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(base, entry.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", entry.Name(), err)
			}
			// A file can hold several resources.
			for _, document := range strings.Split(string(raw), "\n---\n") {
				if strings.TrimSpace(document) == "" {
					continue
				}
				var parsed struct {
					Kind     string `yaml:"kind"`
					Metadata struct {
						Name      string `yaml:"name"`
						Namespace string `yaml:"namespace"`
					} `yaml:"metadata"`
				}
				if err := yaml.Unmarshal([]byte(document), &parsed); err != nil {
					t.Fatalf("parse %s: %v", entry.Name(), err)
				}
				if parsed.Kind == "" {
					continue
				}
				key := parsed.Kind + "/" + parsed.Metadata.Name
				if kept[key] || excluded[key] {
					continue
				}
				t.Errorf("%s/%s is neither kept by the workload tier nor excluded from it; "+
					"a spoke would try to install it (or fail on its missing namespace)",
					dir, key)
			}
		}
	}
}
