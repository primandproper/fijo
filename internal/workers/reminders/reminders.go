// Package reminders runs the reminder loops: the planner, which writes every
// reminder owed once a minute, and the timer worker, which delivers each one.
//
// The work is internal/domain/messaging's. What is here is the composition: a
// timer set and a usage recorder over the platform's tables, the
// messaging.Reminders built over them, and the three runners the worker's
// composition root hands to service.New — the planner, the timer worker, and a
// sweeper that reaps fired timers past their retention.
package reminders

import (
	"context"
	"sync"
	"time"

	"github.com/primandproper/fijo/internal/config"
	"github.com/primandproper/fijo/internal/domain/messaging"
	"github.com/primandproper/fijo/internal/studios"
	"github.com/primandproper/fijo/internal/workers/sweep"

	"github.com/primandproper/platform-go/v15/metering"
	"github.com/primandproper/platform-go/v15/service"
	"github.com/primandproper/platform-go/v15/timers"
	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/email"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/observability"

	"github.com/samber/do/v2"
)

// TimerSet is the name the reminders' timers are partitioned under in the
// shared timer table.
const TimerSet = "fijo_reminders"

// The intervals. A reminder is owed to the minute, so the planner asks once a
// minute; the timer worker polls a few times a minute because the planner
// schedules each delivery for now, and a delivery that waits out a minute's
// poll is a minute late for nothing.
const (
	PlanInterval  = time.Minute
	TimerPoll     = 10 * time.Second
	TimerRetry    = time.Minute
	ReapInterval  = time.Hour
	plannerName   = "reminders_plan"
	timerReapName = "reminder_timers"
)

// Register provides the messaging store, the timer set, the usage recorder and
// the *messaging.Reminders, over the database client, the emailer and the
// studio lookup already in the injector.
func Register(i do.Injector) {
	do.Provide(i, func(i do.Injector) (messaging.Store, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		return messaging.NewSQLStore(messaging.WithPillars(pillars))
	})

	do.Provide(i, func(i do.Injector) (*timers.Timers[string], error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		client, err := do.Invoke[database.Client](i)
		if err != nil {
			return nil, err
		}

		return timers.New[string](do.MustInvoke[context.Context](i), &timers.Config{Name: TimerSet}, client,
			timers.WithLogger(pillars.Logger),
			timers.WithTracerProvider(pillars.TracerProvider),
			timers.WithMetricsProvider(pillars.MetricsProvider),
		)
	})

	do.Provide(i, func(i do.Injector) (*metering.DurableRecorder, error) {
		client, err := do.Invoke[database.Client](i)
		if err != nil {
			return nil, err
		}

		store, err := metering.NewSQLStore(client)
		if err != nil {
			return nil, platformerrors.Wrap(err, "building the metering store")
		}

		registry, err := Registry()
		if err != nil {
			return nil, err
		}

		return metering.NewDurableRecorder(do.MustInvoke[context.Context](i), &metering.RecorderConfig{}, store, registry)
	})

	do.Provide(i, func(i do.Injector) (*messaging.Reminders, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		cfg, err := do.Invoke[*config.Config](i)
		if err != nil {
			return nil, err
		}

		client, err := do.Invoke[database.Client](i)
		if err != nil {
			return nil, err
		}

		store, err := do.Invoke[messaging.Store](i)
		if err != nil {
			return nil, err
		}

		zones, err := do.Invoke[*studios.Zones](i)
		if err != nil {
			return nil, err
		}

		set, err := do.Invoke[*timers.Timers[string]](i)
		if err != nil {
			return nil, err
		}

		recorder, err := do.Invoke[*metering.DurableRecorder](i)
		if err != nil {
			return nil, err
		}

		emailer, err := do.Invoke[email.Emailer](i)
		if err != nil {
			return nil, err
		}

		return messaging.NewReminders(client, store, zones, set, emailer, recorder, cfg.Auth.Mail.FromAddress,
			messaging.WithRemindersPillars(pillars))
	})
}

// Registry is the meters reminders are counted on: one, counted and capped by
// nothing until plans exist.
func Registry() (*metering.Registry, error) {
	registry := metering.NewRegistry()

	if err := registry.RegisterMeter(metering.Meter{
		Name:        messaging.MeterEmails,
		Unit:        "emails",
		Aggregation: metering.AggregationSum,
		Period:      metering.PeriodMonth,
	}); err != nil {
		return nil, platformerrors.Wrap(err, "registering the reminder email meter")
	}

	if err := registry.RegisterUnlimitedQuota(messaging.MeterEmails); err != nil {
		return nil, platformerrors.Wrap(err, "registering the reminder email quota")
	}

	return registry, nil
}

// Runners builds the planner, the timer worker and the timer reaper.
func Runners(ctx context.Context, i do.Injector, pillars *observability.Pillars) ([]service.Runner, error) {
	r, err := do.Invoke[*messaging.Reminders](i)
	if err != nil {
		return nil, platformerrors.Wrap(err, "building the reminders")
	}

	set, err := do.Invoke[*timers.Timers[string]](i)
	if err != nil {
		return nil, platformerrors.Wrap(err, "building the reminder timers")
	}

	planner, err := sweep.New(plannerName, PlanInterval, r.RunOnce, pillars)
	if err != nil {
		return nil, err
	}

	reaper, err := sweep.New(timerReapName, ReapInterval, set.Reap, pillars)
	if err != nil {
		return nil, err
	}

	worker, err := timers.NewWorker(ctx,
		&timers.WorkerConfig{Poll: TimerPoll, RetryDelay: TimerRetry},
		set,
		func(ctx context.Context, due timers.Due[string]) error {
			return r.Deliver(ctx, due.Key)
		})
	if err != nil {
		return nil, platformerrors.Wrap(err, "building the reminder timer worker")
	}

	return []service.Runner{planner, newTimerRunner(worker), reaper}, nil
}

// timerRunner is a timer worker in the shape service.New runs: Run blocks
// until Close, and Close stops it and waits.
//
// The worker stops when its context is cancelled, so Close cancels it. A
// delivery in flight completes on the context the worker detaches for it,
// which is the timer package's promise rather than this one's.
type timerRunner struct {
	worker  *timers.Worker[string]
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	started chan struct{}
	start   sync.Once
}

func newTimerRunner(worker *timers.Worker[string]) *timerRunner {
	ctx, cancel := context.WithCancel(context.Background())

	return &timerRunner{
		worker:  worker,
		ctx:     ctx,
		cancel:  cancel,
		done:    make(chan struct{}),
		started: make(chan struct{}),
	}
}

// Run implements service.Runner.
func (t *timerRunner) Run() {
	t.start.Do(func() { close(t.started) })
	defer close(t.done)

	// The worker's only return is its context ending, which is Close.
	_ = t.worker.Run(t.ctx) //nolint:errcheck // see above
}

// Close implements service.Runner.
func (t *timerRunner) Close(ctx context.Context) error {
	t.cancel()

	select {
	case <-t.started:
	default:
		// Never run, so there is nothing to wait for.
		return nil
	}

	select {
	case <-t.done:
		return nil
	case <-ctx.Done():
		return platformerrors.Wrap(ctx.Err(), "waiting for the reminder timer worker to stop")
	}
}
