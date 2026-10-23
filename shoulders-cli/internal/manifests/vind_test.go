package manifests

import (
	"fmt"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
)

func renderVind(t *testing.T, values []byte) map[string]interface{} {
	t.Helper()
	var parsed map[string]interface{}
	if err := yaml.Unmarshal(values, &parsed); err != nil {
		t.Fatalf("rendered values are not valid YAML: %v\n%s", err, values)
	}
	return parsed
}

func clusterWith(t *testing.T, mutate func(*config.ResolvedCluster)) config.ResolvedCluster {
	t.Helper()
	cluster := config.ResolvedCluster{
		PlatformConfig: config.PlatformConfig{
			Profile: config.ProfileSmall,
			Cilium:  config.CiliumConfig{Version: "1.19.2"},
		},
		Name: "spoke-a",
		Role: config.RoleSpoke,
	}
	if mutate != nil {
		mutate(&cluster)
	}
	return cluster
}

func dockerBlock(t *testing.T, values []byte) map[string]interface{} {
	t.Helper()
	parsed := renderVind(t, values)
	experimental, ok := parsed["experimental"].(map[string]interface{})
	if !ok {
		t.Fatalf("experimental block missing:\n%s", values)
	}
	docker, ok := experimental["docker"].(map[string]interface{})
	if !ok {
		t.Fatalf("experimental.docker missing:\n%s", values)
	}
	return docker
}

