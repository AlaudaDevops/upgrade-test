// cmd/upgrade.go

package cmd

import (
	"github.com/spf13/cobra"
)

// Global upgrade command instance to share flags
var upgradeCommandInstance *UpgradeCommand

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Run upgrade tests",
	Long: `Run upgrade tests for operators.
It will process the upgrade paths defined in the configuration file.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Use the global instance that has the flags
		return upgradeCommandInstance.Execute()
	},
}

func init() {
	// Create a global instance and add flags to it
	upgradeCommandInstance = NewUpgradeCommand()
	upgradeCommandInstance.AddFlags(upgradeCmd)
	rootCmd.AddCommand(upgradeCmd)
}
