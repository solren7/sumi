package cmd

import (
	"sumi/config"
	"sumi/internal/apps"

	"github.com/spf13/cobra"
)

var apiCmd = &cobra.Command{
	Use:   "api",
	Short: "Start the Fiber API server",
	Run: func(cmd *cobra.Command, args []string) {
		config := config.NewConfig()
		apps.StartAPIServer(config)
	},
}

func init() {
	rootView.AddCommand(apiCmd)
}
