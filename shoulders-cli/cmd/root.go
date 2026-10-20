package cmd

import (
	"fmt"
	"os"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"github.com/jherreros/shoulders/shoulders-cli/internal/output"
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags.
var Version = "dev"

// Commands that do not require a shoulders cluster context.
var skipClusterCheck = map[string]bool{
	"docs":    true,
	"down":    true,
	"init":    true,
	"up":      true,
	"update":  true,
	"cluster": true,
	"start":   true,
	"stop":    true,
	"skill":   true,
	"help":    true,
	"version": true,
}

var (
	rootCmd = &cobra.Command{
		Use:     "shoulders",
		Short:   "Developer CLI for the Shoulders IDP",
		Version: Version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := loadRuntimeConfig(); err != nil {
				return err
			}

			if err := ensureShouldersCluster(cmd); err != nil {
				return err
			}
			return nil
		},
	}
	currentConfig    *config.Config
	kubeconfig       string
	outputFormat     string
	configFile       string
	configOverrides  []string
	loadedConfigPath string
	// targetClusterName is the --cluster value, empty when unspecified.
	targetClusterName string
	// targetCluster is the resolved cluster every command operates on.
	targetCluster *config.ResolvedCluster
)

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	rootCmd.PersistentFlags().StringVar(&configFile, "config", defaultConfigPath(), "Path to config file")
	rootCmd.PersistentFlags().StringArrayVar(&configOverrides, "set", nil, "Set a config value override (key=value), repeatable. Overrides are ephemeral and are not saved to the config file")
	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", string(output.Table), "Output format: table|json|yaml")
	rootCmd.PersistentFlags().StringVar(&targetClusterName, "cluster", "", "Target cluster from the topology (defaults to the only cluster, or the hub)")

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(workspaceCmd)
	rootCmd.AddCommand(appCmd)
	rootCmd.AddCommand(workloadCmd)
	rootCmd.AddCommand(infraCmd)
	rootCmd.AddCommand(clusterCmd)
	rootCmd.AddCommand(dashboardCmd)
	rootCmd.AddCommand(portalCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(reporterCmd)
	rootCmd.AddCommand(secretsCmd)
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(skillCmd)
	rootCmd.AddCommand(docsCmd)
}

func outputOption() (output.Format, error) {
	return output.ParseFormat(outputFormat)
}

func loadRuntimeConfig() error {
	path, err := config.Path(configFile)
	if err != nil {
		return err
	}
	loadedConfigPath = path

	cfg, err := config.LoadWithOverrides(configOverrides, path)
	if err != nil {
		return err
	}
	currentConfig = cfg

	// Resolve the target cluster before anything else, so the kubeconfig and
	// context used by every command come from the topology rather than from a
	// single global block. With one cluster this resolves to that cluster and
	// behaviour is unchanged.
	resolved, err := resolveTargetCluster(cfg)
	if err != nil {
		return err
	}
	targetCluster = resolved
	return nil
}

// resolveTargetCluster picks the cluster a command operates on: the one named
// by --cluster, else the sole cluster, else the hub.
//
// It also installs the process-global kube context override that
// internal/kube consults, because the 40-odd call sites in cmd/ read that
// rather than a cluster handle. Keeping the override derived from the topology
// means those sites follow --cluster without being rewritten.
func resolveTargetCluster(cfg *config.Config) (*config.ResolvedCluster, error) {
	var (
		cluster *config.ResolvedCluster
		err     error
	)
	if targetClusterName != "" {
		cluster, err = cfg.ResolveCluster(targetClusterName)
	} else {
		cluster, err = cfg.DefaultCluster()
	}
	if err != nil {
		return nil, err
	}

	if kubeconfig == "" {
		switch {
		case os.Getenv("SHOULDERS_KUBECONFIG") != "":
			kubeconfig = os.Getenv("SHOULDERS_KUBECONFIG")
		case cluster.Kubeconfig != "":
			kubeconfig = cluster.Kubeconfig
		case cfg.Cluster.Kubeconfig != "":
			kubeconfig = cfg.Cluster.Kubeconfig
		}
	}

	contextName := cluster.Context
	if contextName == "" && cluster.ProviderOrDefault() == config.ProviderVind {
		// vind writes its context under a prefixed name.
		contextName = bootstrap.ContextPrefix + cluster.Name
	}
	kube.SetContextOverride(contextName)
	return cluster, nil
}

func defaultConfigPath() string {
	path, err := config.Path()
	if err != nil {
		return ""
	}
	return path
}

// ensureShouldersCluster verifies the current kubeconfig context belongs to
// a compatible cluster. Commands in skipClusterCheck are exempt.
func ensureShouldersCluster(cmd *cobra.Command) error {
	// Walk up to find the top-level subcommand name.
	name := rootCmdName(cmd)
	if skipClusterCheck[name] {
		return nil
	}
	if currentConfig != nil && targetCluster.ProviderOrDefault() == config.ProviderExisting {
		ctx, err := kube.CurrentContext(kubeconfig)
		if err != nil {
			return fmt.Errorf("cannot determine cluster context: %w", err)
		}
		if ctx == "" {
			return fmt.Errorf("no kube context selected; set context for cluster %q in %q or switch kubeconfig context", targetCluster.Name, loadedConfigPath)
		}
		return nil
	}

	ok, ctx, err := kube.IsShouldersContext(kubeconfig)
	if err != nil {
		return fmt.Errorf("cannot determine cluster context: %w", err)
	}
	if !ok {
		return fmt.Errorf("current context %q is not the Shoulders vind cluster %q; switch with 'shoulders cluster use <name>' or run 'shoulders up --cluster %s'", ctx, targetCluster.Name, targetCluster.Name)
	}
	return nil
}

func rootCmdName(cmd *cobra.Command) string {
	for cmd.HasParent() && cmd.Parent().HasParent() {
		cmd = cmd.Parent()
	}
	return cmd.Name()
}

// configuredClusterName resolves the cluster a create/delete style command
// acts on: the flag if given, else the resolved target cluster.
func configuredClusterName(cmd *cobra.Command, flagName, flagValue string) string {
	if cmd.Flags().Changed(flagName) {
		return flagValue
	}
	if targetCluster != nil {
		return targetCluster.Name
	}
	if currentConfig == nil {
		return flagValue
	}
	return currentConfig.ClusterName()
}

func saveCurrentConfig() error {
	if currentConfig == nil {
		return nil
	}
	return config.Save(currentConfig, loadedConfigPath)
}
