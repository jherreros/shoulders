package cmd

import (
	"fmt"
	"os"

	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"github.com/jherreros/shoulders/shoulders-cli/internal/output"
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags.
var Version = "dev"

// Commands that do not require a shoulders cluster context.
var skipClusterCheck = map[string]bool{
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
	rootCmd.PersistentFlags().StringArrayVar(&configOverrides, "set", nil, "Set a config value override (key=value), repeatable")
	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", string(output.Table), "Output format: table|json|yaml")

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(workspaceCmd)
	rootCmd.AddCommand(appCmd)
	rootCmd.AddCommand(infraCmd)
	rootCmd.AddCommand(clusterCmd)
	rootCmd.AddCommand(dashboardCmd)
	rootCmd.AddCommand(portalCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(reporterCmd)
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(skillCmd)
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

	if kubeconfig == "" {
		if envKubeconfig := os.Getenv("SHOULDERS_KUBECONFIG"); envKubeconfig != "" {
			kubeconfig = envKubeconfig
		} else if cfg.Cluster.Kubeconfig != "" {
			kubeconfig = cfg.Cluster.Kubeconfig
		}
	}

	kube.SetContextOverride(cfg.Cluster.Context)
	return nil
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
	if currentConfig != nil && currentConfig.Provider() == config.ProviderExisting {
		ctx, err := kube.CurrentContext(kubeconfig)
		if err != nil {
			return fmt.Errorf("cannot determine cluster context: %w", err)
		}
		if ctx == "" {
			return fmt.Errorf("no kube context selected; set cluster.context in %q or switch kubeconfig context", loadedConfigPath)
		}
		return nil
	}

	ok, ctx, err := kube.IsShouldersContext(kubeconfig)
	if err != nil {
		return fmt.Errorf("cannot determine cluster context: %w", err)
	}
	if !ok {
		return fmt.Errorf("current context %q is not a Shoulders vind cluster; switch with 'shoulders cluster use <name>' or run 'shoulders up'", ctx)
	}
	return nil
}

func rootCmdName(cmd *cobra.Command) string {
	for cmd.HasParent() && cmd.Parent().HasParent() {
		cmd = cmd.Parent()
	}
	return cmd.Name()
}

func configuredClusterName(cmd *cobra.Command, flagName, flagValue string) string {
	if cmd.Flags().Changed(flagName) {
		return flagValue
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
