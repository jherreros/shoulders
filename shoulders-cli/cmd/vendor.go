package cmd

import (
	"fmt"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/airgap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/cli"
	"github.com/spf13/cobra"
)

var vendorOutput string

var vendorCmd = &cobra.Command{
	Use:   "vendor",
	Short: "Build a self-contained airgap bundle (online step)",
	Long: `Resolve every platform Helm chart, harvest every container image
from the cluster, and pack them with the addon manifests and the Flux
install manifest into a single bundle file.

Run online against a healthy cluster, then carry the bundle into the
airgapped environment and install with 'shoulders up --bundle <file>'.
Harvests the union across profiles, so one bundle serves small, medium,
and large installs.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := cli.FindRepoRoot()
		if err != nil {
			return fmt.Errorf("vendor requires a Shoulders checkout: %w", err)
		}
		outPath := vendorOutput
		if outPath == "" {
			outPath = fmt.Sprintf("shoulders-bundle-%s.tar.gz", time.Now().UTC().Format("20060102-150405"))
		}
		meta, err := airgap.Vendor(cmd.Context(), airgap.VendorOptions{
			RepoRoot:    root,
			ClusterName: currentConfig.ClusterName(),
			Kubeconfig:  kubeconfig,
			OutPath:     outPath,
			Log:         func(message string) { fmt.Println(message) },
		})
		if err != nil {
			return err
		}
		fmt.Printf("vendored %d charts and %d images into %s (ref %s)\n", len(meta.Charts), len(meta.Images), outPath, meta.ShouldersRef)
		return nil
	},
}

func init() {
	vendorCmd.Flags().StringVarP(&vendorOutput, "output", "o", "", "Bundle output path (default shoulders-bundle-<timestamp>.tar.gz)")
	rootCmd.AddCommand(vendorCmd)
}
