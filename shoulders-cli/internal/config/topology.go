package config

import (
	"fmt"
	"sort"
	"strings"
)

// Roles.
//
// Roles are LABELS, not behaviour. Nothing in the codebase branches on a role to
// decide what a cluster gets: who owns host ports, who enables the mesh, which
// profile a cluster runs are all explicit configuration. This is decision D1 in
// docs/multi-cluster-target.md and is the direct answer to the concern that
// supporting monocluster and multi-cluster would double the branching. Role is
// the seam where hub/spoke policy can eventually live, without needing a second
// configuration mechanism.
const (
	RoleHub   = "hub"
	RoleSpoke = "spoke"
	RoleEdge  = "edge"
)

func normaliseRole(role string) string {
	return strings.ToLower(strings.TrimSpace(role))
}

func isKnownRole(role string) bool {
	switch normaliseRole(role) {
	case RoleHub, RoleSpoke, RoleEdge:
		return true
	default:
		return false
	}
}

// PortMapping is a cluster's host port bindings.
//
// Ports are per-cluster and explicit because roles are labels: nothing infers
// "the hub gets 80/443". Only one vind cluster can bind a given host port, so a
// topology needs a distinct mapping per cluster (target-doc risk R3).
type PortMapping struct {
	HTTP  int `yaml:"http,omitempty" json:"http,omitempty"`
	HTTPS int `yaml:"https,omitempty" json:"https,omitempty"`
}

func (p PortMapping) WithDefaults() PortMapping {
	if p.HTTP == 0 {
		p.HTTP = 80
	}
	if p.HTTPS == 0 {
		p.HTTPS = 443
	}
	return p
}

func (p PortMapping) Validate(clusterName string) error {
	for label, port := range map[string]int{"http": p.HTTP, "https": p.HTTPS} {
		if port < 1 || port > 65535 {
			return fmt.Errorf("cluster %q: %s port %d is out of range", clusterName, label, port)
		}
	}
	if p.HTTP == p.HTTPS {
		return fmt.Errorf("cluster %q: http and https ports must differ (both %d)", clusterName, p.HTTP)
	}
	return nil
}

// ClusterEntry is one cluster in a topology, as written in the config file.
//
// Every field except Name and Role is an OPTIONAL OVERRIDE of the shared
// `platform:` block (decision: overlay on shared platform). An entry therefore
// only states what differs, and resolution produces a fully-populated
// ResolvedCluster.
//
// A zero value means "not set, inherit from platform". The consequence is that
// an override cannot turn a platform-level true back to false for the plain
// bool fields; use the pointer fields (Cilium.Enabled, Flux.OCIRepository.Insecure)
// where that distinction matters.
type ClusterEntry struct {
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	Role string `yaml:"role,omitempty" json:"role,omitempty"`

	// Cluster connection identity. Provider/Kubeconfig/Context may also come
	// from the legacy top-level `cluster:` block when migrating.
	Provider   string `yaml:"provider,omitempty" json:"provider,omitempty"`
	Kubeconfig string `yaml:"kubeconfig,omitempty" json:"kubeconfig,omitempty"`
	Context    string `yaml:"context,omitempty" json:"context,omitempty"`

	Ports *PortMapping `yaml:"ports,omitempty" json:"ports,omitempty"`

	// Platform overrides.
	Profile string        `yaml:"profile,omitempty" json:"profile,omitempty"`
	Domain  string        `yaml:"domain,omitempty" json:"domain,omitempty"`
	Cilium  *CiliumConfig `yaml:"cilium,omitempty" json:"cilium,omitempty"`
	Flux    *FluxConfig   `yaml:"flux,omitempty" json:"flux,omitempty"`
	Mesh    *MeshConfig   `yaml:"mesh,omitempty" json:"mesh,omitempty"`
}

// Topology is the resolved list of clusters.
type Topology struct {
	Clusters []ResolvedCluster
}

func (t *Topology) Names() []string {
	names := make([]string, 0, len(t.Clusters))
	for _, cluster := range t.Clusters {
		names = append(names, cluster.Name)
	}
	sort.Strings(names)
	return names
}

func (t *Topology) Get(name string) (*ResolvedCluster, bool) {
	for i := range t.Clusters {
		if t.Clusters[i].Name == name {
			return &t.Clusters[i], true
		}
	}
	return nil, false
}