// TestVindValuesDefaultPortsMatchProfile is the monocluster guard: with no
// explicit mapping a cluster must still bind the profile's 80/443, exactly as
// the hardcoded vend values did.
func TestVindValuesDefaultPortsMatchProfile(t *testing.T) {
	got, err := VindValues(config.ProfileSmall, clusterWith(t, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	docker := dockerBlock(t, got)
	ports, ok := docker["ports"].([]interface{})
	if !ok || len(ports) != 2 {
		t.Fatalf("ports missing or wrong shape:\n%s", got)
	}
	want := []string{"80:80", "443:443"}
	for i, expected := range want {
		if fmt.Sprint(ports[i]) != expected {
			t.Fatalf("port %d = %v, want %q", i, ports[i], expected)
		}
	}
}

// TestVindValuesRendersPerClusterPorts is target-doc risk R3: two clusters must
// be able to coexist, which they cannot while both bind 80/443.
func TestVindValuesRendersPerClusterPorts(t *testing.T) {
	got, err := VindValues(config.ProfileSmall, clusterWith(t, func(c *config.ResolvedCluster) {
		c.Ports = config.PortMapping{HTTP: 8080, HTTPS: 8443}
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	docker := dockerBlock(t, got)
	ports := docker["ports"].([]interface{})
	// Only the host side moves. The container side is where the gateway's
	// hostNetwork Envoy binds its listeners, which is 80/443 in every cluster.
	if fmt.Sprint(ports[0]) != "8080:80" || fmt.Sprint(ports[1]) != "8443:443" {
		t.Fatalf("per-cluster ports not rendered: %v\n%s", ports, got)
	}
}

func TestVindValuesRejectsInvalidPorts(t *testing.T) {
	if _, err := VindValues(config.ProfileSmall, clusterWith(t, func(c *config.ResolvedCluster) {
		c.Ports = config.PortMapping{HTTP: 70000, HTTPS: 443}
	})); err == nil {
		t.Fatal("expected an error for an out-of-range port")
	}
}

// TestVindValuesNoMeshKeepsSingleClusterShape asserts that without the mesh the
// only change is the port rendering: no networking block, no shared network.
func TestVindValuesNoMeshKeepsSingleClusterShape(t *testing.T) {
	got, err := VindValues(config.ProfileSmall, clusterWith(t, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parsed := renderVind(t, got)
	if _, ok := parsed["networking"]; ok {
		t.Fatalf("networking block present without the mesh:\n%s", got)
	}
	docker := dockerBlock(t, got)
	if _, ok := docker["network"]; ok {
		t.Fatalf("shared network present without the mesh:\n%s", got)
	}
	if lb, ok := docker["loadBalancer"]; ok {
		t.Fatalf("loadBalancer overrides present without the mesh: %v", lb)
	}
}

func TestVindValuesInjectsMeshSettings(t *testing.T) {
	got, err := VindValues(config.ProfileSmall, clusterWith(t, func(c *config.ResolvedCluster) {
		c.Mesh = config.MeshConfig{Enabled: true, Network: "shoulders-mesh", PodCIDRBase: 16}
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parsed := renderVind(t, got)

	networking, ok := parsed["networking"].(map[string]interface{})
	if !ok {
		t.Fatalf("networking block missing:\n%s", got)
	}
	cidr, _ := networking["podCIDR"].(string)
	if cidr != config.PodCIDRFor("spoke-a", 16) {
		t.Fatalf("podCIDR = %q", cidr)
	}
	if err := config.ValidatePodCIDR(cidr); err != nil {
		t.Fatalf("injected podCIDR invalid: %v", err)
	}

	docker := dockerBlock(t, got)
	if docker["network"] != "shoulders-mesh" {
		t.Fatalf("shared network = %v", docker["network"])
	}
	lb, ok := docker["loadBalancer"].(map[string]interface{})
	if !ok || lb["enabled"] != false || lb["forwardPorts"] != false {
		t.Fatalf("loadBalancer should be disabled in mesh mode, got %v", lb)
	}
}

// TestVindValuesPreservesExistingKeys guards against the re-marshal dropping or
// corrupting settings the platform relies on.
func TestVindValuesPreservesExistingKeys(t *testing.T) {
	got, err := VindValues(config.ProfileSmall, clusterWith(t, func(c *config.ResolvedCluster) {
		c.Mesh = config.MeshConfig{Enabled: true}
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parsed := renderVind(t, got)

	if deploy, ok := parsed["deploy"].(map[string]interface{}); !ok || deploy["cni"] == nil {
		t.Fatalf("deploy.cni was dropped:\n%s", got)
	}
	if privateNodes, ok := parsed["privateNodes"].(map[string]interface{}); !ok || privateNodes["joinNode"] == nil {
		t.Fatalf("privateNodes.joinNode was dropped:\n%s", got)
	}
	if controlPlane, ok := parsed["controlPlane"].(map[string]interface{}); !ok || controlPlane["distro"] == nil {
		t.Fatalf("controlPlane.distro was dropped:\n%s", got)
	}

	// The experimental.docker block is MERGED, not replaced. Losing "nodes"
	// here would silently downgrade every meshed cluster to control-plane only.
	docker := dockerBlock(t, got)
	nodes, ok := docker["nodes"].([]interface{})
	if !ok || len(nodes) == 0 {
		t.Fatalf("experimental.docker.nodes was dropped; meshed clusters would "+
			"lose their worker node:\n%s", got)
	}
	if _, ok := docker["ports"]; !ok {
		t.Fatalf("host ports were dropped:\n%s", got)
	}
}

// TestVindValuesGivesClustersDistinctCIDRs is the fix for the collision that
// made every cluster claim 10.244.0.0/16.
func TestVindValuesGivesClustersDistinctCIDRs(t *testing.T) {
	seen := map[string]string{}
	for _, name := range []string{"hub", "spoke-a", "spoke-b"} {
		got, err := VindValues(config.ProfileSmall, clusterWith(t, func(c *config.ResolvedCluster) {
			c.Name = name
			c.Mesh = config.MeshConfig{Enabled: true}
		}))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		parsed := renderVind(t, got)
		cidr := parsed["networking"].(map[string]interface{})["podCIDR"].(string)
		if prev, clash := seen[cidr]; clash {
			t.Fatalf("clusters %q and %q share pod CIDR %s", prev, name, cidr)
		}
		seen[cidr] = name
	}
}

func TestVindValuesAllProfiles(t *testing.T) {
	for _, profile := range []string{config.ProfileSmall, config.ProfileMedium, config.ProfileLarge} {
		got, err := VindValues(profile, clusterWith(t, func(c *config.ResolvedCluster) {
			c.Mesh = config.MeshConfig{Enabled: true}
		}))
		if err != nil {
			t.Fatalf("%s: %v", profile, err)
		}
		docker := dockerBlock(t, got)
		if _, ok := docker["ports"]; !ok {
			t.Fatalf("%s: ports missing:\n%s", profile, got)
		}
		if !strings.Contains(string(got), "podCIDR") {
			t.Fatalf("%s: podCIDR missing", profile)
		}
	}
}
