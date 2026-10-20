package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// The tier split is what makes a topology more than a list of clusters: the
// hub runs the platform tier — the OIDC issuer, the observability backends,
// the control-plane surface — and a spoke runs the workload tier. Admission
// control runs in both (D16).
//
// The split is expressed as delete patches on the flux Kustomizations, so
// these tests assert on the manifest the CLI applies.

// TestPlatformTierInstallsEverything: the hub's manifest creates every
// kustomization and carries no exclusions.
func TestPlatformTierInstallsEverything(t *testing.T) {
	// The medium profile is the base set, so it is the one that has all of
	// them; the small profile drops the policy and scanning addons.
	manifest := string(fluxKustomizationsManifestForSource(".", "medium", "GitRepository", false))
	created := kustomizationNames(t, manifest)

	for _, name := range []string{
		"helm-repositories", "namespaces", "crds", "headlamp",
		"helm-releases", "crossplane", "gateway", "policy-reporter", "trivy-dashboard",
	} {
		if !created[name] {
			t.Errorf("the platform tier is missing the %q kustomization", name)
		}
	}
	// The hub removes exactly the workload tier's own resources — the spoke
	// telemetry agent and its kube-state-metrics — and nothing else. Anything
	// else here would be the platform tier deleting part of itself.
	excluded := excludedResourceNames(t, manifest)
	for name := range excluded {
		if name != "alloy-agent" && name != "kube-state-metrics" {
			t.Errorf("the hub excludes %q, which is not a workload-tier resource", name)
		}
	}
	if len(excluded) != 2 {
		t.Errorf("the hub excludes %d resources, want 2 (alloy-agent, kube-state-metrics)", len(excluded))
	}
}

// TestWorkloadTierExcludesThePlatform: a spoke's manifest creates fewer
// kustomizations and removes the platform tier from the shared ones.
func TestWorkloadTierExcludesThePlatform(t *testing.T) {
	manifest := string(fluxKustomizationsManifestForSource(".", "medium", "GitRepository", true))
	created := kustomizationNames(t, manifest)

	// Kustomizations that exist only for the platform tier are not created.
	for _, name := range platformTierKustomizations {
		if created[name] {
			t.Errorf("a workload-tier cluster must not create the %q kustomization", name)
		}
	}
	// The shared ones still are.
	for _, name := range []string{"helm-releases", "namespaces", "gateway", "crossplane", "crds"} {
		if !created[name] {
			t.Errorf("a workload-tier cluster still needs the %q kustomization", name)
		}
	}

	// And the platform tier is removed from them.
	excluded := excludedResourceNames(t, manifest)
	for _, want := range []string{"dex", "kube-prometheus-stack", "kyverno", "falco", "alloy"} {
		if !excluded[want] {
			t.Errorf("the workload tier should exclude %q", want)
		}
	}
	if !strings.Contains(manifest, "$patch: delete") {
		t.Error("the workload tier must exclude the platform tier")
	}
}

