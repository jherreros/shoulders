package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"sigs.k8s.io/yaml"
)

// The two tiers of a topology.
//
// The hub runs the platform tier: the OIDC issuer, the observability
// backends, and the platform's control-plane surface — the things that are
// meaningful once, for the whole topology, and that must not be duplicated. A
// spoke runs the workload tier: the workload compositions and the stateful
// services they depend on, all of which stay local to the spoke. Admission
// control is neither: it runs in both tiers (D16), because a policy engine only
// sees the API server it is attached to.
//
// The split is expressed as delete patches on the flux Kustomizations rather
// than as a different manifest path, for two reasons:
//
//   - A path would have to be per cluster, and therefore tied to the cluster
//     names the operator chose. The names are the operator's to pick.
//   - The patches apply to whatever the path renders, so they compose with the
//     profile overlay (2-addons/profiles/<profile>) without either having to
//     know about the other.
//
// This is the same mechanism the CLI already used to keep flux from taking
// over the Cilium release, and the same one the profile overlays use.

// platformTierResources are the resources that make up the platform tier,
// grouped by the flux Kustomization that reconciles them.
//
// Excluding a resource means the spoke never installs it, and — because the
// namespaces are excluded too — never creates a namespace for it.
var platformTierResources = map[string][]resourceRef{
	"helm-releases": {
		{kind: "HelmRelease", name: "dex", namespace: "dex"},
		{kind: "HelmRelease", name: "kube-prometheus-stack", namespace: "observability"},
		{kind: "HelmRelease", name: "loki", namespace: "observability"},
		{kind: "HelmRelease", name: "tempo", namespace: "observability"},
		{kind: "HelmRelease", name: "alloy", namespace: "observability"},
		// Kyverno is NOT excluded. Admission control is per cluster, so a
		// policy engine on the hub does nothing for a pod admitted on a spoke —
		// and the workload tier depends on it directly: the Workspace
		// composition creates a Kyverno ClusterPolicy, so without Kyverno the
		// first Workspace never becomes Ready.
		//
		// What stays central is the policy *source*: both tiers render the same
		// kyverno-policies chart from the same git path.
		{kind: "HelmRelease", name: "policy-reporter", namespace: "policy-reporter"},
		{kind: "HelmRelease", name: "trivy-operator", namespace: "trivy-system"},
		{kind: "HelmRelease", name: "trivy-operator-polr-adapter", namespace: "trivy-system"},
		{kind: "HelmRelease", name: "falco", namespace: "falco"},
		// Dex's serving certificate. It is issued for the hub's dex host and
		// is only used by the dex HelmRelease, which the spoke does not run.
		// It also lives in the dex namespace, which the spoke does not create.
		{kind: "Secret", name: "dex-tls", namespace: "dex"},
	},
	"namespaces": {
		{kind: "Namespace", name: "dex"},
		{kind: "Namespace", name: "trivy-system"},
		{kind: "Namespace", name: "falco"},
		{kind: "Namespace", name: "policy-reporter"},
	},
	"gateway": {
		{kind: "HTTPRoute", name: "dex", namespace: "dex"},
		{kind: "HTTPRoute", name: "grafana", namespace: "observability"},
		{kind: "HTTPRoute", name: "prometheus", namespace: "observability"},
		{kind: "HTTPRoute", name: "alertmanager", namespace: "observability"},
		{kind: "HTTPRoute", name: "reporter", namespace: "observability"},
		// The policy reporter route, in the reporter file.
		{kind: "HTTPRoute", name: "policy-reporter", namespace: "policy-reporter"},
		{kind: "HTTPRoute", name: "hubble", namespace: "observability"},
		// The hubble UI route lives in kube-system, so it would install on a
		// spoke even though the spoke has no hubble to serve.
		{kind: "HTTPRoute", name: "hubble-ui", namespace: "kube-system"},
		// Dex's serving certificate. The spoke does not run Dex, and the
		// certificate is issued for the hub's dex host.
		{kind: "Secret", name: "dex-tls", namespace: "kube-system"},
	},
}

