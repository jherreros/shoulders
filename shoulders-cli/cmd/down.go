package cmd

import (
	"fmt"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"github.com/spf13/cobra"
)

var downClusterName string

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Delete the local vind cluster or uninstall Shoulders from an existing cluster",
	Long: `Delete the local vind cluster or uninstall Shoulders from an existing cluster.

In a topology this removes every cluster the configuration declares, so
` + "`shoulders down`" + ` followed by ` + "`shoulders up`" + ` is a clean cycle.
Pass --name or --cluster to remove exactly one of them.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		topology, err := currentConfig.Topology()
		if err != nil {
			return err
		}

		// A single cluster, or an explicit target, is one removal.
		named := cmd.Flags().Changed("name") || cmd.Flags().Changed("cluster")
		clusters := topology.AllClusters()
		if named {
			name := configuredClusterName(cmd, "name", downClusterName)
			filtered := make([]*config.ResolvedCluster, 0, 1)
			for _, cluster := range clusters {
				if cluster.Name == name {
					filtered = append(filtered, cluster)
				}
			}
			if len(filtered) == 0 {
				return fmt.Errorf("no cluster named %q in the topology; known clusters: %v", name, topology.Names())
			}
			clusters = filtered
		}

		for _, cluster := range clusters {
			selectCluster(cluster)
			if err := downOneCluster(cmd, cluster); err != nil {
				return fmt.Errorf("remove %q: %w", cluster.Name, err)
			}
		}
		return nil
	},
}

// downOneCluster removes Shoulders from a single cluster.
//
// For a cluster the CLI created, that means deleting it. For a cluster it did
// not create, it means uninstalling the platform from it.
func downOneCluster(cmd *cobra.Command, cluster *config.ResolvedCluster) error {
	if cluster.ProviderOrDefault() == config.ProviderExisting {
		if err := bootstrap.UninstallShouldersFlux(cmd.Context(), kubeconfig, cluster.FluxPathPrefix()); err != nil {
			return err
		}
		if err := bootstrap.CleanupKyvernoWebhooks(kubeconfig); err != nil {
			return fmt.Errorf("cleanup kyverno webhooks: %w", err)
		}
		if cluster.CiliumEnabled() {
			if err := bootstrap.UninstallCilium(kubeconfig, targetClusterContextName()); err != nil {
				return fmt.Errorf("uninstall cilium: %w", err)
			}
		}
		if err := bootstrap.WaitForNamespacesDeleted(cmd.Context(), kubeconfig); err != nil {
			return fmt.Errorf("wait for platform namespaces to terminate: %w", err)
		}
		fmt.Printf("Shoulders platform resources removed from %s\n", cluster.Name)
		return nil
	}
	return bootstrap.DeleteVindCluster(cmd.Context(), cluster.Name)
}

func init() {
	downCmd.Flags().StringVar(&downClusterName, "name", bootstrap.DefaultClusterName, "Name of the vind cluster to delete")
}
