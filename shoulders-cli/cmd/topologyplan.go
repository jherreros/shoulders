package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
)

// topologyPlan carries the cross-cluster facts one cluster's install needs.
//
// The hub is the topology's IAM provider: there is exactly one OIDC issuer, and
// it is the hub's. A spoke's Headlamp therefore authenticates against the hub's
// Dex, which means three things have to be true for it, and all three are
// established here rather than in each cluster's install:
//
//   - the spoke's Headlamp points at the *hub's* issuer,
//   - it trusts the *hub's* Dex CA,
//   - the issuer is reachable from the spoke.
//
// The reachability is the interesting one. It was measured, not assumed: from a
// spoke pod, the hub's Dex answers on the hub's shared-network address, over
// the mesh, with the hostname its certificate already carries. So the spoke
// needs one DNS entry pointing at that address, and the certificate and CA the
// hub already has.
//
// None of that is secret distribution. A CA certificate is public material — it
// is what a certificate authority publishes — and the hub's Dex serving
// certificate needs no copying at all, because the spoke talks to the hub's
// address under the hub's own name.
type topologyPlan struct {
	meshPlan

	// hub is the topology's IAM provider.
	hub *config.ResolvedCluster

	// hubTLS is the Dex TLS material, generated once for the topology.
	//
	// It has to be shared: the hub's Dex serves a certificate for the hub's dex
	// host, signed by this CA, and a spoke's Headlamp has to trust that same
	// CA. Generating a fresh CA per cluster would leave the spoke unable to
	// verify the hub — which is the whole of the problem the CA copy solves.
	hubTLS bootstrap.DexTLSMaterial
}

// hubContext returns the plan's hub cluster, if there is one.
func (p *topologyPlan) hubContext() *config.ResolvedCluster {
	if p == nil {
		return nil
	}
	return p.hub
}

// publicConfigFor returns the public host configuration for one cluster.
//
// For the hub it is the hub's own. For a spoke it is the hub's issuer and CA —
// because the hub is where the OIDC provider lives — with the spoke's own
// Headlamp host.
func (p *topologyPlan) publicConfigFor(cluster *config.ResolvedCluster) bootstrap.PublicDomainConfig {
	config := bootstrap.PublicDomainConfig{
		DexHost:          cluster.DexHost(),
		GrafanaHost:      cluster.GrafanaHost(),
		HeadlampHost:     cluster.HeadlampHost(),
		ReporterHost:     cluster.ReporterHost(),
		PrometheusHost:   cluster.PrometheusHost(),
		AlertmanagerHost: cluster.AlertmanagerHost(),
		HubbleHost:       cluster.HubbleHost(),
	}

	config.WorkloadDomain = workloadDomainFor(cluster, p.hubContext())
	config.ClusterName = cluster.Name

	hub := p.hubContext()
	if hub == nil || cluster == hub {
		// The hub is its own issuer. Its Headlamp callback is the plain host:
		// the gateway is published on the default port.
		config.TLS = p.hubTLS
		config.HeadlampCallbackURL = fmt.Sprintf("http://%s/oidc-callback", config.HeadlampHost)
		config.HeadlampClientID = defaultHeadlampClientID
		config.HeadlampClientSecret = defaultHeadlampClientSecret
		return config
	}

	// A spoke authenticates against the hub's issuer.
	config.DexHost = hub.DexHost()
	// The spoke's telemetry goes to the *hub's* Prometheus. A spoke has no
	// backend of its own, so this host is an endpoint, not a route the spoke
	// serves.
	config.PrometheusHost = hub.PrometheusHost()
	config.TLS = p.hubTLS
	config.HeadlampHost = spokeHeadlampHost(hub, cluster)
	config.HeadlampClientID = headlampClientID(cluster)
	config.HeadlampClientSecret = headlampClientSecret(cluster)

	// The spoke's gateway is published on its own host ports, so its callback
	// carries the port. The hub's does not, which is why the whole URL is
	// carried rather than assembled from a host and a port here.
	port := cluster.Ports.HTTP
	if port == 80 {
		config.HeadlampCallbackURL = fmt.Sprintf("http://%s/oidc-callback", config.HeadlampHost)
	} else {
		config.HeadlampCallbackURL = fmt.Sprintf("http://%s:%d/oidc-callback", config.HeadlampHost, port)
	}
	return config
}

// The hub's own Headlamp client. It is the one the repository has always
// declared, so a monocluster and a hub are unchanged by any of this.
const (
	defaultHeadlampClientID     = "kubernetes"
	defaultHeadlampClientSecret = "headlamp-dex-secret"
)

// headlampClientID is the Dex client id a spoke's Headlamp authenticates with.
//
// Dex matches redirect URIs exactly, so each spoke needs its own client. The id
// is derived from the cluster name so it is stable across installs — the client
// is registered with the hub on every run, and a changing id would leave a
// stream of stale clients behind.
func headlampClientID(cluster *config.ResolvedCluster) string {
	return "headlamp-" + cluster.Name
}

