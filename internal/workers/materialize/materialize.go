// Package materialize runs the schedule's materializer on an interval.
//
// It is a constructor and nothing else. The work — turning each studio's
// standing slots into rows out to its own horizon — belongs to
// internal/domain/schedule, and the loop that calls it on a gap, logs what it
// wrote and drains on shutdown is internal/workers/sweep's, which is a generic
// interval runner despite its name. What is here is the naming and the
// default that join the two, so that the worker's composition root registers a
// runner rather than assembling one.
package materialize

import (
	"context"
	"time"

	"github.com/primandproper/fijo/internal/domain/schedule"
	"github.com/primandproper/fijo/internal/studios"
	"github.com/primandproper/fijo/internal/workers/sweep"

	"github.com/primandproper/platform-go/v15/identity"
	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/observability"

	"github.com/samber/do/v2"
)

// RunnerName is what the loop calls itself in a log line.
const RunnerName = "schedule_materialize"

// Pass is the work one tick does: materialize, and say how many rows it wrote.
type Pass func(ctx context.Context) (int64, error)

// New builds the runner. The pass is normally a
// *schedule.Materializer's RunOnce.
func New(interval time.Duration, pass Pass, pillars *observability.Pillars) (*sweep.Runner, error) {
	return sweep.New(RunnerName, interval, sweep.Func(pass), pillars)
}

// Register provides the studio zone lookup and the *schedule.Materializer
// over the schedule.Store, identity.Store and database.Client already in the
// injector. The worker runs it on an interval; serve runs it on a series edit.
func Register(i do.Injector) {
	// The studio's time zone is identity's column, which the schedule's
	// statements cannot join. This is the seam that answers it.
	do.Provide(i, func(i do.Injector) (schedule.Zones, error) {
		accounts, err := do.Invoke[identity.Store](i)
		if err != nil {
			return nil, err
		}

		client, err := do.Invoke[database.Client](i)
		if err != nil {
			return nil, err
		}

		return studios.NewZones(accounts, client.Reader())
	})

	do.Provide(i, func(i do.Injector) (*schedule.Materializer, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		client, err := do.Invoke[database.Client](i)
		if err != nil {
			return nil, err
		}

		store, err := do.Invoke[schedule.Store](i)
		if err != nil {
			return nil, err
		}

		zones, err := do.Invoke[schedule.Zones](i)
		if err != nil {
			return nil, err
		}

		return schedule.NewMaterializer(client, store, zones, schedule.WithMaterializerPillars(pillars))
	})
}