// platformTierKustomizations are the flux Kustomizations that exist only in the
// platform tier. A workload-tier cluster does not create them at all, rather
// than creating them empty.
var platformTierKustomizations = []string{
	"policy-reporter",
	"trivy-dashboard",
}

// workloadTierReleaseDependencies are the HelmReleases the workload tier runs
// that wait on a platform-tier HelmRelease.
//
// The dependency is an ordering nicety: the platform tier's prometheus stack
// brings the monitoring.coreos.com CRDs, and a release that creates a
// ServiceMonitor should wait for them. On a spoke that stack does not exist, so
// the dependency can never be satisfied and the release never installs at all:
//
//	HelmRelease/cnpg: Fulfilling prerequisites
//	unable to get 'observability/kube-prometheus-stack' dependency:
//	  helmreleases.helm.toolkit.fluxcd.io "kube-prometheus-stack" not found
//
// The dependency is therefore dropped, and the whole dependsOn list goes with
// it — which is correct only because each of these releases waits on nothing
// else. The test below says so.
var workloadTierReleaseDependencies = []releaseDependency{
	{kind: "HelmRelease", name: "cnpg", namespace: "cnpg-system", waitsOn: "kube-prometheus-stack"},
	// Headlamp waits on dex for the issuer it authenticates against. On a spoke
	// the issuer is the *hub's* dex, over the mesh, so waiting for a local dex
	// is not merely unnecessary — it can never be satisfied, and the Headlamp
	// never installs:
	//
	//   HelmRelease/headlamp: Fulfilling prerequisites
	//   unable to get 'dex/dex' dependency
	{kind: "HelmRelease", name: "headlamp", namespace: "headlamp", waitsOn: "dex"},
	// Kyverno now runs in both tiers, and it waits on the prometheus stack for
	// the same reason CNPG does: its ServiceMonitors need the CRDs. The spoke
	// has no prometheus stack, and the ServiceMonitors are turned off below.
	{kind: "HelmRelease", name: "kyverno", namespace: "kyverno", waitsOn: "kube-prometheus-stack"},
}

// workloadTierValueOverrides are the HelmRelease values the workload tier
// needs to be different.
//
// CNPG's chart creates a PodMonitor by default, and the PodMonitor is a scrape
// configuration: it tells a Prometheus what to collect. A spoke has no
// Prometheus — the observability backends are in the hub — so the object has
// nothing to be scraped by, and creating it fails outright because the
// monitoring.coreos.com CRDs are not installed on a spoke:
//
//	no matches for kind "PodMonitor" in version "monitoring.coreos.com/v1"
//	ensure CRDs are installed first
//
// The override is applied through the same Flux Kustomization patch that
// removes the platform tier, so it is part of the object the CLI applies rather
// than something patched on afterwards.
var workloadTierValueOverrides = []releaseValueOverride{
	{
		kind:      "HelmRelease",
		name:      "cnpg",
		namespace: "cnpg-system",
		values: map[string]interface{}{
			"monitoring": map[string]interface{}{
				"podMonitorEnabled": false,
			},
		},
	},
	{
		// Kyverno's chart creates a ServiceMonitor per controller, plus a
		// Grafana dashboard. A spoke has neither Prometheus nor Grafana, so the
		// ServiceMonitors cannot be created at all (no monitoring.coreos.com
		// CRDs) and the dashboard would be a ConfigMap nobody reads.
		//
		// This is the third release to hit the same shape — after CNPG and
		// Headlamp — which is why the tier exists as a list of differences
		// rather than as a second set of manifests.
		kind:      "HelmRelease",
		name:      "kyverno",
		namespace: "kyverno",
		values: map[string]interface{}{
			"admissionController":  map[string]interface{}{"serviceMonitor": map[string]interface{}{"enabled": false}},
			"backgroundController": map[string]interface{}{"serviceMonitor": map[string]interface{}{"enabled": false}},
			"cleanupController":    map[string]interface{}{"serviceMonitor": map[string]interface{}{"enabled": false}},
			"reportsController":    map[string]interface{}{"serviceMonitor": map[string]interface{}{"enabled": false}},
			"grafana":              map[string]interface{}{"enabled": false},
		},
	},
}

