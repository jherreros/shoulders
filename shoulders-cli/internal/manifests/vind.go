package manifests

import (
	"fmt"

	"sigs.k8s.io/yaml"

	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
)

// Gateway listener ports inside the cluster. 2-addons/manifests/gateway/gateway.yaml
// declares exactly these two listeners, and Cilium's Envoy binds them on the
// node because gatewayAPI.hostNetwork.enabled is set. They are not configurable
// per cluster: only the host side of the Docker mapping is.
const (
	GatewayHTTPPort  = 80
	GatewayHTTPSPort = 443
)

// VindValues renders the vCluster values for one resolved cluster.
//
// Three cluster-scoped settings are applied:
//
//  1. Host ports. 1-cluster/vind-config*.yaml hardcodes 80:80 and 443:443, so
//     only one vind cluster can exist at a time. A topology gives each cluster
//     its own mapping, which resolves that (target-doc risk R3).
//  2. experimental.docker.network — vCluster otherwise creates an isolated
//     Docker bridge per cluster, leaving node InternalIPs mutually unreachable.
//     Applied only when the mesh is enabled.
//  3. networking.podCIDR — vCluster's chart default is the same fixed
//     10.244.0.0/16 for every cluster, and the collision breaks cross-cluster
//     datapath traffic. Applied only when the mesh is enabled.
//
// The input is parsed and re-marshalled rather than string-patched so existing
// keys are preserved exactly and invalid YAML fails loudly.
func VindValues(profile string, cluster config.ResolvedCluster) ([]byte, error) {
	base := VindConfigForProfile(profile)

	var values map[string]interface{}
	if err := yaml.Unmarshal(base, &values); err != nil {
		return nil, fmt.Errorf("parse vend values: %w", err)
	}
	if values == nil {
		values = map[string]interface{}{}
	}

	// Host ports are always rendered, mesh or not: they are per-cluster config
	// regardless of networking topology.
	//
	// Only the host side is configurable. The container side is fixed: Cilium's
	// Gateway runs Envoy with hostNetwork on the node, and the Gateway listeners
	// bind 80 and 443 inside the cluster whatever the host maps them to. A
	// mapping of 8080:8080 publishes a port nothing listens on, and the spoke's
	// ingress answers 000 from the host while working perfectly in-cluster.
	ports := cluster.Ports.WithDefaults()
	if err := ports.Validate(cluster.Name); err != nil {
		return nil, err
	}
	setNestedValue(values, []interface{}{
		fmt.Sprintf("%d:%d", ports.HTTP, GatewayHTTPPort),
		fmt.Sprintf("%d:%d", ports.HTTPS, GatewayHTTPSPort),
	}, "experimental", "docker", "ports")

	if !cluster.MeshEnabled() {
		rendered, err := yaml.Marshal(values)
		if err != nil {
			return nil, fmt.Errorf("render vend values: %w", err)
		}
		return rendered, nil
	}

	cidr := cluster.ClusterPodCIDR()
	if err := config.ValidatePodCIDR(cidr); err != nil {
		return nil, fmt.Errorf("allocate pod CIDR for cluster %q: %w", cluster.Name, err)
	}

	setNestedValue(values, cidr, "networking", "podCIDR")
	setNestedValue(values, cluster.MeshNetwork(), "experimental", "docker", "network")
	// vCluster only exposes type=LoadBalancer services when it can add a
	// loopback alias, which on macOS shells out to ifconfig and needs sudo.
	// It also warns at create time that LoadBalancer support is unavailable
	// without elevated privileges (see docs/multi-cluster-evidence.md §4.1,
	// which is why clustermesh-apiserver uses NodePort). A non-default Docker
	// network is not reachable by the container's own hostname, so vCluster
	// would try that path and fail on hosts without ifconfig. Disable it: mesh
	// mode does not rely on LoadBalancer services.
	setNestedValue(values, false, "experimental", "docker", "loadBalancer", "enabled")
	setNestedValue(values, false, "experimental", "docker", "loadBalancer", "forwardPorts")

	rendered, err := yaml.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("render vend values: %w", err)
	}
	return rendered, nil
}

// setNestedValue assigns value at the given path, creating intermediate maps as
// needed. Paths walk existing keys when they are already maps.
func setNestedValue(values map[string]interface{}, value interface{}, path ...string) {
	if len(path) == 0 {
		return
	}
	cursor := values
	for _, key := range path[:len(path)-1] {
		next, ok := cursor[key].(map[string]interface{})
		if !ok {
			next = map[string]interface{}{}
			cursor[key] = next
		}
		cursor = next
	}
	cursor[path[len(path)-1]] = value
}
