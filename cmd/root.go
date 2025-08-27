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
	RunE: func(cmd *cobra.Command, args []string) error {
		// Use the global instance that has the flags
		return upgradeCommandInstance.Execute()
	},
}

// Global upgrade command instance to share flags
var upgradeCommandInstance *UpgradeCommand

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	// Create a global instance and add flags to it
	upgradeCommandInstance = NewUpgradeCommand()
	upgradeCommandInstance.AddFlags(rootCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