// releaseValueOverride is a HelmRelease whose values the tier changes.
type releaseValueOverride struct {
	kind      string
	name      string
	namespace string
	values    map[string]interface{}
}

// releaseDependency is a HelmRelease whose dependsOn has to be removed.
type releaseDependency struct {
	kind      string
	name      string
	namespace string
	// waitsOn is the platform-tier release it depends on. It is the whole of
	// its dependsOn list, which the patch relies on.
	waitsOn string
}

// resourceRef identifies a resource a delete patch targets.
type resourceRef struct {
	kind      string
	name      string
	namespace string
}

// workloadTierResources are the resources that exist only in the workload tier.
//
// The tier split is symmetric: the spoke removes the platform tier, and the hub
// removes these. A spoke's telemetry agent and its kube-state-metrics have no
// meaning on the hub, which already runs a full monitoring stack that would
// duplicate them.
var workloadTierResources = map[string][]resourceRef{
	"helm-releases": {
		{kind: "HelmRelease", name: "alloy-agent", namespace: "observability"},
		{kind: "HelmRelease", name: "kube-state-metrics", namespace: "observability"},
	},
}

// workloadTierKustomizations are the flux Kustomizations that exist only in the
// workload tier.
//
// User workloads are what a spoke is for. On a hub they would be the one thing
// the tier split keeps apart — the hub runs the platform, the spokes run the
// workloads — so the hub removes them rather than installing a sample app it
// has no reason to run.
var workloadTierKustomizations = []string{
	"user-space-workspaces",
	"user-space-apps",
}

// jsonPatch is a JSON6902 edit of a resource that must keep existing.
type jsonPatch struct {
	target resourceRef
	ops    []map[string]interface{}
}

// workloadTierJSONPatches edit shared resources for the workload tier, where a
// whole-resource delete is too coarse.
//
// The Gateway is the case that forced this. Its `dex-https` listener serves the
// hub's Dex on 443 with `certificateRefs: kube-system/dex-tls`, and that Secret
// is platform-tier. A listener is not a resource, so it cannot be an entry in
// platformTierResources, and leaving it on a spoke produced a listener that can
// never resolve:
//
//	dex-https  ResolvedRefs=False  InvalidCertificateRef:
//	           Invalid CertificateRef, Secret "dex-tls" not found
//
// plus a published 443 port that accepts a TCP connection and then drops every
// TLS handshake. A spoke serves its platform UIs over HTTP and has no Dex, so
// the listener has no meaning there.
//
// Each patch tests the element it removes. If the listener list is ever
// reordered, the `test` op fails the build loudly instead of removing whatever
// now sits at index 1 — which would be the HTTP listener.
var workloadTierJSONPatches = map[string][]jsonPatch{
	"gateway": {
		{
			target: resourceRef{kind: "Gateway", name: "cilium-gateway", namespace: "kube-system"},
			ops: []map[string]interface{}{
				{"op": "test", "path": "/spec/listeners/1/name", "value": "dex-https"},
				{"op": "remove", "path": "/spec/listeners/1"},
			},
		},
	},
}

// tierJSONPatches renders JSON6902 patches for a flux Kustomization.
func tierJSONPatches(patches []jsonPatch) []interface{} {
	rendered := make([]interface{}, 0, len(patches))
	for _, item := range patches {
		ops, err := json.Marshal(item.ops)
		if err != nil {
			continue
		}
		rendered = append(rendered, map[string]interface{}{
			"target": map[string]interface{}{
				"kind":      item.target.kind,
				"name":      item.target.name,
				"namespace": item.target.namespace,
			},
			"patch": string(ops),
		})
	}
	return rendered
}

// RemovePlatformTierKustomizations deletes the flux Kustomizations that exist
// only for the platform tier.
//
// The shared kustomizations need nothing: their tier exclusions travel in the
// manifest the CLI applies, and flux prunes what the patched build no longer
// contains. These are different — they are whole Kustomization objects the CLI
// created, and nothing else would remove them if a cluster changed tier.
//
// It is safe to call for a cluster that never had them.
func RemovePlatformTierKustomizations(kubeconfigPath string) error {
	return removeKustomizations(kubeconfigPath, platformTierKustomizations)
}

