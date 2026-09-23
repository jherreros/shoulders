package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

var (
	docsDir string
)

var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Generate CLI reference markdown (used by the docs site)",
	Long: `Generate markdown reference for all shoulders commands.

Intended for the Docusaurus site build (website/scripts/generate-cli-docs.sh).
Output files are written to --dir and should not be hand-edited.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if docsDir == "" {
			return fmt.Errorf("--dir is required")
		}
		if err := doc.GenMarkdownTree(rootCmd, docsDir); err != nil {
			return fmt.Errorf("generate markdown: %w", err)
		}
		return nil
	},
}

func init() {
	docsCmd.Flags().StringVar(&docsDir, "dir", "", "Output directory for generated markdown")
}
