package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
)

// SetCiliumHelmReleaseFluxOwnership decides whether Flux reconciles the
// Cilium HelmRelease.
//
// The two cases are opposites and both are real:
//
//   - Cilium enabled: the CLI bootstraps it (Flux cannot reconcile anything
//     until a CNI is up) and Flux then *adopts* it. That is the endorsed
//     bootstrap-then-hand-off pattern, and it requires the HelmRelease to be
//     present in Flux's set. The per-cluster values the CLI discovers — the API
//     server endpoint and, for a mesh, the cluster identity and peer list —
//     travel with it in the `shoulders-cilium-cluster` ConfigMap, which the
//     manifest references with valuesFrom.
//
//   - Cilium disabled (the default for `provider: existing`): the cluster
//     already has its own CNI, and `2-addons/manifests/helm-releases/cilium.yaml`
//     is unconditional, so without an exclusion Flux would install Cilium
//     anyway.
//
// This replaced an unconditional exclusion, which existed as a kind-to-vind
// migration workaround (commit c3fc83b) and outlived its reason. It made
// GitOps the *nominal* owner of the CNI while never actually reconciling it,
// so Renovate's Cilium version bumps never reached a running cluster.
//
// The patch is idempotent in both directions, so re-running `up` after
// toggling the setting converges rather than accumulating patches.
func SetCiliumHelmReleaseFluxOwnership(kubeconfigPath string, fluxOwns bool) error {
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
	kustomization, err := client.Resource(gvr).Namespace("flux-system").Get(ctx, "helm-releases", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get helm-releases kustomization: %w", err)
	}

	patches, _ := nestedSlice(kustomization.Object, "spec", "patches")
	kept := make([]interface{}, 0, len(patches)+1)
	for _, existing := range patches {
		patchMap, ok := existing.(map[string]interface{})
		if ok && isCiliumHelmReleasePatch(patchMap) {
			// Drop any previous exclusion; it is re-added below only if
			// Flux should not own Cilium.
			continue
		}
		kept = append(kept, existing)
	}
	if !fluxOwns {
		kept = append(kept, map[string]interface{}{
			"target": map[string]interface{}{
				"kind":      "HelmRelease",
				"name":      "cilium",
				"namespace": "kube-system",
			},
			"patch": ciliumHelmReleaseDeletePatch(),
		})
	}

	patchBytes, err := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{"patches": kept},
	})
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}
	if _, err := client.Resource(gvr).Namespace("flux-system").Patch(
		ctx, "helm-releases", types.MergePatchType, patchBytes, metav1.PatchOptions{},
	); err != nil {
		return fmt.Errorf("patch helm-releases kustomization: %w", err)
	}
	return nil
}

// ciliumHelmReleaseDeletePatch removes the Cilium HelmRelease from the
// Kustomization's build.
func ciliumHelmReleaseDeletePatch() string {
	return "apiVersion: helm.toolkit.fluxcd.io/v2\n" +
		"kind: HelmRelease\n" +
		"metadata:\n" +
		"  name: cilium\n" +
		"  namespace: kube-system\n" +
		"$patch: delete\n"
}

// isCiliumHelmReleasePatch reports whether a Flux Kustomization patch targets
// the Cilium HelmRelease, so a stale one can be recognised and replaced.
func isCiliumHelmReleasePatch(patch map[string]interface{}) bool {
	target, ok := patch["target"].(map[string]interface{})
	if !ok {
		return false
	}
	if target["kind"] != "HelmRelease" || target["name"] != "cilium" {
		return false
	}
	body, _ := patch["patch"].(string)
	return body != ""
}
