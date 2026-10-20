package cmd

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/spf13/cobra"

	corev1 "k8s.io/api/core/v1"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"github.com/jherreros/shoulders/shoulders-cli/internal/tui"
)

const (
	// meshDomain is the DNS suffix ClusterMesh API servers are published under.
	// Peers must be dialled by name, not IP: the generated certificates carry
	// DNS SANs only (docs/multi-cluster-evidence.md §4.3).
	meshDomain = "mesh.cilium.io"

	// meshPort is the NodePort vCluster assigns to clustermesh-apiserver.
	meshPort = 32379
)

// meshPlan carries the cross-cluster state a topology install needs.
//
// ClusterMesh has ordering requirements that a plain loop cannot express:
//
//  1. Every cluster must EXIST before any Cilium is installed. A peer's API
//     endpoint resolves by name to its shared-network address, which only
//     exists once the peer container is up.
//  2. CoreDNS in every cluster must then carry those peer entries.
//  3. Only then can Cilium be installed with peer configuration, and the trust
//     anchor from the first cluster must be seeded into the rest BEFORE their
//     install, because the helm certificate method generates a fresh CA
//     otherwise.
//
// The hub is installed first so that it becomes the trust anchor.
type meshPlan struct {
	clusters  []*config.ResolvedCluster
	addresses map[string]string // cluster name -> "<ip>:<port>"
	ca        *corev1.Secret
}

// peers returns the ClusterMesh peers for one cluster, excluding itself and any
// cluster that is not meshed.
//
// Peers are addressed by DNS name with the port carried separately, which is the
// form that was measured to work (evidence doc §7.2). The name is not cosmetic:
// the generated server certificates carry DNS SANs and no IP SANs, so a peer
// addressed by IP would fail TLS verification even when it is reachable.
//
// The names resolve through the CoreDNS entries seeded in prepareMesh, which is
// why addresses are needed at all — they populate that mapping, they do not
// appear in the Cilium values.
func (p *meshPlan) peers(cluster *config.ResolvedCluster) map[string]string {
	peers := make(map[string]string, len(p.clusters))
	for _, peer := range p.clusters {
		if peer.Name == cluster.Name || !peer.MeshEnabled() {
			continue
		}
		peers[peer.Name] = fmt.Sprintf("%s.%s", peer.Name, meshDomain)
	}
	return peers
}

// optionsFor builds the Cilium ClusterMesh options for one cluster.
//
// The defaults are all measured requirements, not preferences; see the
// ClusterMeshOptions doc comment and docs/multi-cluster-evidence.md §4.
func (p *meshPlan) optionsFor(cluster *config.ResolvedCluster) *bootstrap.ClusterMeshOptions {
	if !cluster.MeshEnabled() {
		return nil
	}
	return &bootstrap.ClusterMeshOptions{
		Enabled:       true,
		ClusterName:   cluster.Name,
		ClusterID:     cluster.ClusterID(),
		Peers:         p.peers(cluster),
		Domain:        meshDomain,
		APIServerPort: meshPort,
		ServiceType:   "NodePort",
		TLSAuthMode:   "legacy",
		TLSCertMethod: "helm",
		ExtraDNSNames: []string{cluster.Name + "." + meshDomain},
		Encryption:    cluster.MeshEncryption(),
	}
}

// runUp installs the topology.
//
// A single-cluster config, or an explicit --cluster, runs exactly one install.
// Otherwise a topology runs one install per cluster, staged as described on
// meshPlan.
func runUp(cmd *cobra.Command, _ []string) error {
	if notice := setOverridesNotice(); notice != "" {
		fmt.Println(notice)
	}
	if upLocal && upBundle != "" {
		return fmt.Errorf("cannot combine --local and --bundle")
	}
	if upBundle != "" && targetCluster.ProviderOrDefault() == config.ProviderExisting {
		return fmt.Errorf("--bundle on provider existing requires pre-mirrored images; automatic image import only supports vind")
	}

	topology, err := currentConfig.Topology()
	if err != nil {
		return err
	}
	clusters, err := clustersToInstall(cmd, topology)
	if err != nil {
		return err
	}

	meshed := false
	for _, cluster := range clusters {
		if cluster.MeshEnabled() {
			meshed = true
		}
	}
	if meshed && len(clusters) > 1 {
		if err := validateMeshedTopology(clusters); err != nil {
			return err
		}
	}

	plan, err := newTopologyPlan(clusters)
	if err != nil {
		return err
	}
	if meshed && len(clusters) > 1 {
		if err := prepareMesh(ctx(cmd), clusters, plan); err != nil {
			return err
		}
	}

	for i, cluster := range clusters {
		if len(clusters) > 1 {
			fmt.Printf("\n=== cluster %d/%d: %s (%s) ===\n", i+1, len(clusters), cluster.Name, cluster.Role)
		}

		// A spoke's Headlamp authenticates against the hub's Dex, so the hub
		// has to know the spoke's client before the spoke comes up. The
		// registration patches the hub's dex HelmRelease, so it runs against
		// the hub's context.
		if cluster.Tier() == config.TierWorkload {
			if err := registerSpokeWithHub(ctx(cmd), plan, cluster); err != nil {
				return err
			}
		}

		selectCluster(cluster)

		tracker := tui.NewPhaseTracker(upPhases(), upVerbose)
		installErr := upOneCluster(cmd, cluster, tracker, plan)
		tracker.Stop()
		if installErr != nil {
			return installErr
		}

		if meshed && plan.ca == nil {
			ca, found, caErr := bootstrap.CiliumCA(ctx(cmd), kubeconfig)
			if caErr != nil {
				return fmt.Errorf("read cilium trust anchor from %q: %w", cluster.Name, caErr)
			}
			if found {
				plan.ca = ca
			}
		}
	}
	return nil
}

