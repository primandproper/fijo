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

	"github.com/primandproper/fijo/internal/workers/sweep"

	"github.com/primandproper/primitives-go/v2/observability"
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
