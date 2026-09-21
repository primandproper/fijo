package cli

import (
	"github.com/spf13/cobra"
)

// newServeCommand returns the `serve` subcommand, which runs the API server.
func (a *application) newServeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the gRPC API server.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runServe(cmd.Context())
		},
	}
}
