package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"github.com/jherreros/shoulders/shoulders-cli/internal/manifests"
)

// ensureClusterExists performs phase 1 for one cluster: connect, or create a
// vind cluster with its declared ports and mesh settings.
//
// It is idempotent — EnsureVindCluster returns early when the container already
// exists — which is what lets the topology install call it once for every
// cluster before any Cilium is installed, and again per cluster later.
func ensureClusterExists(c context.Context, cluster *config.ResolvedCluster, plan *topologyPlan) error {
	if cluster.ProviderOrDefault() == config.ProviderExisting {
		return bootstrap.EnsureExistingCluster(c, kubeconfig)
	}
	clusterName := cluster.Name
	profileSpec := cluster.ProfileSpec()

	ports := cluster.Ports.WithDefaults()
	if err := bootstrap.CheckHostPortsAvailable(c, clusterName, []int{ports.HTTP, ports.HTTPS}); err != nil {
		return err
	}
	if err := bootstrap.CheckDiskHeadroom(bootstrap.ProfileDiskMinimum(profileSpec.Name)); err != nil {
		return err
	}

	// Snapshot the kubeconfig before vind rewrites it: a failed create can
	// otherwise leave it nulled.
	kubeconfigRestore, err := bootstrap.BackupKubeconfig(kubeconfig)
	if err != nil {
		return err
	}

	vindValues, err := manifests.VindValues(profileSpec.Name, *cluster)
	if err != nil {
		return fmt.Errorf("render vind values: %w", err)
	}
	authConfig, dexHost, err := clusterAuthMaterial(*cluster, plan)
	if err != nil {
		return err
	}
	if err := bootstrap.EnsureVindCluster(c, clusterName, vindValues, authConfig, dexHost); err != nil {
		if kubeconfigRestore != nil {
			if rErr := kubeconfigRestore(); rErr != nil {
				return fmt.Errorf("failed to create vind cluster: %w (kubeconfig restore also failed: %v)", err, rErr)
			}
		}
		return fmt.Errorf("failed to create vind cluster: %w", err)
	}

	// vind freezes cluster DNS state at creation; reconcile forwarding right
	// away so later phases never depend on stale upstreams.
	//
	// CoreDNS is not schedulable yet — it has no CNI — so ErrCoreDNSNotRunning
	// here is expected, not a fault: the Corefile is already patched and takes
	// effect when the first CoreDNS pod starts. Any other error is worth saying.
	if _, err := bootstrap.EnsureClusterDNS(c, kubeconfig); err != nil && !errors.Is(err, bootstrap.ErrCoreDNSNotRunning) {
		fmt.Printf("Note: cluster DNS reconcile warning for %s: %v\n", clusterName, err)
	}
	return nil
}

// clusterAuthMaterial returns the API server auth config and Dex host for a
// cluster, minting TLS material when a custom domain is in use.
func clusterAuthMaterial(cluster config.ResolvedCluster, plan *topologyPlan) ([]byte, string, error) {
	// A cluster's own API server authenticates users through the topology's
	// issuer — the hub's Dex — so the authentication config names the hub's
	// issuer and trusts the hub's CA.
	//
	// For a single cluster that is its own, and the embedded default
	// authentication config already says so.
	dexHost := cluster.DexHost()
	caPEM := ""
	if plan != nil {
		if hub := plan.hubContext(); hub != nil && !cluster.IsHub() {
			dexHost = hub.DexHost()
		}
		caPEM = plan.hubTLS.CAPEM
	}
	if caPEM == "" {
		if !cluster.HasCustomDomain() {
			return manifests.AuthenticationConfig, dexHost, nil
		}
		tls, err := bootstrap.GenerateDexTLSMaterial(dexHost)
		if err != nil {
			return nil, "", fmt.Errorf("prepare dex tls material: %w", err)
		}
		return bootstrap.RenderAuthenticationConfig(dexHost, tls.CAPEM), dexHost, nil
	}
	return bootstrap.RenderAuthenticationConfig(dexHost, caPEM), dexHost, nil
}
