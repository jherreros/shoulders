package bootstrap

import (
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/yaml"
)

// Cilium's observability is expressed in git, not in the CLI.
//
// The reason is the merge order: flux applies the manifest's inline values over
// the values the CLI publishes through valuesFrom, so a value the CLI computed
// would be silently overridden by the manifest. The CLI used to set
// `dashboards.enabled` and `hubble.{relay,ui}.enabled` from the profile, and
// the base manifest set them to true — so a `small` cluster, whose profile
// omits the hubble UI and the Cilium dashboards, got them anyway.
//
// The profile now decides, in 2-addons/profiles/<profile>/helm-releases/. These
// tests pin both halves of that arrangement.

// TestCliDoesNotSetProfileDerivedCiliumValues guards the CLI half.
func TestCliDoesNotSetProfileDerivedCiliumValues(t *testing.T) {
	values := ciliumValuesForTest()

	for _, key := range []string{"dashboards", "hubble"} {
		if _, ok := values[key]; ok {
			t.Errorf("the CLI sets %q; it is owned by the profile patches in git, "+
				"and flux merges the manifest's inline values over the CLI's", key)
		}
	}
}

// TestProfileManifestsDecideCiliumObservability guards the git half: the
// rendered per-profile manifests must express the profile's intent.
func TestProfileManifestsDecideCiliumObservability(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	cases := []struct {
		profile string
		path    string
		enabled bool
	}{
		// The base manifests are the medium profile.
		{"medium", "2-addons/manifests/helm-releases", true},
		{"small", "2-addons/profiles/small/helm-releases", false},
		{"large", "2-addons/profiles/large/helm-releases", true},
	}

	for _, tc := range cases {
		t.Run(tc.profile, func(t *testing.T) {
			values := renderCiliumHelmReleaseValues(t, repoRoot, tc.path)

			dashboards, ok := values["dashboards"].(map[string]interface{})
			if !ok {
				t.Fatal("the cilium HelmRelease has no dashboards values")
			}
			if got := dashboards["enabled"]; got != tc.enabled {
				t.Errorf("dashboards.enabled = %v, want %v", got, tc.enabled)
			}

			hubble, ok := values["hubble"].(map[string]interface{})
			if !ok {
				t.Fatal("the cilium HelmRelease has no hubble values")
			}
			for _, section := range []string{"relay", "ui"} {
				inner, ok := hubble[section].(map[string]interface{})
				if !ok {
					t.Errorf("hubble.%s is missing", section)
					continue
				}
				if got := inner["enabled"]; got != tc.enabled {
					t.Errorf("hubble.%s.enabled = %v, want %v", section, got, tc.enabled)
				}
			}
		})
	}
}

// renderCiliumHelmReleaseValues builds a manifests path and returns the values
// of the Cilium HelmRelease in it.
func renderCiliumHelmReleaseValues(t *testing.T, repoRoot, path string) map[string]interface{} {
	t.Helper()
	fs := filesys.MakeFsOnDisk()
	k := krusty.MakeKustomizer(krusty.MakeDefaultOptions())
	res, err := k.Run(fs, filepath.Join(repoRoot, path))
	if err != nil {
		t.Fatalf("build %s: %v", path, err)
	}
	for _, r := range res.Resources() {
		if r.GetKind() != "HelmRelease" || r.GetName() != "cilium" {
			continue
		}
		body, err := r.AsYAML()
		if err != nil {
			t.Fatalf("render cilium HelmRelease: %v", err)
		}
		var parsed struct {
			Spec struct {
				Values map[string]interface{} `json:"values"`
			} `json:"spec"`
		}
		if err := yaml.Unmarshal([]byte(body), &parsed); err != nil {
			t.Fatalf("parse cilium HelmRelease: %v", err)
		}
		return parsed.Spec.Values
	}
	t.Fatalf("no cilium HelmRelease in %s", path)
	return nil
}

// TestCliAndGitOpsAgreeOnStaticCiliumValues is the invariant that lets the CLI
// bootstrap Cilium with a complete value set while flux later adopts the
// release with the manifest's.
//
// The CLI needs the static values at bootstrap: with `ipam.mode` left at the
// chart default of `cluster-pool`, the agents would allocate pod addresses
// from a cluster-wide pool instead of the node's podCIDR, which is wrong for
// vCluster. So the two sets overlap by necessity.
//
// The overlap is safe only because the two agree. flux merges the manifest's
// inline values over the published ones, so a disagreement is not a merge
// conflict — it is a silent reconfiguration of a running CNI.
func TestCliAndGitOpsAgreeOnStaticCiliumValues(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	gitValues := renderCiliumHelmReleaseValues(t, repoRoot, "2-addons/manifests/helm-releases")
	cliValues := ciliumValuesForTest()

	// The static keys both sides set. The profile-derived ones (dashboards,
	// hubble) are git-only, and the runtime ones are CLI-only.
	for _, key := range []string{"image", "ipam", "gatewayAPI", "envoy", "prometheus"} {
		cliValue, cliHas := cliValues[key]
		gitValue, gitHas := gitValues[key]
		if !cliHas || !gitHas {
			t.Errorf("key %q: cli=%v git=%v; both sides must set the static keys", key, cliHas, gitHas)
			continue
		}
		if !equalValues(t, cliValue, gitValue) {
			t.Errorf("key %q: the CLI installs %v but the manifest says %v; "+
				"flux would reconfigure the running CNI to the manifest's value", key, cliValue, gitValue)
		}
	}

	// The profile-derived keys must be git-only. The CLI setting them would
	// mean the manifest wins anyway (inline values are merged last), so the
	// CLI's value would be a lie.
	for _, key := range []string{"dashboards", "hubble"} {
		if _, ok := cliValues[key]; ok {
			t.Errorf("the CLI sets %q, but the profile owns it in git", key)
		}
		if _, ok := gitValues[key]; !ok {
			t.Errorf("the manifest does not set %q; the profile has to express it somewhere", key)
		}
	}
}

// equalValues compares two value trees structurally.
func equalValues(t *testing.T, a, b interface{}) bool {
	t.Helper()
	left, err := yaml.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	right, err := yaml.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(left) == string(right)
}

// ciliumValuesForTest returns the values the CLI would install Cilium with,
// without touching a cluster.
func ciliumValuesForTest() map[string]interface{} {
	// The real builder, not a copy: a copy is a second source of truth, which
	// is exactly the failure mode this test exists to catch.
	return ciliumValues("10.96.0.1", 8443, CiliumOptionsForProfile("small"))
}

var _ = strings.TrimSpace