// Hubs returns the clusters labelled as hubs.
func (t *Topology) Hubs() []ResolvedCluster {
	var hubs []ResolvedCluster
	for _, cluster := range t.Clusters {
		if normaliseRole(cluster.Role) == RoleHub {
			hubs = append(hubs, cluster)
		}
	}
	return hubs
}

// ResolvedCluster is a cluster with every inherited value already applied.
//
// PlatformConfig is embedded so existing accessors keep working: a resolved
// cluster's Profile, CiliumEnabled, FluxRepositoryURL etc. are promoted methods.
// Consumers therefore never deal with overlay rules; they read a flat value.
type ResolvedCluster struct {
	PlatformConfig

	Name       string      `yaml:"name,omitempty" json:"name,omitempty"`
	Role       string      `yaml:"role,omitempty" json:"role,omitempty"`
	Provider   string      `yaml:"provider,omitempty" json:"provider,omitempty"`
	Kubeconfig string      `yaml:"kubeconfig,omitempty" json:"kubeconfig,omitempty"`
	Context    string      `yaml:"context,omitempty" json:"context,omitempty"`
	Ports      PortMapping `yaml:"ports,omitempty" json:"ports,omitempty"`

	// FluxPath is this cluster's prefix for the manifests flux reconciles.
	//
	// It is resolved per cluster rather than taken from the shared platform
	// block, because the manifests a cluster reconciles depend on its tier: the
	// hub reconciles the platform tier, a spoke the workload tier. An empty
	// value means the shared tree, which is what a single cluster uses.
	FluxPath string `yaml:"-" json:"-"`
}

// ClusterTier selects which set of addons a cluster reconciles.
type ClusterTier string

const (
	// TierPlatform is the hub's tier: IAM (Dex), observability, and policy.
	// It is the topology's public surface and the only place the OIDC issuer
	// exists.
	TierPlatform ClusterTier = "platform"

	// TierWorkload is the spoke's tier: the workload compositions and their
	// local stateful dependencies (Crossplane, CNPG, Garage), plus the cluster's
	// own Cilium, Flux and Headlamp.
	TierWorkload ClusterTier = "workload"
)

// Tier reports which set of addons this cluster reconciles.
//
// This is the one thing the role does determine. Everything else — host ports,
// mesh enablement, profile, domain — stays explicit configuration, because a
// role that silently implies behaviour is how two clusters end up configured
// differently from what their config says.
//
// The distinction matters beyond tidiness: the platform tier owns the OIDC
// issuer, so two clusters running it would publish the same issuer URL with
// different signing keys.
func (c *ResolvedCluster) Tier() ClusterTier {
	if c == nil {
		return TierPlatform
	}
	if normaliseRole(c.Role) == RoleHub {
		return TierPlatform
	}
	return TierWorkload
}

// IsHub is a convenience for readability at call sites. It must never be the
// reason behaviour differs between clusters.
func (c *ResolvedCluster) IsHub() bool { return normaliseRole(c.Role) == RoleHub }

// ProviderOrDefault returns the cluster provider, defaulting to vind the same
// way Config.Provider does. Provider is a promoted field, so this cannot be a
// method named Provider.
func (c *ResolvedCluster) ProviderOrDefault() string {
	if c == nil || c.Provider == "" {
		return ProviderVind
	}
	return c.Provider
}

// bindsHostPorts reports whether the CLI will create this cluster and therefore
// bind its host ports. Only vind clusters are created by the CLI.
func (c *ResolvedCluster) bindsHostPorts() bool {
	provider := c.Provider
	if provider == "" {
		provider = ProviderVind
	}
	return provider == ProviderVind
}

// view returns a Config facade over this resolved cluster.
//
// Profile and Domain are promoted FIELDS from the embedded PlatformConfig, so
// they cannot also be methods here. Every other accessor is delegated through
// this facade rather than reimplemented, which keeps the overlay rules in one
// place and leaves Config's existing API untouched for current callers.
func (c *ResolvedCluster) view() *Config {
	return &Config{
		Platform: c.PlatformConfig,
		Cluster: ClusterConfig{
			Provider:   c.Provider,
			Name:       c.Name,
			Kubeconfig: c.Kubeconfig,
			Context:    c.Context,
		},
	}
}

