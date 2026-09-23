package schedule

import (
	"context"
	"time"

	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

// MaxHorizonWeeks is the furthest ahead any studio may ask to be materialized,
// and is the window the worker reads series over before each studio's own
// horizon narrows it. It matches the schema's check.
const MaxHorizonWeeks = 52

// Zones answers what zone a studio keeps its clock in.
//
// It is a seam rather than a join because the answer lives in
// identity_accounts, a platform table this domain's statements cannot reach.
// The implementation reads the account; a test hands back a location.
type Zones interface {
	ZoneOf(ctx context.Context, studioID string) (*time.Location, error)
}

// ZoneFunc adapts a function to Zones.
type ZoneFunc func(ctx context.Context, studioID string) (*time.Location, error)

// ZoneOf implements Zones.
func (f ZoneFunc) ZoneOf(ctx context.Context, studioID string) (*time.Location, error) {
	return f(ctx, studioID)
}

// Plan is the pure half of materialization: the dates a series runs between
// two dates inclusive, minus the days the studio is shut.
//
// It answers in civil dates and never in instants, because which instant a
// date and a wall-clock time name depends on a zone this function is not told.
// Turning the dates into instants is the caller's, and is where DST is
// resolved.
func Plan(series *Series, from, until Date, closures []*Closure) []Date {
	if series == nil || from.IsZero() || until.IsZero() || until.Before(from) {
		return nil
	}

	// Nothing before the rule begins, and nothing after it ends.
	start := from
	if start.Before(series.StartsOn) {
		start = series.StartsOn
	}

	stop := until
	if !series.EndsOn.IsZero() && series.EndsOn.Before(stop) {
		stop = series.EndsOn
	}

	if stop.Before(start) {
		return nil
	}

	// Walk from the first day the rule could apply rather than day by day from
	// the start of the window: a weekly series has one candidate a week and a
	// fortnightly one has one a fortnight.
	candidate := series.FirstRunOnOrAfter(start)
	if candidate.IsZero() {
		return nil
	}

	step := max(series.IntervalWeeks*7, 7)

	var dates []Date

	for !candidate.After(stop) {
		if series.Runs(candidate) && !shut(closures, candidate) {
			dates = append(dates, candidate)
		}

		candidate = candidate.AddDays(step)
	}

	return dates
}

func shut(closures []*Closure, on Date) bool {
	for _, closure := range closures {
		if closure.Covers(on) {
			return true
		}
	}

	return false
}

// Materializer writes the rows Plan names, out to each studio's own horizon.
//
// It is the only thing that turns a rule into a row. Running it again over a
// horizon it has already written is free: the store's materialize is guarded
// by a unique index and reports that it wrote nothing rather than failing,
// which is what lets the worker run on an interval and what lets a series edit
// re-run it immediately.
type Materializer struct {
	client database.Client
	store  Store
	zones  Zones
	o11y   observability.Observer
	now    func() time.Time
}

// MaterializerOption configures a Materializer.
type MaterializerOption func(*Materializer)

// WithMaterializerPillars attaches the observability pillars.
func WithMaterializerPillars(p *observability.Pillars) MaterializerOption {
	return func(m *Materializer) {
		if p != nil {
			m.o11y = observability.NewObserver("schedule_materializer", p.Logger, p.TracerProvider)
		}
	}
}

// WithClock replaces the clock, for a test that needs to know what day it is.
func WithClock(now func() time.Time) MaterializerOption {
	return func(m *Materializer) {
		if now != nil {
			m.now = now
		}
	}
}

// NewMaterializer builds one over the database, the store and the zone lookup.
func NewMaterializer(client database.Client, store Store, zones Zones, opts ...MaterializerOption) (*Materializer, error) {
	switch {
	case client == nil:
		return nil, platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil database client")
	case store == nil:
		return nil, platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil schedule store")
	case zones == nil:
		return nil, platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil zone lookup")
	}

	m := &Materializer{
		client: client,
		store:  store,
		zones:  zones,
		o11y:   observability.NewObserver("schedule_materializer", nil, nil),
		now:    time.Now,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}

	return m, nil
}

// RunOnce materializes every live series out to its studio's horizon and
// reports how many rows it wrote.
//
// A studio whose zone cannot be resolved is skipped rather than fatal: one
// studio with a bad zone must not stop the rest of a deployment's grid from
// being written. The failure is logged against that studio and the loop
// continues.
func (m *Materializer) RunOnce(ctx context.Context) (_ int64, err error) {
	ctx, op := m.o11y.Begin(ctx)
	defer op.End()

	today := Today(m.now(), time.UTC)
	furthest := today.AddDays(MaxHorizonWeeks * 7)

	series, err := m.store.ListLiveSeries(ctx, m.client.Reader(), today, furthest)
	if err != nil {
		return 0, op.Error(err, "reading the live series")
	}

	if len(series) == 0 {
		return 0, nil
	}

	byStudio := make(map[string][]*Series)
	studios := make([]string, 0, len(series))

	for _, one := range series {
		if _, seen := byStudio[one.StudioID]; !seen {
			studios = append(studios, one.StudioID)
		}

		byStudio[one.StudioID] = append(byStudio[one.StudioID], one)
	}

	closures, err := m.store.ListClosuresForStudios(ctx, m.client.Reader(), studios)
	if err != nil {
		return 0, op.Error(err, "reading the closures")
	}

	var written int64

	for _, studio := range studios {
		count, studioErr := m.materializeStudio(ctx, studio, byStudio[studio], closures[studio], today)
		if studioErr != nil {
			op.Acknowledge(studioErr, "materializing studio %q", studio)

			continue
		}

		written += count
	}

	return written, nil
}

func (m *Materializer) materializeStudio(ctx context.Context, studio string, series []*Series, closures []*Closure, today Date) (int64, error) {
	scope := tenancy.Of(studio)

	zone, err := m.zones.ZoneOf(ctx, studio)
	if err != nil {
		return 0, platformerrors.Wrapf(err, "resolving the zone for studio %q", studio)
	}

	settings, err := m.store.GetSettings(ctx, m.client.Reader(), scope)
	if err != nil {
		return 0, platformerrors.Wrapf(err, "reading settings for studio %q", studio)
	}

	horizonWeeks := settings.HorizonWeeks
	if horizonWeeks < 1 || horizonWeeks > MaxHorizonWeeks {
		horizonWeeks = DefaultSettings(studio).HorizonWeeks
	}

	// Archived offerings are included: a studio that retires an offering has
	// stopped selling it, not stopped teaching the lessons already sold.
	offerings, err := m.store.ListOfferings(ctx, m.client.Reader(), scope, true)
	if err != nil {
		return 0, platformerrors.Wrapf(err, "reading offerings for studio %q", studio)
	}

	durations := make(map[string]time.Duration, len(offerings))
	for _, offering := range offerings {
		durations[offering.ID] = offering.Duration
	}

	horizon := today.AddDays(horizonWeeks * 7)

	var pending []*Occurrence

	for _, one := range series {
		duration, known := durations[one.OfferingID]
		if !known || duration <= 0 {
			return 0, platformerrors.Wrapf(ErrInvalidSeries, "series %q names offering %q, which the studio does not have", one.ID, one.OfferingID)
		}

		for _, date := range Plan(one, today, horizon, closures) {
			startsAt := date.At(one.StartTime, zone)

			pending = append(pending, &Occurrence{
				SeriesID:   one.ID,
				OfferingID: one.OfferingID,
				StartsAt:   startsAt,
				EndsAt:     startsAt.Add(duration),
				Staff:      one.Staff,
			})
		}
	}

	if len(pending) == 0 {
		return 0, nil
	}

	var written int64

	if err = m.client.WithTransaction(ctx, func(tx database.Tx) error {
		for _, occurrence := range pending {
			_, wrote, writeErr := m.store.MaterializeOccurrence(ctx, tx, scope, occurrence)
			if writeErr != nil {
				return writeErr
			}

			if wrote {
				written++
			}
		}

		return nil
	}); err != nil {
		return 0, platformerrors.Wrapf(err, "writing occurrences for studio %q", studio)
	}

	return written, nil
}
