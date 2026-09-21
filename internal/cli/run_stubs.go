package cli

import "context"

// These are replaced by the composition roots in internal/build once they exist.

func (a *application) runServe(_ context.Context) error  { return nil }
func (a *application) runWorker(_ context.Context) error { return nil }
