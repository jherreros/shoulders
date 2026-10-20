package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"

	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
)

func clusterNamed(name, role string, meshed bool, ports config.PortMapping) *config.ResolvedCluster {
	cluster := &config.ResolvedCluster{
		PlatformConfig: config.PlatformConfig{
			Profile: config.ProfileSmall,
			Mesh:    config.MeshConfig{Network: config.DefaultMeshNetwork, PodCIDRBase: 16},
		},
		Name:  name,
		Role:  role,
		Ports: ports.WithDefaults(),
	}
	if meshed {
		cluster.Mesh.Enabled = true
	}
	return cluster
}

func TestValidateMeshedTopology(t *testing.T) {
	t.Run("accepts a well formed mesh", func(t *testing.T) {
		clusters := []*config.ResolvedCluster{
			clusterNamed("hub", config.RoleHub, true, config.PortMapping{HTTP: 80, HTTPS: 443}),
			clusterNamed("spoke-a", config.RoleSpoke, true, config.PortMapping{HTTP: 8080, HTTPS: 8443}),
		}
		if err := validateMeshedTopology(clusters); err != nil {
			t.Fatalf("expected a valid mesh, got %v", err)
		}
	})

	t.Run("rejects a single meshed cluster", func(t *testing.T) {
		clusters := []*config.ResolvedCluster{
			clusterNamed("hub", config.RoleHub, true, config.PortMapping{HTTP: 80, HTTPS: 443}),
			clusterNamed("spoke-a", config.RoleSpoke, false, config.PortMapping{HTTP: 8080, HTTPS: 8443}),
		}
		err := validateMeshedTopology(clusters)
		if err == nil || !strings.Contains(err.Error(), "at least two clusters") {
			t.Fatalf("expected an 'at least two' error, got %v", err)
		}
	})

	t.Run("rejects clusters on different networks", func(t *testing.T) {
		a := clusterNamed("hub", config.RoleHub, true, config.PortMapping{HTTP: 80, HTTPS: 443})
		b := clusterNamed("spoke-a", config.RoleSpoke, true, config.PortMapping{HTTP: 8080, HTTPS: 8443})
		b.Mesh.Network = "other-net"
		err := validateMeshedTopology([]*config.ResolvedCluster{a, b})
		if err == nil || !strings.Contains(err.Error(), "share one Docker network") {
			t.Fatalf("expected a network error, got %v", err)
		}
	})

	t.Run("accepts a uniformly encrypted mesh", func(t *testing.T) {
		clusters := []*config.ResolvedCluster{
			clusterNamed("hub", config.RoleHub, true, config.PortMapping{HTTP: 80, HTTPS: 443}),
			clusterNamed("spoke-a", config.RoleSpoke, true, config.PortMapping{HTTP: 8080, HTTPS: 8443}),
		}
		for _, c := range clusters {
			c.Mesh.Encryption = "wireguard"
		}
		if err := validateMeshedTopology(clusters); err != nil {
			t.Fatalf("expected a valid encrypted mesh, got %v", err)
		}
	})

	t.Run("rejects a half-encrypted mesh", func(t *testing.T) {
		clusters := []*config.ResolvedCluster{
			clusterNamed("hub", config.RoleHub, true, config.PortMapping{HTTP: 80, HTTPS: 443}),
			clusterNamed("spoke-a", config.RoleSpoke, true, config.PortMapping{HTTP: 8080, HTTPS: 8443}),
		}
		clusters[0].Mesh.Encryption = "wireguard"
		err := validateMeshedTopology(clusters)
		if err == nil || !strings.Contains(err.Error(), "whole-mesh property") {
			t.Fatalf("expected a mixed-encryption error, got %v", err)
		}
	})

	t.Run("rejects an unknown encryption mode", func(t *testing.T) {
		clusters := []*config.ResolvedCluster{
			clusterNamed("hub", config.RoleHub, true, config.PortMapping{HTTP: 80, HTTPS: 443}),
			clusterNamed("spoke-a", config.RoleSpoke, true, config.PortMapping{HTTP: 8080, HTTPS: 8443}),
		}
		clusters[0].Mesh.Encryption = "ipsec"
		err := validateMeshedTopology(clusters)
		if err == nil || !strings.Contains(err.Error(), "supported values: wireguard") {
			t.Fatalf("expected an unsupported-encryption error, got %v", err)
		}
	})

	t.Run("rejects duplicate cluster ids", func(t *testing.T) { // Cluster ids are name-derived, so a collision is possible (255 slots).
		// Find such a pair and assert the mesh is refused rather than silently
		// joining with two identities.
		byID := map[int]*config.ResolvedCluster{}
		var pair []*config.ResolvedCluster
		for i := 0; i < 20000 && len(pair) < 2; i++ {
			name := fmt.Sprintf("cluster-%d", i)
			candidate := clusterNamed(name, config.RoleSpoke, true,
				config.PortMapping{HTTP: 9000 + i, HTTPS: 9500 + i})
			id := candidate.ClusterID()
			if existing, clash := byID[id]; clash {
				pair = []*config.ResolvedCluster{existing, candidate}
				break
			}
			byID[id] = candidate
		}
		if pair == nil {
			t.Skip("no colliding id found")
		}
		if pair[0].ClusterID() != pair[1].ClusterID() {
			t.Fatalf("test setup error: %q and %q do not collide", pair[0].Name, pair[1].Name)
		}
		// One of them must be the hub for the topology to be otherwise valid.
		pair[0].Role = config.RoleHub
		pair[0].Ports = config.PortMapping{HTTP: 80, HTTPS: 443}.WithDefaults()

		err := validateMeshedTopology(pair)
		if err == nil || !strings.Contains(err.Error(), "ids must be unique") {
			t.Fatalf("expected a duplicate id error, got %v", err)
		}
	})
}

