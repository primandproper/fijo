// Package sweep runs a store's Sweep on an interval, as a service.Runner.
//
// Several platform stores keep rows that expire — refresh tokens, reset tokens
// — and expose a Sweep that removes the dead ones without deciding when to call
// it. The decision is a deployment's: one sweep for the fleet, from the worker,
// rather than one per API replica. This is the loop the worker runs it in, in
// the shape service.New orders and drains with the platform's own runners.
package sweep

import (
	"context"
	"sync"
	"time"

	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/observability/metrics"
)

// Func is one store's sweep: how many rows it removed, or why it could not.
type Func func(ctx context.Context) (int64, error)

// The refusals.
var (
	ErrNilSweep       = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil sweep function")
	ErrEmptyName      = platformerrors.Wrap(platformerrors.ErrEmptyInputParameter, "empty sweeper name")
	ErrNonPositiveGap = platformerrors.New("sweep interval must be positive")
)

// Runner sweeps one store.
type Runner struct {
	sweep    Func
	o11y     observability.Observer
	swept    metrics.Int64Counter
	failures metrics.Int64Counter
	stop     chan struct{}
	done     chan struct{}
	name     string
	interval time.Duration
	once     sync.Once
}

// New builds a Runner. The interval is how often it sweeps and the name is how
// it appears in logs and metrics.
func New(name string, interval time.Duration, fn Func, pillars *observability.Pillars) (*Runner, error) {
	switch {
	case name == "":
		return nil, ErrEmptyName
	case interval <= 0:
		return nil, ErrNonPositiveGap
	case fn == nil:
		return nil, ErrNilSweep
	}

	r := &Runner{
		sweep:    fn,
		name:     name,
		interval: interval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		o11y:     observability.NewObserver("sweep_"+name, nil, nil),
	}

	var mp metrics.Provider
	if pillars != nil {
		r.o11y = observability.NewObserver("sweep_"+name, pillars.Logger, pillars.TracerProvider)
		mp = pillars.MetricsProvider
	}

	mp = metrics.EnsureMetricsProvider(mp)

	var err error

	if r.swept, err = mp.NewInt64Counter("sweep_" + name + "_rows_swept"); err != nil {
		return nil, platformerrors.Wrap(err, "creating sweep counter")
	}

	if r.failures, err = mp.NewInt64Counter("sweep_" + name + "_failures"); err != nil {
		return nil, platformerrors.Wrap(err, "creating sweep failure counter")
	}

	return r, nil
}

// Run sweeps until Close. It does not take a context, on purpose: tied to a
// server context it would stop mid-sweep the instant that context was
// cancelled, and Close is the stop signal that lets a sweep in flight finish.
func (r *Runner) Run() {
	defer close(r.done)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			r.once_(context.Background())
		}
	}
}

// once_ is one sweep, bounded to the interval so a slow one cannot pile up.
func (r *Runner) once_(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, r.interval)
	defer cancel()

	ctx, op := r.o11y.Begin(ctx)
	defer op.End()

	n, err := r.sweep(ctx)
	if err != nil {
		r.failures.Add(ctx, 1)
		op.Acknowledge(err, "sweeping %s", r.name)

		return
	}

	r.swept.Add(ctx, n)

	if n > 0 {
		op.Logger().WithValue("rows", n).Debug("swept")
	}
}

// Close stops the loop and waits for a sweep in flight, or for ctx.
func (r *Runner) Close(ctx context.Context) error {
	r.once.Do(func() { close(r.stop) })

	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return platformerrors.Wrapf(ctx.Err(), "waiting for the %s sweeper to stop", r.name)
	}
}
