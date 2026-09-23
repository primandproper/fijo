package config

import (
	"context"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// The schedule's defaults.
const (
	// DefaultMaterializeInterval is how often the worker turns the standing
	// slots into rows. It is minutes rather than seconds because the horizon
	// is weeks: nothing within an hour of now is waiting on this pass, and a
	// series edit materializes its own rule without waiting for the loop.
	DefaultMaterializeInterval = 15 * time.Minute

	// DefaultHorizonWeeks is how far ahead a studio that has said nothing is
	// materialized. It is the same number the schedule domain defaults to, and
	// is here so a deployment can move the floor for every new studio.
	DefaultHorizonWeeks = 8
)

// ScheduleConfig is the grid's configuration: how often the materializer runs
// and how far ahead a studio that has set nothing is written.
//
// The per-studio horizon lives in the database, because a studio that books a
// term at a time and one that books a fortnight at a time want different
// answers. What is here is the deployment's default and the loop's cadence,
// neither of which is any studio's business.
type ScheduleConfig struct {
	_ struct{} `json:"-"`

	// MaterializeInterval is how often the worker's materialize pass runs.
	MaterializeInterval time.Duration `env:"MATERIALIZE_INTERVAL" json:"materializeInterval,omitempty"`

	// DefaultHorizonWeeks is the horizon a studio with no settings row gets.
	DefaultHorizonWeeks int `env:"DEFAULT_HORIZON_WEEKS" json:"defaultHorizonWeeks,omitempty"`
}

// EnsureDefaults fills in what a sparse config leaves out.
func (cfg *ScheduleConfig) EnsureDefaults() {
	if cfg.MaterializeInterval <= 0 {
		cfg.MaterializeInterval = DefaultMaterializeInterval
	}

	if cfg.DefaultHorizonWeeks <= 0 {
		cfg.DefaultHorizonWeeks = DefaultHorizonWeeks
	}
}

// ValidateWithContext applies defaults and validates what survives.
func (cfg *ScheduleConfig) ValidateWithContext(ctx context.Context) error {
	cfg.EnsureDefaults()

	return validation.ValidateStructWithContext(ctx, cfg,
		validation.Field(&cfg.MaterializeInterval, validation.Min(time.Second)),
		validation.Field(&cfg.DefaultHorizonWeeks, validation.Min(1), validation.Max(52)),
	)
}
