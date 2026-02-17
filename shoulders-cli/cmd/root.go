package cmd

import (
	"fmt"
	"os"

	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"github.com/jherreros/shoulders/shoulders-cli/internal/output"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	rootCmd = &cobra.Command{
		Use:   "shoulders",
		Short: "Developer CLI for the Shoulders IDP",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			currentConfig = cfg
			return nil
		},
	}
	currentConfig *config.Config
	kubeconfig    string
	outputFormat  string
)

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", string(output.Table), "Output format: table|json|yaml")

	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(workspaceCmd)
	rootCmd.AddCommand(appCmd)
	rootCmd.AddCommand(infraCmd)
	rootCmd.AddCommand(clusterCmd)
	rootCmd.AddCommand(dashboardCmd)
	rootCmd.AddCommand(headlampCmd)
	rootCmd.AddCommand(logsCmd)
}

func initConfig() {
	viper.SetEnvPrefix("SHOULDERS")
	viper.AutomaticEnv()
}

func outputOption() (output.Format, error) {
	return output.ParseFormat(outputFormat)
}
