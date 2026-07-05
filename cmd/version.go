package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is overridden at build time via -ldflags "-X .../cmd.Version=<vX.Y.Z>".
var Version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(cmd.OutOrStdout(), Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
