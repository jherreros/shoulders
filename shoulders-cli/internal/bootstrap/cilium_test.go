package bootstrap

import (
	"strings"
	"testing"
)

// --- ClusterMesh values -------------------------------------------------------
//
// Every value below was established by measurement; see
// docs/multi-cluster-evidence.md §7.2 and §12.4. They are not chart defaults and
// not stylistic choices.

func meshOptionsForTest(peers map[string]string) *ClusterMeshOptions {
	return &ClusterMeshOptions{
		Enabled:     true,
		ClusterName: "hub",
		ClusterID:   211,
		Peers:       peers,
	}
}

// Regression guard. Cilium concatenates `address` and `port` when dialling a
// peer, so putting "IP:port" in the address produced endpoints like
// "https://10.90.0.2:32379:32379". The agent tolerated that and reported a
// connection; KVStoreMesh did not, and the mesh never formed — a failure that
// looks like a flaky network rather than a malformed value.
//
// The address must therefore be a bare name with the port as a separate field.
func TestMeshValuesPeerAddressHasNoPort(t *testing.T) {
	options := meshOptionsForTest(map[string]string{
		"spoke-a": "spoke-a.mesh.cilium.io",
		"spoke-b": "spoke-b.mesh.cilium.io",
	})

	clusters := options.meshValues()["clustermesh"].(map[string]interface{})["config"].(map[string]interface{})["clusters"].(map[string]interface{})
	if len(clusters) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(clusters))
	}

	for name, raw := range clusters {
		peer := raw.(map[string]interface{})
		address, _ := peer["address"].(string)
		port, _ := peer["port"].(int)

		if strings.Contains(address, ":") {
			t.Errorf("peer %q address %q embeds a port; cilium appends one itself", name, address)
		}
		if port != 32379 {
			t.Errorf("peer %q port = %d, want 32379", name, port)
		}
	}
}

// A cluster must not be listed as its own peer: Cilium would try to dial itself.
func TestMeshValuesOmitsSelf(t *testing.T) {
	options := meshOptionsForTest(map[string]string{
		"hub":     "hub.mesh.cilium.io",
		"spoke-a": "spoke-a.mesh.cilium.io",
	})
	clusters := options.meshValues()["clustermesh"].(map[string]interface{})["config"].(map[string]interface{})["clusters"].(map[string]interface{})
	if _, self := clusters["hub"]; self {
		t.Error("the local cluster must not appear in the peer list")
	}
	if len(clusters) != 1 {
		t.Errorf("expected only the remote peer, got %d entries", len(clusters))
	}
}

// The certificate SAN is what the peer address is verified against, so the
// cluster's own mesh name must be in the SANs.
func TestMeshValuesIncludesOwnNameAsDNSName(t *testing.T) {
	values := meshOptionsForTest(nil).meshValues()
	apiserver := values["clustermesh"].(map[string]interface{})["apiserver"].(map[string]interface{})
	tls := apiserver["tls"].(map[string]interface{})
	auto := tls["auto"].(map[string]interface{})
	server := auto["server"].(map[string]interface{})

	names, ok := server["extraDnsNames"].([]interface{})
	if !ok || len(names) == 0 {
		t.Fatalf("extraDnsNames missing: %#v", server)
	}
	if names[0] != "hub.mesh.cilium.io" {
		t.Errorf("first SAN = %v, want hub.mesh.cilium.io", names[0])
	}

	if tls["authMode"] != "legacy" {
		t.Errorf("authMode = %v, want legacy: the chart default (migration) rejects helm-generated certs", tls["authMode"])
	}
	if apiserver["service"].(map[string]interface{})["type"] != "NodePort" {
		t.Errorf("service type must be NodePort; vCluster cannot expose LoadBalancer without sudo")
	}
}

// Cilium 1.19 flips the default to local-cluster scope, and this topology wants
// exactly that: every stateful dependency is local. Keeping the safe default
// means a policy without a cluster selector cannot silently span the mesh. A
// rule that genuinely needs a remote cluster must say so; see evidence §19.
func TestMeshValuesScopesPolicyToTheLocalCluster(t *testing.T) {
	clustermesh := meshOptionsForTest(nil).meshValues()["clustermesh"].(map[string]interface{})
	if clustermesh["policyDefaultLocalCluster"] != true {
		t.Errorf("policyDefaultLocalCluster = %v, want true (local scope)",
			clustermesh["policyDefaultLocalCluster"])
	}
}

func TestMeshValuesWireGuardEncryption(t *testing.T) {
	options := meshOptionsForTest(nil)
	options.Encryption = "wireguard"
	values := options.meshValues()

	encryption, ok := values["encryption"].(map[string]interface{})
	if !ok {
		t.Fatalf("wireguard encryption block missing: %#v", values)
	}
	if encryption["enabled"] != true || encryption["type"] != "wireguard" {
		t.Errorf("encryption = %#v, want enabled wireguard", encryption)
	}
	// Node encryption is deliberately off: Cilium opts control-plane nodes out
	// by default, and the resulting mixed state drops host-bound traffic (the
	// ClusterMesh NodePort and spoke telemetry) on a cold install. Pod traffic is
	// still encrypted, and the mesh control plane is mTLS.
	if encryption["nodeEncryption"] != false {
		t.Errorf("nodeEncryption = %v, want false", encryption["nodeEncryption"])
	}
}

func TestMeshValuesEncryptionIsOffByDefault(t *testing.T) {
	if _, ok := meshOptionsForTest(nil).meshValues()["encryption"]; ok {
		t.Error("encryption must stay opt-in; a default would change the monocluster datapath")
	}
}

func TestMeshValuesDefaultsAreOverridable(t *testing.T) {
	options := &ClusterMeshOptions{Enabled: true, ClusterName: "hub", ClusterID: 1}
	values := options.meshValues()
	if values["routingMode"] != "tunnel" || values["protocol"] != "vxlan" {
		t.Errorf("expected the measured tunnel/vxlan datapath, got %v/%v",
			values["routingMode"], values["protocol"])
	}

	custom := &ClusterMeshOptions{
		Enabled: true, ClusterName: "hub", ClusterID: 1,
		APIServerPort: 12345, TLSAuthMode: "migration", ServiceType: "LoadBalancer",
	}
	apiserver := custom.meshValues()["clustermesh"].(map[string]interface{})["apiserver"].(map[string]interface{})
	if apiserver["service"].(map[string]interface{})["type"] != "LoadBalancer" {
		t.Error("an explicit service type must win over the default")
	}
	peer := apiserver
	if peer["tls"].(map[string]interface{})["authMode"] != "migration" {
		t.Error("an explicit authMode must win over the default")
	}
}