func ctx(cmd *cobra.Command) context.Context { return cmd.Context() }

// clustersToInstall decides which clusters this run touches.
//
//	--cluster or --name given  -> exactly that cluster
//	topology with >1 cluster    -> every cluster, hub first
//	otherwise                   -> the default cluster
//
// The explicit flags matter more than they look: in a topology, a bare `up` is a
// bulk operation, so without them there would be no way to install or repair a
// single cluster.
func clustersToInstall(cmd *cobra.Command, topology *config.Topology) ([]*config.ResolvedCluster, error) {
	named := cmd.Flags().Changed("name") || cmd.Flags().Changed("cluster")
	if !named {
		if len(topology.Clusters) <= 1 {
			return []*config.ResolvedCluster{targetCluster}, nil
		}
		return topology.AllClusters(), nil
	}

	name := targetClusterName
	if !cmd.Flags().Changed("cluster") && cmd.Flags().Changed("name") {
		name = upClusterName
	}
	for _, cluster := range topology.AllClusters() {
		if cluster.Name == name {
			return []*config.ResolvedCluster{cluster}, nil
		}
	}
	return nil, fmt.Errorf("no cluster named %q in the topology; known clusters: %s",
		name, strings.Join(topology.ClusterNames(), ", "))
}

// selectCluster points the process at a cluster: the context override that the
// kube helpers and the rest of cmd/ consult.
func selectCluster(cluster *config.ResolvedCluster) {
	targetCluster = cluster
	if kubeconfig == "" && cluster.Kubeconfig != "" {
		kubeconfig = cluster.Kubeconfig
	}
	kube.SetContextOverride(targetClusterContextName())
}

// targetClusterContextName is the kubeconfig context of the currently selected
// cluster.
//
// It is the single answer used everywhere a cluster must be named: the kube
// helpers, the DNS work, and — critically — Helm, which ignores the package's
// context override and reads the ambient current-context instead. Deriving it
// in one place is what keeps those two mechanisms from disagreeing.
func targetClusterContextName() string {
	if targetCluster == nil {
		return ""
	}
	if targetCluster.Context != "" {
		return targetCluster.Context
	}
	if targetCluster.ProviderOrDefault() == config.ProviderVind {
		return bootstrap.ContextPrefix + targetCluster.Name
	}
	return ""
}

// prepareMesh performs the stages that must precede any Cilium install:
// create every cluster, resolve addresses, then seed peer DNS everywhere.
func prepareMesh(c context.Context, clusters []*config.ResolvedCluster, plan *topologyPlan) error {
	// Stage 1: every cluster must exist before any address can be resolved.
	for _, cluster := range clusters {
		selectCluster(cluster)
		if err := ensureClusterExists(c, cluster, plan); err != nil {
			return err
		}
	}

	// Stage 2: resolve each cluster's shared-network address.
	plan.addresses = make(map[string]string, len(clusters))
	for _, cluster := range clusters {
		address, err := bootstrap.ClusterMeshAddress(c, cluster.Name, meshPort)
		if err != nil {
			return fmt.Errorf("resolve mesh address for %q: %w", cluster.Name, err)
		}
		plan.addresses[cluster.Name] = address
	}

	// Stage 3: every cluster must resolve every peer by name, and a spoke must
	// also resolve the hub's issuer to the hub's mesh address.
	//
	// This runs BEFORE Cilium is installed, which is deliberate: peers are
	// dialled by name, and CoreDNS cannot come up until the CNI exists, so the
	// entries have to be in the Corefile before the first CoreDNS pod starts.
	for _, cluster := range clusters {
		selectCluster(cluster)
		var entries []string
		for name, address := range plan.addresses {
			if name == cluster.Name {
				continue
			}
			host, _, _ := net.SplitHostPort(address)
			entries = append(entries, fmt.Sprintf("%s %s.%s", host, name, meshDomain))
		}
		// The hub's issuer, at the hub's mesh address. A spoke's Headlamp is
		// redirected to the hub's Dex, so the name has to resolve to something
		// the spoke can reach — the host's loopback is not that, because a pod's
		// 127.0.0.1 is its own.
		for host, address := range plan.hubDNSEntries() {
			entries = append(entries, fmt.Sprintf("%s %s", address, host))
		}
		if len(entries) == 0 {
			continue
		}
		if _, err := bootstrap.SeedCoreDNSHosts(c, kubeconfig, entries); err != nil {
			return fmt.Errorf("seed mesh DNS in %q: %w", cluster.Name, err)
		}
	}
	return nil
}

