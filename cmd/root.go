package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "upgrade-test",
	Short: "A tool for testing operator upgrades",
	Long: `A tool for testing operator upgrades.
It supports testing operator upgrades with different versions and paths.`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
