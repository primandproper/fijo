package cli

import (
	"context"
	"fmt"

	"github.com/primandproper/fijo/internal/build/api"
	"github.com/primandproper/fijo/internal/build/worker"
)

// runServe composes and runs the API. The service owns its own observability
// pillars, built from the same config the CLI built its logger from, so the
// CLI's logger is for the CLI's own lines and the service's for everything the
// platform and Fijo's servers emit.
func (a *application) runServe(ctx context.Context) error {
	if a.cfg == nil {
		return errNoConfig
	}

	server, err := api.NewServer(ctx, a.cfg)
	if err != nil {
		return fmt.Errorf("building the API server: %w", err)
	}

	a.log().Info("serving")

	return server.Run(ctx)
}

// runWorker composes and runs the background loops.
func (a *application) runWorker(ctx context.Context) error {
	if a.cfg == nil {
		return errNoConfig
	}

	svc, err := worker.NewService(ctx, a.cfg)
	if err != nil {
		return fmt.Errorf("building the worker: %w", err)
	}

	a.log().Info("working")

	return svc.Run(ctx)
}
