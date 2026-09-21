package cli

import (
	"github.com/spf13/cobra"
)

// newWorkerCommand returns the `worker` subcommand, which runs the background
// loops: the platform's relays and sweepers and, as the domains arrive, Fijo's
// own materializers and reminder senders.
func (a *application) newWorkerCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "worker",
		Short: "Run the background worker.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runWorker(cmd.Context())
		},
	}
}