func (c *ResolvedCluster) ProfileSpec() ProfileSpec { return ProfileSpecFor(c.Profile) }

func (c *ResolvedCluster) CiliumEnabled() bool   { return c.view().CiliumEnabled() }
func (c *ResolvedCluster) CiliumVersion() string { return c.view().CiliumVersion() }

func (c *ResolvedCluster) FluxSource() string           { return c.view().FluxSource() }
func (c *ResolvedCluster) FluxRepositoryURL() string    { return c.view().FluxRepositoryURL() }
func (c *ResolvedCluster) FluxRepositoryBranch() string { return c.view().FluxRepositoryBranch() }
func (c *ResolvedCluster) FluxOCIURL() string           { return c.view().FluxOCIURL() }
func (c *ResolvedCluster) FluxOCITag() string           { return c.view().FluxOCITag() }
func (c *ResolvedCluster) FluxOCIInsecure() bool        { return c.view().FluxOCIInsecure() }
func (c *ResolvedCluster) FluxPathPrefix() string {
	if c == nil {
		return ""
	}
	if c.FluxPath != "" {
		return c.FluxPath
	}
	return c.view().FluxPathPrefix()
}

func (c *ResolvedCluster) HasCustomDomain() bool    { return c.Domain != "" }
func (c *ResolvedCluster) DexHost() string          { return c.view().DexHost() }
func (c *ResolvedCluster) DexURL() string           { return c.view().DexURL() }
func (c *ResolvedCluster) GrafanaHost() string      { return c.view().GrafanaHost() }
func (c *ResolvedCluster) GrafanaURL() string       { return c.view().GrafanaURL() }
func (c *ResolvedCluster) HeadlampHost() string     { return c.view().HeadlampHost() }
func (c *ResolvedCluster) HeadlampURL() string      { return c.view().HeadlampURL() }
func (c *ResolvedCluster) ReporterHost() string     { return c.view().ReporterHost() }
func (c *ResolvedCluster) ReporterURL() string      { return c.view().ReporterURL() }
func (c *ResolvedCluster) PrometheusHost() string   { return c.view().PrometheusHost() }
func (c *ResolvedCluster) AlertmanagerHost() string { return c.view().AlertmanagerHost() }
func (c *ResolvedCluster) HubbleHost() string       { return c.view().HubbleHost() }

func (c *ResolvedCluster) MeshEnabled() bool    { return c.view().MeshEnabled() }
func (c *ResolvedCluster) MeshNetwork() string  { return c.view().MeshNetwork() }
func (c *ResolvedCluster) MeshPodCIDRBase() int { return c.view().MeshPodCIDRBase() }
func (c *ResolvedCluster) MeshEncryption() string {
	return c.view().MeshEncryption()
}

// ClusterPodCIDR allocates this cluster's pod CIDR under the mesh.
func (c *ResolvedCluster) ClusterPodCIDR() string {
	return PodCIDRFor(c.Name, c.MeshPodCIDRBase())
}

