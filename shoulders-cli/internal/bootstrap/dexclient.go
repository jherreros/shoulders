package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"sigs.k8s.io/yaml"
)

// RegisterHeadlampClient registers a Dex client for a spoke's Headlamp.
//
// The hub is the topology's only OIDC provider, so a spoke's Headlamp
// authenticates against the hub's Dex — and Dex matches redirect URIs
// **exactly**. There is no wildcard: a stored `http://headlamp-*` was measured
// rejecting every callback it should have matched, including the hub's own
// pattern. So each spoke needs its own client, registered with its own callback
// URI.
//
// The client is registered by patching the hub's dex HelmRelease through the
// Flux Kustomization that owns it. Two alternatives were considered and
// rejected:
//
//   - Dex's `StaticClient`/`OAuth2Client` CRD: the hub's dex runs with
//     `storage: {type: memory}`, so it never reads the CRDs. Switching dex to
//     kubernetes storage would move its whole configuration out of the
//     HelmRelease, which is a much larger change than this problem needs.
//   - Dex's gRPC CreateClient: it needs a mutually trusted client certificate
//     and a gRPC endpoint the chart does not expose by default.
//
// Patching the HelmRelease through the Kustomization is the option that
// changes nothing else, and it is the same mechanism the tier exclusions use.
//
// The patch replaces the whole `staticClients` list, because kustomize has no
// merge key for it — the HelmRelease CRD preserves unknown fields rather than
// describing them. So the list is read from the live release and written back
// with the new client added, which keeps whatever the operator has configured.
func RegisterHeadlampClient(
	ctx context.Context,
	kubeconfigPath string,
	clientID string,
	clientName string,
	clientSecret string,
	redirectURI string,
) error {
	client, err := kube.NewDynamicClient(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}

	helmReleaseGVR := kube.HelmReleaseGVR()
	release, err := client.Resource(helmReleaseGVR).Namespace("dex").Get(ctx, "dex", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get the hub's dex helmrelease: %w", err)
	}

	clients, err := dexStaticClients(release)
	if err != nil {
		return err
	}
	clients = withClient(clients, dexClient{
		ID:           clientID,
		Name:         clientName,
		Secret:       clientSecret,
		RedirectURIs: []string{redirectURI},
	})

	patch, err := dexStaticClientsPatch(clients)
	if err != nil {
		return err
	}
	if err := setKustomizationPatch(ctx, client, "helm-releases", helmReleasePatch{
		Target: map[string]interface{}{
			"kind":      "HelmRelease",
			"name":      "dex",
			"namespace": "dex",
		},
		Patch: patch,
	}); err != nil {
		return err
	}

	return waitForDexClient(ctx, client, helmReleaseGVR, clientID, redirectURI)
}

// dexClient is one entry of dex's staticClients.
type dexClient struct {
	ID           string
	Name         string
	Secret       string
	RedirectURIs []string
}

// dexStaticClients reads the static clients from a dex HelmRelease.
//
// The list is read with NestedSlice, not NestedMap: staticClients is a list, and
// NestedMap returns an error for one. Treating that error as "no clients" would
// make the patch below replace the whole list with just the new client —
// silently removing the hub's own.
func dexStaticClients(release *unstructured.Unstructured) ([]dexClient, error) {
	raw, found, err := unstructured.NestedSlice(release.Object, "spec", "values", "config", "staticClients")
	if err != nil || !found {
		// A dex with no static clients at all is unusual but not an error.
		return nil, nil
	}
	clients := make([]dexClient, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		client := dexClient{}
		client.ID, _ = entry["id"].(string)
		client.Name, _ = entry["name"].(string)
		client.Secret, _ = entry["secret"].(string)
		client.RedirectURIs = append(client.RedirectURIs, toStringSlice(entry["redirectURIs"])...)
		clients = append(clients, client)
	}
	return clients, nil
}