func TestMeshPlanPeersExcludeSelf(t *testing.T) {
	clusters := []*config.ResolvedCluster{
		clusterNamed("hub", config.RoleHub, true, config.PortMapping{HTTP: 80, HTTPS: 443}),
		clusterNamed("spoke-a", config.RoleSpoke, true, config.PortMapping{HTTP: 8080, HTTPS: 8443}),
		clusterNamed("spoke-b", config.RoleSpoke, true, config.PortMapping{HTTP: 8081, HTTPS: 8444}),
		// A cluster that is not meshed must never appear as a peer.
		clusterNamed("plain", config.RoleSpoke, false, config.PortMapping{HTTP: 8082, HTTPS: 8445}),
	}
	plan := &meshPlan{
		clusters: clusters,
		addresses: map[string]string{
			"hub":     "10.90.0.2:32379",
			"spoke-a": "10.90.0.4:32379",
			"spoke-b": "10.90.0.5:32379",
			"plain":   "10.90.0.6:32379",
		},
	}

	hubPeers := plan.peers(clusters[0])
	if _, self := hubPeers["hub"]; self {
		t.Fatal("a cluster must not be its own peer")
	}
	// Peers are addressed by DNS name with the port as a separate field: the
	// certificates carry DNS SANs and no IP SANs, so an IP address would fail
	// TLS verification even when the peer is reachable.
	if hubPeers["spoke-a"] != "spoke-a.mesh.cilium.io" {
		t.Fatalf("spoke-a peer = %q, want spoke-a.mesh.cilium.io", hubPeers["spoke-a"])
	}
	if _, unmeshed := hubPeers["plain"]; unmeshed {
		t.Fatal("a non-meshed cluster must not be listed as a peer")
	}
	if len(hubPeers) != 2 {
		t.Fatalf("expected 2 peers, got %d: %v", len(hubPeers), hubPeers)
	}
}

