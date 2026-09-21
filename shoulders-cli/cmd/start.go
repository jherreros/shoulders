package cmd

import (
	"context"
	"fmt"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"github.com/jherreros/shoulders/shoulders-cli/internal/manifests"
	"github.com/spf13/cobra"
)

var startClusterName string

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a previously stopped vind cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		if currentConfig.Provider() == config.ProviderExisting {
			if err := bootstrap.EnsureExistingCluster(cmd.Context(), kubeconfig); err != nil {
				return err
			}
			fmt.Println("Existing cluster context is reachable")
			return nil
		}

		clusterName := configuredClusterName(cmd, "name", startClusterName)
		if err := bootstrap.StartVindCluster(cmd.Context(), clusterName); err != nil {
			return err
		}
		fmt.Printf("Cluster %q started, waiting for API server...\n", clusterName)

		if err := kube.WaitForAPIServer(context.Background(), kubeconfig); err != nil {
			return fmt.Errorf("waiting for API server: %w", err)
		}

		// vind freezes the cluster DNS upstream at creation time; a later
		// change in Docker Desktop networking silently breaks all
		// in-cluster DNS. Reconcile CoreDNS forwarding on every start.
		// Best effort: a stale-but-working setup must not fail startup.
		if changed, err := bootstrap.EnsureClusterDNS(cmd.Context(), kubeconfig); err != nil {
			fmt.Printf("Warning: could not reconcile cluster DNS: %v\n", err)
		} else if changed {
			fmt.Println("Reconciled cluster DNS forwarding")
		}

		// Re-apply Gateway API CRDs so that any version-served flags
		// required by Cilium are up to date (e.g. TLSRoute v1alpha2).
		if err := kube.ApplyManifest(cmd.Context(), kubeconfig, manifests.GatewayAPICRDs, ""); err != nil {
			return fmt.Errorf("re-applying gateway api crds: %w", err)
		}

		if currentConfig.CiliumEnabled() {
			if err := bootstrap.RestartCiliumWorkloads(kubeconfig); err != nil {
				return fmt.Errorf("restarting cilium: %w", err)
			}
		}

		// Best effort: if the platform reconciles from the local OCI
		// registry and its storage was wiped while stopped, re-push the
		// persisted tag so Flux recovers without a manual sync.
		if err := repushLocalSnapshotIfNeeded(cmd.Context()); err != nil {
			fmt.Printf("Warning: %v\n", err)
		}

		fmt.Printf("Cluster %q ready\n", clusterName)
		return nil
	},
}

func init() {
	startCmd.Flags().StringVar(&startClusterName, "name", bootstrap.DefaultClusterName, "Name of the vind cluster")
}