// seedSharedCA propagates the trust anchor so every cluster signs its
// certificates from the same root. It must run before that cluster's Cilium
// install.
func seedSharedCA(c context.Context, cluster *config.ResolvedCluster, plan *topologyPlan) error {
	if plan == nil || plan.ca == nil {
		return nil
	}
	seeded, err := bootstrap.SeedCiliumCA(c, kubeconfig, plan.ca, false)
	if err != nil {
		return fmt.Errorf("seed cilium trust anchor into %q: %w", cluster.Name, err)
	}
	if seeded {
		fmt.Printf("Seeded the shared Cilium trust anchor into %s\n", cluster.Name)
	}
	return nil
}

// validateMeshedTopology rejects combinations that cannot form a working mesh,
// before anything is created.
func validateMeshedTopology(clusters []*config.ResolvedCluster) error {
	var meshed []*config.ResolvedCluster
	for _, cluster := range clusters {
		if cluster.MeshEnabled() {
			meshed = append(meshed, cluster)
		}
	}
	if len(meshed) < 2 {
		return fmt.Errorf("a cluster mesh needs at least two clusters with platform.mesh.enabled, found %d", len(meshed))
	}

	networks := map[string]bool{}
	ids := map[int]string{}
	var meshEncryption, encryptionOwner string
	encryptionSet := false
	for _, cluster := range meshed {
		networks[cluster.MeshNetwork()] = true
		if other, clash := ids[cluster.ClusterID()]; clash {
			return fmt.Errorf("clusters %q and %q both resolve to mesh cluster id %d; ids must be unique in a mesh "+
				"(rename one, or give it a distinct name)", other, cluster.Name, cluster.ClusterID())
		}
		ids[cluster.ClusterID()] = cluster.Name
		switch encryption := cluster.MeshEncryption(); encryption {
		case "", "wireguard":
			if !encryptionSet {
				meshEncryption, encryptionOwner, encryptionSet = encryption, cluster.Name, true
			} else if encryption != meshEncryption {
				return fmt.Errorf("cluster %q has mesh encryption %q but %q has %q; "+
					"encryption is a whole-mesh property", cluster.Name, encryption, encryptionOwner, meshEncryption)
			}
		default:
			return fmt.Errorf("cluster %q has unsupported platform.mesh.encryption %q; supported values: wireguard",
				cluster.Name, encryption)
		}
	}
	if len(networks) > 1 {
		return fmt.Errorf("all meshed clusters must share one Docker network, found %d distinct values", len(networks))
	}
	return nil
}

// registerSpokeWithHub registers a spoke's Headlamp with the hub's Dex.
//
// The client id and secret are derived from the spoke's name, so the
// registration is idempotent: re-running `up` replaces the same client rather
// than adding another.
func registerSpokeWithHub(ctx context.Context, plan *topologyPlan, spoke *config.ResolvedCluster) error {
	hub := plan.hubContext()
	if hub == nil {
		return nil
	}
	publicConfig := plan.publicConfigFor(spoke)

	// The registration patches the hub's dex HelmRelease, so it has to run
	// against the hub.
	previous := targetCluster
	selectCluster(hub)
	defer selectCluster(previous)

	if err := bootstrap.RegisterHeadlampClient(ctx, kubeconfig,
		headlampClientID(spoke),
		fmt.Sprintf("Headlamp (%s)", spoke.Name),
		headlampClientSecret(spoke),
		publicConfig.HeadlampCallbackURL,
	); err != nil {
		return fmt.Errorf("register %q with the hub's identity provider: %w", spoke.Name, err)
	}
	fmt.Printf("Registered %s with the hub's identity provider\n", spoke.Name)
	return nil
}