func TestMeshPlanOptionsForCarriesMeasuredDefaults(t *testing.T) {
	clusters := []*config.ResolvedCluster{
		clusterNamed("hub", config.RoleHub, true, config.PortMapping{HTTP: 80, HTTPS: 443}),
		clusterNamed("spoke-a", config.RoleSpoke, true, config.PortMapping{HTTP: 8080, HTTPS: 8443}),
	}
	plan := &meshPlan{
		clusters:  clusters,
		addresses: map[string]string{"hub": "10.90.0.2:32379", "spoke-a": "10.90.0.4:32379"},
	}

	options := plan.optionsFor(clusters[0])
	if options == nil {
		t.Fatal("expected mesh options for a meshed cluster")
	}
	// Every one of these was established by measurement; see
	// docs/multi-cluster-evidence.md. They are not stylistic choices.
	if options.TLSAuthMode != "legacy" {
		t.Fatalf("authMode = %q, want legacy", options.TLSAuthMode)
	}
	if options.ServiceType != "NodePort" {
		t.Fatalf("serviceType = %q, want NodePort", options.ServiceType)
	}
	if options.TLSCertMethod != "helm" {
		t.Fatalf("certMethod = %q, want helm", options.TLSCertMethod)
	}
	if options.ClusterName != "hub" {
		t.Fatalf("clusterName = %q", options.ClusterName)
	}
	if options.ClusterID != clusters[0].ClusterID() {
		t.Fatalf("clusterID = %d", options.ClusterID)
	}
	wantDNS := []string{"hub.mesh.cilium.io"}
	if len(options.ExtraDNSNames) != 1 || options.ExtraDNSNames[0] != wantDNS[0] {
		t.Fatalf("extraDnsNames = %v", options.ExtraDNSNames)
	}

	// Encryption is a whole-mesh property carried through the same options
	// struct; the values renderer is what turns it into Cilium's block.
	clusters[0].Mesh.Encryption = "wireguard"
	if got := plan.optionsFor(clusters[0]).Encryption; got != "wireguard" {
		t.Fatalf("encryption = %q, want wireguard", got)
	}

	// A non-meshed cluster gets no mesh options at all.
	if got := plan.optionsFor(clusterNamed("plain", config.RoleSpoke, false, config.PortMapping{})); got != nil {
		t.Fatalf("expected nil options for a non-meshed cluster, got %+v", got)
	}
}

func TestSeedSharedCAIsNoopWithoutAnAnchor(t *testing.T) {
	// Before the first cluster installs, there is no trust anchor to share.
	if err := seedSharedCA(context.Background(), clusterNamed("hub", config.RoleHub, true, config.PortMapping{}), nil); err != nil {
		t.Fatalf("expected a no-op, got %v", err)
	}
}

