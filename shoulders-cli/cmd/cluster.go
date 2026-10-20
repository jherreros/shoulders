package cmd

import (
	"fmt"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"github.com/jherreros/shoulders/shoulders-cli/internal/output"
	"github.com/spf13/cobra"
)

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Manage target clusters and kube contexts",
}

var clusterListCmd = &cobra.Command{
	Use:   "list",
	Short: "List topology clusters, or clusters/kube contexts for a single-cluster config",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := outputOption()
		if err != nil {
			return err
		}

		// A declared topology is the source of truth, so show it rather than
		// whatever clusters or contexts happen to exist.
		if len(currentConfig.Clusters) > 0 {
			topology, tErr := currentConfig.Topology()
			if tErr != nil {
				return tErr
			}
			if format == output.Table {
				rows := [][]string{}
				for _, cluster := range topology.Clusters {
					marker := ""
					if targetCluster != nil && cluster.Name == targetCluster.Name {
						marker = "*"
					}
					ports := cluster.Ports.WithDefaults()
					rows = append(rows, []string{
						marker,
						cluster.Name,
						cluster.Role,
						cluster.ProviderOrDefault(),
						cluster.Profile,
						fmt.Sprintf("%d/%d", ports.HTTP, ports.HTTPS),
					})
				}
				return output.PrintTable([]string{"", "NAME", "ROLE", "PROVIDER", "PROFILE", "PORTS"}, rows)
			}
			payload, err := output.Render(topology.Clusters, format)
			if err != nil {
				return err
			}
			fmt.Println(string(payload))
			return nil
		}

		var clusters []string
		if targetCluster.ProviderOrDefault() == config.ProviderExisting {
			clusters, err = kube.ListContexts(kubeconfig)
		} else {
			clusters, err = bootstrap.ListClusters()
		}
		if err != nil {
			return err
		}

		if format == output.Table {
			rows := [][]string{}
			for _, c := range clusters {
				rows = append(rows, []string{c})
			}
			return output.PrintTable([]string{"Name"}, rows)
		}

		payload, err := output.Render(clusters, format)
		if err != nil {
			return err
		}
		fmt.Println(string(payload))
		return nil
	},
}

var clusterUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Switch context to another cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		contextName := name
		if targetCluster.ProviderOrDefault() != config.ProviderExisting {
			contextName = bootstrap.ContextPrefix + name
		}

		if err := kube.SwitchContext(kubeconfig, contextName); err != nil {
			return err
		}
		// Update the right place: a topology entry when one is declared, or the
		// legacy `cluster:` block for a single-cluster config. Writing the legacy
		// block alongside `clusters:` would leave two competing sources of truth.
		if len(currentConfig.Clusters) > 0 {
			if err := currentConfig.SetClusterContext(name, contextName); err != nil {
				return err
			}
		} else if targetCluster.ProviderOrDefault() == config.ProviderExisting {
			currentConfig.Cluster.Context = contextName
		} else {
			currentConfig.Cluster.Name = name
		}
		if err := saveCurrentConfig(); err != nil {
			return err
		}
		fmt.Printf("Switched to cluster %s (context: %s)\n", name, contextName)
		return nil
	},
}

func init() {
	clusterCmd.AddCommand(clusterListCmd)
	clusterCmd.AddCommand(clusterUseCmd)
}