// Topology resolves the configured topology, desugaring the legacy single
// `cluster:` block when `clusters:` is absent.
//
// This is decision D2: an existing ~/.shoulders/config.yaml keeps working
// unchanged and resolves to a one-cluster topology with role `hub`, which is
// exactly what monocluster is.
func (cfg *Config) Topology() (*Topology, error) {
	entries := cfg.Clusters
	desugared := false

	if len(entries) == 0 {
		entries = []ClusterEntry{{
			Name:       cfg.Cluster.Name,
			Provider:   cfg.Cluster.Provider,
			Kubeconfig: cfg.Cluster.Kubeconfig,
			Context:    cfg.Cluster.Context,
			Role:       RoleHub,
		}}
		desugared = true
	}

	// Platform defaults are applied first so an entry with no overrides
	// resolves to the same values today's code reads.
	platform := cfg.Platform
	platform.Profile = normalizeProfile(platform.Profile)
	if platform.Profile == "" {
		platform.Profile = DefaultPlatformProfile
	}
	if platform.Cilium.Version == "" {
		platform.Cilium.Version = DefaultCiliumVersion
	}

	topology := &Topology{}
	for _, entry := range entries {
		resolved, err := resolveCluster(entry, platform)
		if err != nil {
			return nil, err
		}
		if desugared {
			// Inherit the legacy cluster identity defaults that today live in
			// ClusterConfig, so behaviour is unchanged for monocluster.
			if resolved.Provider == "" {
				resolved.Provider = cfg.Cluster.Provider
			}
			if resolved.Kubeconfig == "" {
				resolved.Kubeconfig = cfg.Cluster.Kubeconfig
			}
			if resolved.Context == "" {
				resolved.Context = cfg.Cluster.Context
			}
			if resolved.Name == "" {
				resolved.Name = DefaultClusterName
			}
			if cfg.Platform.Cilium.Enabled != nil {
				enabled := *cfg.Platform.Cilium.Enabled
				resolved.Cilium.Enabled = &enabled
			}
		}
		resolved.Ports = resolved.Ports.WithDefaults()
		topology.Clusters = append(topology.Clusters, resolved)
	}

	// Resolve each cluster's flux path.
	//
	// Only an explicitly configured pathPrefix is used. D6 anticipated a
	// per-cluster subtree at clusters/<name>, but that ties the repository to
	// the cluster names the operator chose, and the names are the operator's
	// to pick. The tier difference between clusters is expressed instead as
	// delete patches on the flux Kustomizations (see bootstrap.SetClusterTier),
	// which is profile-independent and works for any cluster name.
	for i := range topology.Clusters {
		cluster := &topology.Clusters[i]
		configured := strings.TrimSpace(cluster.view().FluxPathPrefix())
		if configured == "." {
			configured = ""
		}
		cluster.FluxPath = configured
	}

	if err := topology.Validate(); err != nil {
		return nil, err
	}
	return topology, nil
}

// resolveCluster overlays one entry's set fields onto the shared platform block.
func resolveCluster(entry ClusterEntry, platform PlatformConfig) (ResolvedCluster, error) {
	resolved := ResolvedCluster{
		PlatformConfig: platform,
		Name:           strings.TrimSpace(entry.Name),
		Role:           normaliseRole(entry.Role),
		Provider:       entry.Provider,
		Kubeconfig:     entry.Kubeconfig,
		Context:        entry.Context,
	}

	if resolved.Role == "" {
		resolved.Role = RoleHub
	}

	if entry.Profile != "" {
		resolved.Profile = normalizeProfile(entry.Profile)
	}
	if entry.Domain != "" {
		resolved.Domain = entry.Domain
	}

	// Cilium: Version is a string (zero = unset); Enabled is a pointer so an
	// entry can explicitly disable it for one cluster.
	if entry.Cilium != nil {
		if entry.Cilium.Version != "" {
			resolved.Cilium.Version = entry.Cilium.Version
		}
		if entry.Cilium.Enabled != nil {
			enabled := *entry.Cilium.Enabled
			resolved.Cilium.Enabled = &enabled
		}
	}

	// Flux: merge the nested source configs field by field.
	if entry.Flux != nil {
		mergeFlux(&resolved.Flux, *entry.Flux)
	}

	// Mesh: an entry may enable or tune the mesh for its own cluster only.
	if entry.Mesh != nil {
		mergeMesh(&resolved.Mesh, *entry.Mesh)
	}

	if entry.Ports != nil {
		resolved.Ports = *entry.Ports
	}

	return resolved, nil
}

func mergeFlux(dst *FluxConfig, src FluxConfig) {
	if src.Source != "" {
		dst.Source = normalizeFluxSource(src.Source)
	}
	if src.PathPrefix != "" {
		dst.PathPrefix = src.PathPrefix
	}
	if src.GitRepository.URL != "" {
		dst.GitRepository.URL = src.GitRepository.URL
	}
	if src.GitRepository.Branch != "" {
		dst.GitRepository.Branch = src.GitRepository.Branch
	}
	if src.OCIRepository.URL != "" {
		dst.OCIRepository.URL = src.OCIRepository.URL
	}
	if src.OCIRepository.Tag != "" {
		dst.OCIRepository.Tag = src.OCIRepository.Tag
	}
	if src.OCIRepository.Insecure != nil {
		insecure := *src.OCIRepository.Insecure
		dst.OCIRepository.Insecure = &insecure
	}
}

