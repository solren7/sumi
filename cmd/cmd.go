package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootView = &cobra.Command{
	Use:   "sumi",
	Short: "Sumi personal finance server and API client",
	// Errors go to stderr with a non-zero exit code and nothing else; printing
	// usage on every failure would bury the actual message for non-human callers.
	SilenceUsage: true,
}

func Execute() {
	if err := rootView.Execute(); err != nil {
		os.Exit(1)
	}
}

// groupCommand builds a command that only holds subcommands. It rejects an
// unknown subcommand instead of cobra's default of printing help and exiting 0,
// which a non-interactive caller would read as success.
func groupCommand(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown subcommand %q; run `%s --help`", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		},
	}
}