func TestTopologyAllClustersPutsHubFirst(t *testing.T) {
	cfg, err := config.Load(writeTargetTestConfig(t, `
platform: { profile: small }
clusters:
  - { name: spoke-a, role: spoke, ports: { http: 8080, https: 8443 } }
  - { name: hub, role: hub, domain: "hub.example.test" }
  - { name: spoke-b, role: spoke, ports: { http: 8081, https: 8444 } }
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	topology, err := cfg.Topology()
	if err != nil {
		t.Fatalf("topology: %v", err)
	}
	all := topology.AllClusters()
	if len(all) != 3 {
		t.Fatalf("expected 3 clusters, got %d", len(all))
	}
	if all[0].Name != "hub" {
		t.Fatalf("first cluster = %q, want hub (it becomes the trust anchor)", all[0].Name)
	}
}

// TestMeshClusterIDRange guards the value Cilium accepts (1-255).
func TestMeshClusterIDRange(t *testing.T) {
	seen := map[int]string{}
	for _, name := range []string{"hub", "spoke-a", "spoke-b", "edge", "a", "b", "shoulders", "dev"} {
		id := config.MeshClusterID(name)
		if id < 1 || id > 255 {
			t.Fatalf("%s got id %d, outside 1-255", name, id)
		}
		if id != config.MeshClusterID(name) {
			t.Fatalf("%s id is not stable", name)
		}
		seen[id] = name
	}
	if len(seen) != 8 {
		t.Fatalf("expected 8 distinct ids, got %d", len(seen))
	}
}

// clustersToInstall decides the blast radius of `shoulders up`, so it is worth
// pinning down: in a topology a bare `up` installs everything.
func TestClustersToInstall(t *testing.T) {
	const topologyConfig = `
platform: { profile: small }
clusters:
  - { name: hub, role: hub, domain: "hub.example.test" }
  - { name: spoke-a, role: spoke, ports: { http: 8080, https: 8443 } }
  - { name: spoke-b, role: spoke, ports: { http: 8081, https: 8444 } }
`
	const singleClusterConfig = `
platform: { profile: small }
clusters:
  - { name: hub, role: hub, domain: "hub.example.test" }
`

	load := func(t *testing.T, body string) *config.Topology {
		t.Helper()
		path := writeTargetTestConfig(t, body)
		cfg, err := config.Load(path)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		topology, err := cfg.Topology()
		if err != nil {
			t.Fatalf("topology: %v", err)
		}
		return topology
	}

	names := func(clusters []*config.ResolvedCluster) []string {
		out := make([]string, 0, len(clusters))
		for _, cluster := range clusters {
			out = append(out, cluster.Name)
		}
		return out
	}

	newCmd := func(t *testing.T, args ...string) *cobra.Command {
		t.Helper()
		cmd := &cobra.Command{Use: "up", RunE: runUp}
		cmd.Flags().String("cluster", "", "")
		cmd.Flags().String("name", "", "")
		if err := cmd.Flags().Parse(args); err != nil {
			t.Fatalf("parse flags: %v", err)
		}
		return cmd
	}

	t.Run("a bare up on a topology installs every cluster, hub first", func(t *testing.T) {
		got, err := clustersToInstall(newCmd(t), load(t, topologyConfig))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"hub", "spoke-a", "spoke-b"}
		if strings.Join(names(got), ",") != strings.Join(want, ",") {
			t.Fatalf("got %v, want %v", names(got), want)
		}
	})

	t.Run("--cluster restricts the install to that cluster", func(t *testing.T) {
		targetClusterName = "spoke-a"
		defer func() { targetClusterName = "" }()
		cmd := newCmd(t, "--cluster", "spoke-a")
		got, err := clustersToInstall(cmd, load(t, topologyConfig))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].Name != "spoke-a" {
			t.Fatalf("got %v, want just spoke-a", names(got))
		}
	})

	t.Run("--name restricts the install to that cluster", func(t *testing.T) {
		upClusterName = "spoke-b"
		defer func() { upClusterName = bootstrap.DefaultClusterName }()
		cmd := newCmd(t, "--name", "spoke-b")
		got, err := clustersToInstall(cmd, load(t, topologyConfig))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].Name != "spoke-b" {
			t.Fatalf("got %v, want just spoke-b", names(got))
		}
	})

	t.Run("a single-cluster config installs one cluster", func(t *testing.T) {
		got, err := clustersToInstall(newCmd(t), load(t, singleClusterConfig))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %v, want exactly one cluster", names(got))
		}
	})

	t.Run("an unknown name is an error naming the real clusters", func(t *testing.T) {
		upClusterName = "typo"
		defer func() { upClusterName = bootstrap.DefaultClusterName }()
		cmd := newCmd(t, "--name", "typo")
		_, err := clustersToInstall(cmd, load(t, topologyConfig))
		if err == nil {
			t.Fatal("expected an error for an unknown cluster")
		}
		for _, want := range []string{"typo", "hub", "spoke-a", "spoke-b"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q should mention %q", err, want)
			}
		}
	})
}

// TestWorkloadDomainFor pins the hostname scheme for user workloads.
//
// A spoke has no domain of its own: the hub owns the only domain in the
// topology. A workload on a spoke is therefore named after the spoke inside the
// hub's domain, which keeps two spokes from claiming the same hostname without
// giving either one a second DNS zone.
func TestWorkloadDomainFor(t *testing.T) {
	hub := clusterNamed("hub", "hub", true, config.PortMapping{})
	hub.Domain = "hub.127.0.0.1.sslip.io"
	spoke := clusterNamed("spokea", "spoke", true, config.PortMapping{HTTP: 8080})
	spoke.Domain = ""

	if got, want := workloadDomainFor(hub, hub), "hub.127.0.0.1.sslip.io"; got != want {
		t.Errorf("hub workload domain = %q, want %q", got, want)
	}
	if got, want := workloadDomainFor(spoke, hub), "spokea.hub.127.0.0.1.sslip.io"; got != want {
		t.Errorf("spoke workload domain = %q, want %q", got, want)
	}

	// A monocluster is a hub with no domain configured. The platform's own
	// defaults are hostnames (dex.127.0.0.1.sslip.io), not a domain, so there is
	// no base to qualify a workload host with; the value still has to be well
	// formed.
	mono := clusterNamed("shoulders", "hub", false, config.PortMapping{})
	if got, want := workloadDomainFor(mono, mono), "localhost"; got != want {
		t.Errorf("monocluster workload domain = %q, want %q", got, want)
	}
}
