package cmd

import (
	"context"
	"fmt"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"github.com/jherreros/shoulders/shoulders-cli/internal/manifests"
	"github.com/spf13/cobra"
)

var startClusterName string

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a previously stopped vind cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := bootstrap.StartVindCluster(cmd.Context(), startClusterName); err != nil {
			return err
		}
		fmt.Printf("Cluster %q started, waiting for API server...\n", startClusterName)

		if err := kube.WaitForAPIServer(context.Background(), kubeconfig); err != nil {
			return fmt.Errorf("waiting for API server: %w", err)
		}

		// Re-apply Gateway API CRDs so that any version-served flags
		// required by Cilium are up to date (e.g. TLSRoute v1alpha2).
		if err := kube.ApplyManifest(cmd.Context(), kubeconfig, manifests.GatewayAPICRDs, ""); err != nil {
			return fmt.Errorf("re-applying gateway api crds: %w", err)
		}

		if err := bootstrap.RestartCiliumWorkloads(kubeconfig); err != nil {
			return fmt.Errorf("restarting cilium: %w", err)
		}

		fmt.Printf("Cluster %q ready\n", startClusterName)
		return nil
	},
}

func init() {
	startCmd.Flags().StringVar(&startClusterName, "name", bootstrap.DefaultClusterName, "Name of the vind cluster")
}