func mergeMesh(dst *MeshConfig, src MeshConfig) {
	// Enabled is a plain bool, so an entry can turn the mesh on for itself but
	// cannot turn off a platform-level `true`. Documented on ClusterEntry.
	if src.Enabled {
		dst.Enabled = true
	}
	if src.Network != "" {
		dst.Network = src.Network
	}
	if src.PodCIDRBase != 0 {
		dst.PodCIDRBase = src.PodCIDRBase
	}
	if src.Encryption != "" {
		dst.Encryption = src.Encryption
	}
}

// Validate enforces the structural rules of a topology. These describe the shape
// of the configuration only; no rule here grants a cluster different behaviour
// because of its role.
func (t *Topology) Validate() error {
	if len(t.Clusters) == 0 {
		return fmt.Errorf("topology contains no clusters")
	}

	seenNames := map[string]bool{}
	seenPorts := map[int]string{}
	seenDomains := map[string]string{}
	hubs := 0

	for _, cluster := range t.Clusters {
		if cluster.Name == "" {
			return fmt.Errorf("topology contains a cluster with no name")
		}
		if seenNames[cluster.Name] {
			return fmt.Errorf("duplicate cluster name %q in topology", cluster.Name)
		}
		seenNames[cluster.Name] = true

		if !isKnownRole(cluster.Role) {
			return fmt.Errorf("cluster %q has unknown role %q (want one of %s, %s, %s)",
				cluster.Name, cluster.Role, RoleHub, RoleSpoke, RoleEdge)
		}
		if normaliseRole(cluster.Role) == RoleHub {
			hubs++
		}

		switch cluster.Provider {
		case "", ProviderVind, ProviderExisting:
		default:
			return fmt.Errorf("cluster %q: unsupported provider %q", cluster.Name, cluster.Provider)
		}

		switch cluster.Profile {
		case ProfileSmall, ProfileMedium, ProfileLarge:
		default:
			return fmt.Errorf("cluster %q: unsupported platform profile %q", cluster.Name, cluster.Profile)
		}

		if err := cluster.Ports.Validate(cluster.Name); err != nil {
			return err
		}

		// Two clusters must not claim the same platform hostnames.
		//
		// `platform.domain` decides them — dex.<domain>, grafana.<domain> and
		// so on — and they become the HTTPRoute hostnames. A shared domain
		// means one hostname for two clusters, of which only one is reachable,
		// and worse: two Dex instances publishing the same OIDC issuer URL with
		// different signing keys, so a token minted by one is rejected by the
		// other and the browser cannot tell them apart.
		//
		//
		// Only *explicitly declared* domains are compared. The implicit default
		// (grafana.localhost and friends) collides too, but whether that is an
		// error depends on the addon split: once the platform tier moves to the
		// hub (D3) only the hub serves these hostnames and a spoke does not
		// need a domain at all. Until then the default collision is the
		// remaining sliver of R3, and the fix is D3 rather than a rule.
		if domain := cluster.Domain; domain != "" {
			if owner, taken := seenDomains[domain]; taken {
				return fmt.Errorf("clusters %q and %q both declare platform.domain %q; "+
					"their hostnames would collide (dex.%s, grafana.%s, ...) and two "+
					"clusters would publish the same OIDC issuer URL",
					owner, cluster.Name, domain, domain, domain)
			}
			seenDomains[domain] = cluster.Name
		}
		// Only vind clusters bind host ports. `provider: existing` targets a
		// cluster the CLI did not create, so several such clusters may share the
		// same mapping without conflicting.
		if cluster.bindsHostPorts() {
			for label, port := range map[string]int{"http": cluster.Ports.HTTP, "https": cluster.Ports.HTTPS} {
				if owner, taken := seenPorts[port]; taken {
					return fmt.Errorf("clusters %q and %q both bind host %s port %d; "+
						"only one vind cluster can own a host port", owner, cluster.Name, label, port)
				}
				seenPorts[port] = cluster.Name
			}
		}
	}

	if hubs != 1 {
		return fmt.Errorf("topology must declare exactly one hub cluster, found %d", hubs)
	}

	// In a topology, the hub owns the domain.
	//
	// The platform hostnames are derived from it — dex.<domain>,
	// grafana.<domain> — and the hub is the only cluster that serves the
	// platform tier, so it is the only cluster that needs them. A spoke
	// declares no domain and has no platform hostnames at all.
	//
	// The hub must declare one, though: with no domain the hub would fall
	// back to the same defaults as a spoke, and the topology's public surface
	// would have no stable name.
	if len(t.Clusters) > 1 {
		hub := t.hub()
		if hub != nil && strings.TrimSpace(hub.Domain) == "" {
			return fmt.Errorf("the hub cluster %q must declare platform.domain in a topology; "+
				"it is the topology's public surface and its hostnames (dex.<domain>, grafana.<domain>, ...) "+
				"are derived from it. On a laptop, domain: %s.127.0.0.1.sslip.io works",
				hub.Name, hub.Name)
		}
	}
	return nil
}