// headlampClientSecret is the shared secret for a spoke's Headlamp client.
//
// It is derived rather than random so that it is stable across installs: the
// client is registered with the hub from the spoke's configuration, and a
// random value would have to be persisted to be reused.
//
// This is not a secret in the usual sense. The client is a public one — PKCE
// binds the authorization code to the challenge, so the secret is not what
// protects the flow — and it authenticates nothing on its own: it is only ever
// accepted by the hub's Dex for one spoke's callback URI.
func headlampClientSecret(cluster *config.ResolvedCluster) string {
	sum := sha256.Sum256([]byte("shoulders-headlamp-client-" + cluster.Name))
	return "sh-" + hex.EncodeToString(sum[:16])
}

// spokeHeadlampHost is the hostname a spoke's Headlamp is served under.
//
// It is qualified by the cluster name because the topology's domain belongs to
// the hub, and the hub's Headlamp already answers at the unqualified name. The
// "headlamp-" prefix is also what the hub's Dex wildcard redirect URI matches.
func spokeHeadlampHost(hub *config.ResolvedCluster, spoke *config.ResolvedCluster) string {
	domain := strings.TrimSpace(hub.Domain)
	if domain == "" {
		// Without a domain the hub's Headlamp is at headlamp.localhost, and a
		// spoke cannot be told apart from it by name alone.
		return "headlamp-" + spoke.Name + ".localhost"
	}
	return "headlamp-" + spoke.Name + "." + domain
}

// hubDNSEntries are the names a spoke must resolve to reach the hub's issuer.
//
// The hub's Dex is published on its gateway, which is reachable from a spoke at
// the hub's shared-network address. The name has to resolve to that address
// rather than to the host's loopback, because a pod's 127.0.0.1 is its own.
func (p *topologyPlan) hubDNSEntries() map[string]string {
	hub := p.hubContext()
	if hub == nil {
		return nil
	}
	address := p.addresses[hub.Name]
	if address == "" {
		return nil
	}
	host := address
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		host = host[:idx]
	}
	return map[string]string{
		hub.DexHost():        host,
		hub.PrometheusHost(): host,
	}
}

// newTopologyPlan builds the plan for a set of clusters.
//
// The Dex TLS material is generated once, for the hub's dex host, and shared by
// every cluster: the hub's Dex serves a certificate for that host signed by this
// CA, and a spoke's Headlamp has to trust that same CA to verify the issuer it
// is redirected to.
func newTopologyPlan(clusters []*config.ResolvedCluster) (*topologyPlan, error) {
	plan := &topologyPlan{meshPlan: meshPlan{clusters: clusters}}
	for _, cluster := range clusters {
		if cluster.IsHub() {
			plan.hub = cluster
			break
		}
	}
	if plan.hub == nil {
		// A single cluster, or a topology without a hub: the cluster is its own
		// issuer and keeps the material it would have had anyway.
		material, err := dexTLSMaterialFor(clusters)
		if err != nil {
			return nil, err
		}
		plan.hubTLS = material
		return plan, nil
	}

	material, err := bootstrap.GenerateDexTLSMaterial(plan.hub.DexHost())
	if err != nil {
		return nil, fmt.Errorf("prepare the hub's dex tls material: %w", err)
	}
	plan.hubTLS = material
	return plan, nil
}

// dexTLSMaterialFor returns the Dex TLS material for a cluster with no hub.
func dexTLSMaterialFor(clusters []*config.ResolvedCluster) (bootstrap.DexTLSMaterial, error) {
	if len(clusters) == 0 {
		return bootstrap.DexTLSMaterial{}, nil
	}
	cluster := clusters[0]
	if cluster.HasCustomDomain() {
		return bootstrap.GenerateDexTLSMaterial(cluster.DexHost())
	}
	return bootstrap.DefaultDexTLSMaterial()
}

// workloadDomainFor is the domain a cluster's user workloads are served under.
//
// A spoke's workloads live under the spoke's own name inside the hub's domain,
// so the hub's domain stays the topology's single domain and two spokes still
// get different hostnames: team-a.spokea.<domain> and team-a.spokeb.<domain>.
func workloadDomainFor(cluster *config.ResolvedCluster, hub *config.ResolvedCluster) string {
	if hub != nil && cluster != hub && hub.Domain != "" {
		return cluster.Name + "." + hub.Domain
	}
	if cluster.Domain != "" {
		return cluster.Domain
	}
	// Without a domain the platform's defaults are hostnames, not a domain, so
	// there is no base to qualify a workload host with. "localhost" keeps the
	// value well formed and resolvable on the machine running the cluster.
	return "localhost"
}
