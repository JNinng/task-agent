package cmd

import (
	"go-template/pkg/version"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := cmd.OutOrStdout().Write([]byte(version.String() + "\n"))
		return err
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