// hub returns the topology's hub cluster, or nil when there is none.
func (t *Topology) hub() *ResolvedCluster {
	for i := range t.Clusters {
		if t.Clusters[i].IsHub() {
			return &t.Clusters[i]
		}
	}
	return nil
}

// ClusterID is the numeric identity this cluster uses inside a Cilium
// ClusterMesh. Cilium requires 1-255 and unique across the mesh.
//
// It is derived from the cluster name so it is stable across recreations and
// needs no cluster registry. Distinct names can collide (modulo the slot count);
// validateTopology reports that when the cluster set is known.
func (c *ResolvedCluster) ClusterID() int {
	if c == nil {
		return 0
	}
	return MeshClusterID(c.Name)
}

// AllClusters returns the topology's clusters as pointers, hub first.
//
// The hub leads because a mesh install makes it the trust anchor: its Cilium CA
// must exist before any peer's Cilium is installed.
func (t *Topology) AllClusters() []*ResolvedCluster {
	ordered := make([]*ResolvedCluster, 0, len(t.Clusters))
	for _, cluster := range t.Clusters {
		if normaliseRole(cluster.Role) == RoleHub {
			c := cluster
			ordered = append(ordered, &c)
		}
	}
	for _, cluster := range t.Clusters {
		if normaliseRole(cluster.Role) != RoleHub {
			c := cluster
			ordered = append(ordered, &c)
		}
	}
	return ordered
}

// ClusterNames lists the topology's cluster names in declared order, for error
// messages that should tell the user what they could have typed instead.
func (t *Topology) ClusterNames() []string {
	names := make([]string, 0, len(t.Clusters))
	for _, cluster := range t.Clusters {
		names = append(names, cluster.Name)
	}
	return names
}

// SetClusterContext records the kubeconfig context for a cluster in the
// topology. It errors when the cluster is not in the topology so a typo cannot
// silently create a phantom entry.
func (cfg *Config) SetClusterContext(name, contextName string) error {
	for i := range cfg.Clusters {
		if cfg.Clusters[i].Name == name {
			cfg.Clusters[i].Context = contextName
			return nil
		}
	}
	return fmt.Errorf("cluster %q is not in the topology", name)
}

// ResolveCluster looks up a single resolved cluster by name.
func (cfg *Config) ResolveCluster(name string) (*ResolvedCluster, error) {
	topology, err := cfg.Topology()
	if err != nil {
		return nil, err
	}
	cluster, ok := topology.Get(name)
	if !ok {
		return nil, fmt.Errorf("cluster %q is not in the topology (have: %s)", name, strings.Join(topology.Names(), ", "))
	}
	return cluster, nil
}

// DefaultCluster returns the cluster the CLI operates on when none is named:
// the single cluster for monocluster, the hub for a topology. This is a
// convenience default, not a role-driven behaviour switch.
func (cfg *Config) DefaultCluster() (*ResolvedCluster, error) {
	topology, err := cfg.Topology()
	if err != nil {
		return nil, err
	}
	if len(topology.Clusters) == 1 {
		cluster := topology.Clusters[0]
		return &cluster, nil
	}
	hubs := topology.Hubs()
	if len(hubs) == 1 {
		return &hubs[0], nil
	}
	return nil, fmt.Errorf("cannot pick a default cluster; name one explicitly (have: %s)", strings.Join(topology.Names(), ", "))
}
