package config

import (
	"fmt"
	"hash/fnv"
	"net"
	"strings"
)

// Mesh configuration.
//
// This is the first slice of the multi-cluster surface described in
// docs/multi-cluster-target.md (decisions D1/D11). It is deliberately NOT the
// full topology model: it carries only what the CLI needs to create a cluster
// that can join a Cilium ClusterMesh, so the measured prerequisites can be
// satisfied today (see docs/multi-cluster-evidence.md §10).
//
// Everything here is opt-in. With mesh disabled the vend values are passed
// through untouched, so monocluster behaviour is byte-for-byte unchanged.

const (
	// DefaultMeshNetwork is the shared Docker network every meshed cluster
	// joins. vCluster creates an isolated bridge per cluster by default
	// (vcluster.<name>), which leaves node InternalIPs mutually unreachable and
	// breaks the Cilium ClusterMesh prerequisite that nodes can reach each
	// other.
	DefaultMeshNetwork = "shoulders-mesh"

	// DefaultMeshPodCIDRBase is the SECOND octet from which per-cluster pod
	// CIDRs are allocated. Each cluster gets a distinct /16 of the form
	// 10.<base+n>.0.0/16.
	//
	// The second octet is the only one that can vary in a /16: a /16 network
	// address has its last two octets zero, so 10.244.231.0/16 is not a valid
	// network and silently normalises to 10.244.0.0/16.
	//
	// The base must leave room for slots, hence 16 rather than the vCluster
	// default of 244 (which would only leave 12 usable /16s).
	//
	// vCluster's chart default is a single fixed 10.244.0.0/16 for every
	// cluster (chart/values.yaml:1169, applied as --cluster-cidr in
	// pkg/k8s/k8s.go:182). That collision was measured to break cross-cluster
	// datapath traffic even though the mesh control plane connects fine.
	DefaultMeshPodCIDRBase = 16

	// maxSecondOctet is the upper bound for the second octet of a /16.
	maxSecondOctet = 255
)

// MeshConfig configures inter-cluster networking for vend clusters.
//
// NOTE: enabling the mesh shares one Docker network across clusters, which
// means the host port mappings in 1-cluster/vind-config*.yaml (80/443) will
// collide if more than one meshed cluster is created. Resolve target-doc R3
// (per-cluster host ports) before enabling this in normal use.
type MeshConfig struct {
	// Enabled turns on shared-network and per-cluster pod CIDR allocation.
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	// Network is the shared Docker network name. Defaults to
	// DefaultMeshNetwork.
	Network string `yaml:"network,omitempty" json:"network,omitempty"`

	// PodCIDRBase is the second octet used to allocate each cluster a distinct
	// /16 pod CIDR. Defaults to DefaultMeshPodCIDRBase.
	PodCIDRBase int `yaml:"podCIDRBase,omitempty" json:"podCIDRBase,omitempty"`

	// Encryption enables transparent inter-node encryption on the mesh
	// datapath. The only supported value is "wireguard"; empty leaves traffic
	// unencrypted. See docs/multi-cluster-target.md §9.
	Encryption string `yaml:"encryption,omitempty" json:"encryption,omitempty"`
}

func (cfg *Config) Mesh() MeshConfig {
	if cfg == nil {
		return MeshConfig{}
	}
	return cfg.Platform.Mesh
}

func (cfg *Config) MeshEnabled() bool {
	return cfg.Mesh().Enabled
}

func (cfg *Config) MeshNetwork() string {
	if network := strings.TrimSpace(cfg.Mesh().Network); network != "" {
		return network
	}
	return DefaultMeshNetwork
}

func (cfg *Config) MeshPodCIDRBase() int {
	base := cfg.Mesh().PodCIDRBase
	if base == 0 {
		return DefaultMeshPodCIDRBase
	}
	return base
}

// MeshEncryption returns the mesh datapath encryption mode. The empty string
// means encryption is off; "wireguard" is the only supported value.
func (cfg *Config) MeshEncryption() string {
	return strings.ToLower(strings.TrimSpace(cfg.Mesh().Encryption))
}

// ClusterPodCIDR returns a stable /16 pod CIDR for a cluster name.
//
// Deterministic so that recreating a cluster reclaims the same range, and
// derived from the name so no cluster registry is needed. Collisions are
// possible in principle (240 slots from the default base of 16); detect with
// PodCIDRForNames when the set of clusters is known.
func (cfg *Config) ClusterPodCIDR(clusterName string) string {
	return PodCIDRFor(clusterName, cfg.MeshPodCIDRBase())
}

// PodCIDRFor allocates a deterministic /16 pod CIDR from base for clusterName.
//
//	PodCIDRFor("shoulders", 16) -> "10.<base+n>.0.0/16"
//
// The result is always a valid /16 network address (last two octets zero).
// Only the second octet varies: "10.244.231.0/16" is not a network address and
// silently normalises to 10.244.0.0/16, which would give every cluster the same
// range. An earlier revision varied the third octet and reproduced exactly that
// failure live; ValidatePodCIDR now rejects non-network addresses so it cannot
// come back unnoticed.
func PodCIDRFor(clusterName string, base int) string {
	slots := podCIDRSlots(base)
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(clusterName))
	offset := int(hash.Sum32() % uint32(slots))
	return fmt.Sprintf("10.%d.0.0/16", base+offset)
}

// podCIDRSlots is how many distinct /16s are available from base.
func podCIDRSlots(base int) int {
	slots := maxSecondOctet - base + 1
	if slots < 1 {
		return 1
	}
	return slots
}

// PodCIDRForNames allocates CIDRs for a known set of cluster names and reports
// any collision, rather than silently handing two clusters the same range.
func PodCIDRForNames(names []string, base int) (map[string]string, error) {
	assigned := make(map[string]string, len(names))
	owner := make(map[string]string, len(names))
	for _, name := range names {
		cidr := PodCIDRFor(name, base)
		if existing, taken := owner[cidr]; taken {
			return nil, fmt.Errorf("clusters %q and %q both allocate pod CIDR %s; choose a different platform.mesh.podCIDRBase", existing, name, cidr)
		}
		owner[cidr] = name
		assigned[name] = cidr
	}
	return assigned, nil
}

// MeshClusterID derives a stable Cilium ClusterMesh cluster id (1-255) from a
// cluster name, so a recreated cluster keeps its identity in the mesh.
func MeshClusterID(clusterName string) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(clusterName))
	// +1 maps the hash into 1..255, the range Cilium accepts.
	return int(hash.Sum32()%255) + 1
}

// ValidatePodCIDR checks that a CIDR is a valid, routable /16 network address
// inside RFC1918 space.
//
// The network-address check matters: net.ParseCIDR accepts "10.244.231.0/16"
// and silently masks it to 10.244.0.0/16. Two clusters given such ranges would
// receive the SAME network and collide — the exact failure this allocator
// exists to prevent. Requiring the trailing octets to be zero catches that.
func ValidatePodCIDR(cidr string) error {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("invalid pod CIDR %q: %w", cidr, err)
	}
	ones, bits := ipnet.Mask.Size()
	if ones != 16 || bits != 32 {
		return fmt.Errorf("pod CIDR %q must be a /16, got /%d", cidr, ones)
	}
	if !ip.IsPrivate() {
		return fmt.Errorf("pod CIDR %q is not inside RFC1918 private space", cidr)
	}
	if !ip.Equal(ipnet.IP) {
		return fmt.Errorf("pod CIDR %q is not a network address; it normalises to %s. "+
			"a /16 must have its last two octets set to zero", cidr, ipnet.String())
	}
	return nil
}