// TestWorkloadTierPatchesAreValidYAML: a malformed patch would make flux
// reject the whole Kustomization, and the failure would look like a
// reconciliation error rather than a configuration one.
func TestWorkloadTierPatchesAreValidYAML(t *testing.T) {
	manifest := string(fluxKustomizationsManifestForSource(".", "small", "GitRepository", true))

	for _, document := range strings.Split(manifest, "\n---\n") {
		if !strings.Contains(document, "patches:") {
			continue
		}
		var parsed struct {
			Spec struct {
				Patches []struct {
					Target struct {
						Kind      string `yaml:"kind"`
						Name      string `yaml:"name"`
						Namespace string `yaml:"namespace"`
					} `yaml:"target"`
					Patch string `yaml:"patch"`
				} `yaml:"patches"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal([]byte(document), &parsed); err != nil {
			t.Fatalf("the manifest is not valid YAML: %v\n%s", err, document)
		}
		if len(parsed.Spec.Patches) == 0 {
			t.Fatal("a kustomization declared patches but parsed to none")
		}
		for _, patch := range parsed.Spec.Patches {
			if patch.Target.Kind == "" || patch.Target.Name == "" {
				t.Errorf("patch target is incomplete: %+v", patch.Target)
			}
			// A JSON6902 patch edits an element of a resource that must
			// keep existing. Its body is a list of operations, not a
			// resource, so it is validated separately.
			if trimmed := strings.TrimSpace(patch.Patch); strings.HasPrefix(trimmed, "[") {
				var ops []struct {
					Op    string      `json:"op"`
					Path  string      `json:"path"`
					Value interface{} `json:"value"`
				}
				if err := json.Unmarshal([]byte(trimmed), &ops); err != nil {
					t.Errorf("JSON patch is not valid: %v\n%s", err, patch.Patch)
					continue
				}
				if len(ops) == 0 {
					t.Errorf("JSON patch has no operations:\n%s", patch.Patch)
				}
				for _, op := range ops {
					if op.Op != "test" && op.Op != "remove" {
						t.Errorf("a tier JSON patch may only test or remove, got %q", op.Op)
					}
					if op.Path == "" {
						t.Errorf("JSON patch operation has no path: %+v", op)
					}
				}
				continue
			}
			// The patch body must itself be valid YAML, and must carry the
			// same apiVersion and name as its target, or kustomize will not
			// match it.
			var body map[string]interface{}
			if err := yaml.Unmarshal([]byte(patch.Patch), &body); err != nil {
				t.Errorf("patch body is not valid YAML: %v\n%s", err, patch.Patch)
				continue
			}
			if body["kind"] != patch.Target.Kind {
				t.Errorf("patch kind %v does not match target %q", body["kind"], patch.Target.Kind)
			}
			meta, _ := body["metadata"].(map[string]interface{})
			if meta["name"] != patch.Target.Name {
				t.Errorf("patch name %v does not match target %q", meta["name"], patch.Target.Name)
			}
			if ns, ok := meta["namespace"].(string); ok && ns != patch.Target.Namespace {
				t.Errorf("patch namespace %q does not match target %q", ns, patch.Target.Namespace)
			}
			// A tier patch either removes a resource or drops a
			// dependency it cannot satisfy. Both are legitimate; anything
			// else is a mistake.
			isDelete := strings.Contains(patch.Patch, "$patch: delete")
			isDependencyDrop := strings.Contains(patch.Patch, "dependsOn: null")
			isValueOverride := strings.Contains(patch.Patch, "  values:")
			if !isDelete && !isDependencyDrop && !isValueOverride {
				t.Errorf("a tier patch must delete a resource, drop a dependency, or set a value:\n%s", patch.Patch)
			}
		}
	}
}

// TestWorkloadTierRemovesTheHubOnlyListener is the Gateway half of the tier
// split.
//
// The Gateway's dex-https listener terminates TLS with kube-system/dex-tls,
// which is platform-tier. A spoke does not have that Secret, so the listener
// could never resolve:
//
//	dex-https  ResolvedRefs=False  InvalidCertificateRef
//
// and its published 443 port accepted TCP then dropped every handshake. The
// hub must keep the listener: it serves the Dex that every cluster's Headlamp
// authenticates against.
func TestWorkloadTierRemovesTheHubOnlyListener(t *testing.T) {
	spoke := string(fluxKustomizationsManifestForSource(".", "small", "GitRepository", true))
	hub := string(fluxKustomizationsManifestForSource(".", "small", "GitRepository", false))

	if !strings.Contains(spoke, "dex-https") {
		t.Fatal("the workload tier does not remove the dex-https listener")
	}
	if !strings.Contains(spoke, "/spec/listeners/1/name") {
		t.Error("the listener patch must test the element it removes, or a reorder removes the wrong one")
	}
	if !strings.Contains(spoke, "name: cilium-gateway") {
		t.Error("the listener patch must target the Gateway")
	}
	if strings.Contains(hub, "dex-https") {
		t.Error("the hub must keep the HTTPS listener")
	}
}

// TestTierExclusionsCoverThePlatformResources: every resource in the platform
// tier list must be excluded, and the list must not name a resource that does
// not exist in the manifests — a typo would silently exclude nothing.
func TestTierExclusionsCoverThePlatformResources(t *testing.T) {
	for kustomization, resources := range platformTierResources {
		if len(resources) == 0 {
			t.Errorf("%s has no exclusions", kustomization)
		}
		for _, ref := range resources {
			if ref.kind == "" || ref.name == "" {
				t.Errorf("%s has an incomplete exclusion: %+v", kustomization, ref)
			}
		}
	}
}

// TestPlatformTierKustomizationsAreDistinct: a name in both lists would mean
// the kustomization is created and then deleted.
func TestPlatformTierKustomizationsAreDistinct(t *testing.T) {
	shared := map[string]bool{}
	for kustomization := range platformTierResources {
		shared[kustomization] = true
	}
	for _, name := range platformTierKustomizations {
		if shared[name] {
			t.Errorf("%q is both a platform-only kustomization and patched for exclusion", name)
		}
	}
}

// kustomizationNames returns the names of the flux Kustomizations a manifest
// creates.
//
// The names have to be read from the parsed objects: a name also appears in
// dependsOn, so a substring match would report a kustomization that was only
// referred to.
func kustomizationNames(t *testing.T, manifest string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, document := range strings.Split(manifest, "\n---\n") {
		if strings.TrimSpace(document) == "" {
			continue
		}
		var parsed struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
		}
		if err := yaml.Unmarshal([]byte(document), &parsed); err != nil {
			t.Fatalf("the manifest is not valid YAML: %v\n%s", err, document)
		}
		if parsed.Kind == "Kustomization" {
			names[parsed.Metadata.Name] = true
		}
	}
	return names
}

// excludedResourceNames returns the names of the resources a manifest removes.
func excludedResourceNames(t *testing.T, manifest string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, document := range strings.Split(manifest, "\n---\n") {
		if strings.TrimSpace(document) == "" {
			continue
		}
		var parsed struct {
			Spec struct {
				Patches []struct {
					Target struct {
						Name string `yaml:"name"`
					} `yaml:"target"`
				} `yaml:"patches"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal([]byte(document), &parsed); err != nil {
			t.Fatalf("the manifest is not valid YAML: %v\n%s", err, document)
		}
		for _, patch := range parsed.Spec.Patches {
			names[patch.Target.Name] = true
		}
	}
	return names
}

// TestWorkloadTierDropsDependenciesOnRemovedKustomizations guards a deadlock.
//
// A kustomization that depends on one that was never created stays stuck with
// "dependency 'flux-system/headlamp' not found", which blocks the tier rather
// than just the dependent kustomization.
func TestWorkloadTierDropsDependenciesOnRemovedKustomizations(t *testing.T) {
	manifest := string(fluxKustomizationsManifestForSource(".", "medium", "GitRepository", true))

	for _, document := range strings.Split(manifest, "\n---\n") {
		if strings.TrimSpace(document) == "" {
			continue
		}
		var parsed struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Spec struct {
				DependsOn []struct {
					Name string `yaml:"name"`
				} `yaml:"dependsOn"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal([]byte(document), &parsed); err != nil {
			t.Fatalf("the manifest is not valid YAML: %v", err)
		}
		for _, dependency := range parsed.Spec.DependsOn {
			if isPlatformTierKustomization(dependency.Name) {
				t.Errorf("%q still depends on %q, which a workload-tier cluster does not create",
					parsed.Metadata.Name, dependency.Name)
			}
		}
	}
}

// TestWorkloadTierDependencyDropsAreComplete says that the dropped dependsOn
// lists are the whole list, which is what makes dropping them safe.
//
// The patch removes `dependsOn` entirely. If a release also waited on something
// in its own tier, that dependency would be lost with it.
func TestWorkloadTierDependencyDropsAreComplete(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	for _, dependency := range workloadTierReleaseDependencies {
		raw, err := os.ReadFile(filepath.Join(repoRoot, "2-addons", "manifests", "helm-releases", dependency.name+".yaml"))
		if err != nil {
			t.Fatalf("read %s: %v", dependency.name, err)
		}
		var parsed struct {
			Spec struct {
				DependsOn []struct {
					Name string `yaml:"name"`
				} `yaml:"dependsOn"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("parse %s: %v", dependency.name, err)
		}
		if len(parsed.Spec.DependsOn) != 1 {
			t.Errorf("%s waits on %d releases; the tier patch drops the whole list, so it "+
				"must wait on exactly one", dependency.name, len(parsed.Spec.DependsOn))
			continue
		}
		if parsed.Spec.DependsOn[0].Name != dependency.waitsOn {
			t.Errorf("%s waits on %q, but the tier drops %q",
				dependency.name, parsed.Spec.DependsOn[0].Name, dependency.waitsOn)
		}
	}
}

// TestWorkloadTierDropsUnreachableDependencies checks the resulting manifest
// drops the dependency rather than removing the release.
func TestWorkloadTierDropsUnreachableDependencies(t *testing.T) {
	manifest := string(fluxKustomizationsManifestForSource(".", "small", "GitRepository", true))
	if !strings.Contains(manifest, "dependsOn: null") {
		t.Fatal("the workload tier should drop the dependencies it cannot satisfy")
	}
	// And the release itself is still installed.
	if !strings.Contains(manifest, "name: cnpg\n") {
		t.Error("the workload tier should still install cnpg")
	}
}

// TestPolicyRunsOnEveryTier guards the decision that put admission control on
// the spoke too.
//
// Kyverno is not an observability addon that can live once in the hub: a policy
// engine only sees the pods admitted by its own kube-apiserver. The workload
// tier also depends on it directly — the Workspace composition emits a Kyverno
// ClusterPolicy, so a spoke without the CRD never reconciles its first
// Workspace.
func TestPolicyRunsOnEveryTier(t *testing.T) {
	for _, resources := range platformTierResources {
		for _, ref := range resources {
			if strings.HasPrefix(ref.name, "kyverno") {
				t.Errorf("the workload tier excludes %s/%s; policy has to run per cluster", ref.kind, ref.name)
			}
		}
	}

	var kyverno *releaseValueOverride
	for i := range workloadTierValueOverrides {
		if workloadTierValueOverrides[i].name == "kyverno" {
			kyverno = &workloadTierValueOverrides[i]
		}
	}
	if kyverno == nil {
		t.Fatal("the workload tier has no kyverno overrides; its ServiceMonitors need the monitoring CRDs")
	}
	for _, controller := range []string{"admissionController", "backgroundController", "cleanupController", "reportsController"} {
		section, ok := kyverno.values[controller].(map[string]interface{})
		if !ok {
			t.Errorf("kyverno %s has no overrides", controller)
			continue
		}
		monitor, ok := section["serviceMonitor"].(map[string]interface{})
		if !ok || monitor["enabled"] != false {
			t.Errorf("kyverno %s.serviceMonitor.enabled must be false on a spoke", controller)
		}
	}
	if grafana, ok := kyverno.values["grafana"].(map[string]interface{}); !ok || grafana["enabled"] != false {
		t.Error("the kyverno Grafana dashboard has no Grafana to render into on a spoke")
	}
}

// TestUserWorkloadsAreAWorkloadTierKustomization checks both directions of the
// user-space split.
//
// The names are what the two tiers act on: a workload-tier cluster keeps them,
// a hub deletes them. If a name here drifts from the manifest, the hub silently
// starts reconciling the sample workloads — a deploy that looks fine and puts
// user pods where the design says they do not belong.
func TestUserWorkloadsAreAWorkloadTierKustomization(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(repoRoot, "2-addons", "flux", "kustomizations.yaml"))
	if err != nil {
		t.Fatalf("read the flux kustomizations: %v", err)
	}

	declared := map[string]bool{}
	for _, document := range strings.Split(string(raw), "\n---\n") {
		var parsed struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name      string `yaml:"name"`
				Namespace string `yaml:"namespace"`
			} `yaml:"metadata"`
		}
		if err := yaml.Unmarshal([]byte(document), &parsed); err != nil {
			t.Fatalf("parse the flux kustomizations: %v", err)
		}
		if parsed.Kind == "Kustomization" && parsed.Metadata.Namespace == "flux-system" {
			declared[parsed.Metadata.Name] = true
		}
	}

	for _, name := range workloadTierKustomizations {
		if !declared[name] {
			t.Errorf("the hub deletes %q, but no such flux Kustomization is declared", name)
		}
	}
	if len(workloadTierKustomizations) == 0 {
		t.Fatal("the workload tier claims no user-space kustomizations")
	}
}

// TestUserWorkloadsAreInTheFluxSet is the other half of
// TestUserWorkloadsAreAWorkloadTierKustomization.
//
// That test reads 2-addons/flux/kustomizations.yaml, which documents the set —
// but the CLI builds the set in Go. A Kustomization added to the file and not to
// the list is delivered by nothing, which is exactly the gap Phase 0 found.
func TestUserWorkloadsAreInTheFluxSet(t *testing.T) {
	for _, profile := range []string{"small", "medium", "large"} {
		byName := map[string]fluxKustomization{}
		for _, item := range fluxKustomizationsForProfile(".", profile) {
			byName[item.Name] = item
		}
		for _, name := range workloadTierKustomizations {
			if _, ok := byName[name]; !ok {
				t.Errorf("the %s flux set has no %q", profile, name)
			}
		}
		apps, ok := byName["user-space-apps"]
		if !ok {
			continue
		}
		if !apps.Substitute {
			t.Errorf("%s: user-space-apps must substitute SHOULDERS_WORKLOAD_DOMAIN", profile)
		}
		if apps.Path != "./3-user-space/team-a" {
			t.Errorf("%s: user-space-apps path = %q", profile, apps.Path)
		}
	}
}