// RemoveWorkloadTierKustomizations deletes the flux Kustomizations that exist
// only for the workload tier, so a hub does not reconcile user workloads.
//
// It is safe to call for a cluster that never had them.
func RemoveWorkloadTierKustomizations(kubeconfigPath string) error {
	return removeKustomizations(kubeconfigPath, workloadTierKustomizations)
}

func removeKustomizations(kubeconfigPath string, names []string) error {
	client, err := kube.NewDynamicClient(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}
	gvr := schema.GroupVersionResource{
		Group:    "kustomize.toolkit.fluxcd.io",
		Version:  "v1",
		Resource: "kustomizations",
	}
	ctx := context.Background()
	for _, name := range names {
		err := client.Resource(gvr).Namespace("flux-system").Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete %s kustomization: %w", name, err)
		}
	}
	return nil
}

// tierDeletePatches renders the delete patches that remove a set of resources
// from a flux Kustomization's build.
func tierDeletePatches(resources []resourceRef) []interface{} {
	patches := make([]interface{}, 0, len(resources))
	for _, ref := range resources {
		patches = append(patches, map[string]interface{}{
			"target": map[string]interface{}{
				"kind":      ref.kind,
				"name":      ref.name,
				"namespace": ref.namespace,
			},
			"patch": deletePatchFor(ref),
		})
	}
	return patches
}

// tierValuePatches renders the patches that change a release's values for the
// tier.
func tierValuePatches(overrides []releaseValueOverride) []interface{} {
	patches := make([]interface{}, 0, len(overrides))
	for _, override := range overrides {
		values, err := yaml.Marshal(override.values)
		if err != nil {
			continue
		}
		patches = append(patches, map[string]interface{}{
			"target": map[string]interface{}{
				"kind":      override.kind,
				"name":      override.name,
				"namespace": override.namespace,
			},
			"patch": "apiVersion: " + apiVersionFor(override.kind) + "\n" +
				"kind: " + override.kind + "\n" +
				"metadata:\n" +
				"  name: " + override.name + "\n" +
				"  namespace: " + override.namespace + "\n" +
				"spec:\n" +
				"  values:\n" +
				indentYAML(values, "    "),
		})
	}
	return patches
}

// indentYAML indents every line of a YAML document.
func indentYAML(body []byte, prefix string) string {
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n") + "\n"
}

// tierDependencyPatches renders the patches that drop a dependsOn pointing at a
// release the tier does not have.
//
// The whole list is dropped, so this is only correct when the release waits on
// nothing else. TestWorkloadTierDependencyDropsAreComplete says so.
func tierDependencyPatches(dependencies []releaseDependency) []interface{} {
	patches := make([]interface{}, 0, len(dependencies))
	for _, dependency := range dependencies {
		patches = append(patches, map[string]interface{}{
			"target": map[string]interface{}{
				"kind":      dependency.kind,
				"name":      dependency.name,
				"namespace": dependency.namespace,
			},
			"patch": "apiVersion: " + apiVersionFor(dependency.kind) + "\n" +
				"kind: " + dependency.kind + "\n" +
				"metadata:\n" +
				"  name: " + dependency.name + "\n" +
				"  namespace: " + dependency.namespace + "\n" +
				"spec:\n" +
				"  dependsOn: null\n",
		})
	}
	return patches
}

// deletePatchFor renders the kustomize patch that removes a resource.
func deletePatchFor(ref resourceRef) string {
	patch := "apiVersion: " + apiVersionFor(ref.kind) + "\n" +
		"kind: " + ref.kind + "\n" +
		"metadata:\n" +
		"  name: " + ref.name + "\n"
	if ref.namespace != "" {
		patch += "  namespace: " + ref.namespace + "\n"
	}
	return patch + "$patch: delete\n"
}

// apiVersionFor maps a kind to the apiVersion a delete patch needs.
//
// A delete patch must carry the same apiVersion as the resource it removes, or
// kustomize does not match it.
func apiVersionFor(kind string) string {
	switch kind {
	case "Namespace", "Secret":
		return "v1"
	default:
		return "helm.toolkit.fluxcd.io/v2"
	}
}