// withClient replaces a client with the same id, or appends it.
func withClient(clients []dexClient, client dexClient) []dexClient {
	out := make([]dexClient, 0, len(clients)+1)
	for _, existing := range clients {
		if existing.ID == client.ID {
			continue
		}
		out = append(out, existing)
	}
	return append(out, client)
}

// dexStaticClientsPatch renders the patch that sets the static clients.
func dexStaticClientsPatch(clients []dexClient) (string, error) {
	type entry struct {
		ID           string   `json:"id"`
		Name         string   `json:"name"`
		Secret       string   `json:"secret"`
		RedirectURIs []string `json:"redirectURIs"`
	}
	entries := make([]entry, 0, len(clients))
	for _, client := range clients {
		entries = append(entries, entry(client))
	}
	body, err := yaml.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("render the dex clients: %w", err)
	}
	return "apiVersion: helm.toolkit.fluxcd.io/v2\n" +
		"kind: HelmRelease\n" +
		"metadata:\n" +
		"  name: dex\n" +
		"  namespace: dex\n" +
		"spec:\n" +
		"  values:\n" +
		"    config:\n" +
		"      staticClients:\n" +
		indentYAML(body, "        "), nil
}

// waitForDexClient waits until the hub's dex release carries the client.
//
// The registration only takes effect when flux reconciles, and the spoke cannot
// authenticate before it does.
func waitForDexClient(
	ctx context.Context,
	client dynamic.Interface,
	gvr schema.GroupVersionResource,
	clientID string,
	redirectURI string,
) error {
	deadline := time.Now().Add(5 * time.Minute)
	for {
		release, err := client.Resource(gvr).Namespace("dex").Get(ctx, "dex", metav1.GetOptions{})
		if err == nil {
			clients, cerr := dexStaticClients(release)
			if cerr == nil {
				for _, registered := range clients {
					if registered.ID != clientID {
						continue
					}
					for _, uri := range registered.RedirectURIs {
						if uri == redirectURI {
							return nil
						}
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the hub's dex did not register the client %q within the timeout", clientID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// helmReleasePatch is a patch on a flux Kustomization's build.
type helmReleasePatch struct {
	Target map[string]interface{}
	Patch  string
}

// setKustomizationPatch adds or replaces a patch on a flux Kustomization.
//
// It replaces any existing patch with the same target, so repeated calls
// converge rather than accumulating.
func setKustomizationPatch(ctx context.Context, client dynamic.Interface, name string, patch helmReleasePatch) error {
	gvr := schema.GroupVersionResource{
		Group:    "kustomize.toolkit.fluxcd.io",
		Version:  "v1",
		Resource: "kustomizations",
	}
	kustomization, err := client.Resource(gvr).Namespace("flux-system").Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get the %s kustomization: %w", name, err)
	}

	existing, _ := nestedSlice(kustomization.Object, "spec", "patches")
	kept := make([]interface{}, 0, len(existing)+1)
	for _, item := range existing {
		if targetsSame(item, patch.Target) {
			continue
		}
		kept = append(kept, item)
	}
	kept = append(kept, map[string]interface{}{
		"target": patch.Target,
		"patch":  patch.Patch,
	})

	body, err := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{"patches": kept},
	})
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}
	if _, err := client.Resource(gvr).Namespace("flux-system").Patch(
		ctx, name, types.MergePatchType, body, metav1.PatchOptions{},
	); err != nil {
		return fmt.Errorf("patch the %s kustomization: %w", name, err)
	}
	return nil
}

// targetsSame reports whether a patch targets the same resource.
func targetsSame(item interface{}, target map[string]interface{}) bool {
	entry, ok := item.(map[string]interface{})
	if !ok {
		return false
	}
	other, ok := entry["target"].(map[string]interface{})
	if !ok {
		return false
	}
	for _, key := range []string{"kind", "name", "namespace"} {
		if fmt.Sprint(other[key]) != fmt.Sprint(target[key]) {
			return false
		}
	}
	return true
}

// toStringSlice converts an unstructured value to a string slice.
func toStringSlice(value interface{}) []string {
	items, ok := value.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}
