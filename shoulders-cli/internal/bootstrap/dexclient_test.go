package bootstrap

import (
	"testing"

	"sigs.k8s.io/yaml"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// The registration replaces dex's whole staticClients list, because kustomize
// has no merge key for it. So reading the list wrong is destructive: the hub
// would lose its own clients.
//
// The first implementation read the list with NestedMap, which errors on a
// list. The error was treated as "no clients", and the patch replaced the
// list with just the new one — removing the hub's own clients. The spoke
// registered; the hub's Headlamp silently stopped working.
//
// These tests read the same list the registration does.
func TestDexStaticClientsReadsAList(t *testing.T) {
	release := &unstructured.Unstructured{Object: map[string]interface{}{
		"spec": map[string]interface{}{
			"values": map[string]interface{}{
				"config": map[string]interface{}{
					"staticClients": []interface{}{
						map[string]interface{}{
							"id":           "grafana",
							"name":         "Grafana",
							"secret":       "grafana-dex-secret",
							"redirectURIs": []interface{}{"http://grafana.example/login/generic_oauth"},
						},
						map[string]interface{}{
							"id":           "kubernetes",
							"name":         "Kubernetes",
							"secret":       "headlamp-dex-secret",
							"redirectURIs": []interface{}{"http://headlamp.example/oidc-callback"},
						},
					},
				},
			},
		},
	}}

	clients, err := dexStaticClients(release)
	if err != nil {
		t.Fatalf("read the clients: %v", err)
	}
	if len(clients) != 2 {
		t.Fatalf("expected 2 clients, got %d: %+v", len(clients), clients)
	}
	if clients[0].ID != "grafana" || clients[1].ID != "kubernetes" {
		t.Fatalf("clients read wrong: %+v", clients)
	}
	if clients[0].Secret != "grafana-dex-secret" {
		t.Errorf("secret = %q", clients[0].Secret)
	}
	if len(clients[0].RedirectURIs) != 1 || clients[0].RedirectURIs[0] != "http://grafana.example/login/generic_oauth" {
		t.Errorf("redirectURIs = %v", clients[0].RedirectURIs)
	}
}

// The registration must keep the clients that are already there.
func TestWithClientReplacesOnlyTheSameID(t *testing.T) {
	existing := []dexClient{
		{ID: "grafana", Name: "Grafana", Secret: "s1", RedirectURIs: []string{"http://grafana/cb"}},
		{ID: "kubernetes", Name: "Kubernetes", Secret: "s2", RedirectURIs: []string{"http://headlamp/cb"}},
	}
	updated := withClient(existing, dexClient{
		ID: "kubernetes", Name: "Kubernetes (spoke)", Secret: "s3",
		RedirectURIs: []string{"http://headlamp-spoke/cb"},
	})

	if len(updated) != 2 {
		t.Fatalf("expected 2 clients, got %d: %+v", len(updated), updated)
	}
	if updated[0].ID != "grafana" {
		t.Errorf("the hub's own client was dropped: %+v", updated)
	}
	if updated[1].ID != "kubernetes" || updated[1].Secret != "s3" {
		t.Errorf("the client was not replaced: %+v", updated[1])
	}
}

// A client that is not there yet is appended, not dropped.
func TestWithClientAppendsANewID(t *testing.T) {
	existing := []dexClient{{ID: "grafana", Name: "Grafana"}}
	updated := withClient(existing, dexClient{ID: "headlamp-spoke", Name: "Spoke"})

	if len(updated) != 2 {
		t.Fatalf("expected 2 clients, got %d", len(updated))
	}
	if updated[1].ID != "headlamp-spoke" {
		t.Errorf("the new client is wrong: %+v", updated[1])
	}
}

// The rendered patch must keep the rest of the dex values. It is a merge patch
// on an opaque field, so a malformed one would take the whole configuration
// with it — including the issuer, without which dex does not start.
func TestDexStaticClientsPatchKeepsTheOtherValues(t *testing.T) {
	clients := []dexClient{
		{ID: "kubernetes", Name: "Kubernetes", Secret: "s2", RedirectURIs: []string{"http://headlamp/cb"}},
	}
	patch, err := dexStaticClientsPatch(clients)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"apiVersion: helm.toolkit.fluxcd.io/v2",
		"kind: HelmRelease",
		"  name: dex",
		"  namespace: dex",
		"spec:",
		"  values:",
		"    config:",
		"      staticClients:",
		"        - id: kubernetes",
	} {
		if !contains(patch, want) {
			t.Errorf("the patch is missing %q:\n%s", want, patch)
		}
	}
}

// The patch must be valid YAML, or flux rejects the whole Kustomization.
func TestDexStaticClientsPatchIsValidYAML(t *testing.T) {
	patch, err := dexStaticClientsPatch([]dexClient{
		{ID: "a", Name: "A", Secret: "s", RedirectURIs: []string{"http://a/cb", "http://a/cb2"}},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var parsed struct {
		Spec struct {
			Values struct {
				Config struct {
					StaticClients []struct {
						ID           string   `json:"id"`
						RedirectURIs []string `json:"redirectURIs"`
					} `json:"staticClients"`
				} `json:"config"`
			} `json:"values"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal([]byte(patch), &parsed); err != nil {
		t.Fatalf("the patch is not valid YAML: %v\n%s", err, patch)
	}
	if len(parsed.Spec.Values.Config.StaticClients) != 1 {
		t.Fatalf("the patch has %d clients", len(parsed.Spec.Values.Config.StaticClients))
	}
	if len(parsed.Spec.Values.Config.StaticClients[0].RedirectURIs) != 2 {
		t.Errorf("the redirect URIs were lost: %+v", parsed.Spec.Values.Config.StaticClients[0])
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
