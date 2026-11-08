package importing

import (
	"context"
	"strings"

	"github.com/primandproper/fijo/internal/domain/roster"
	"github.com/primandproper/fijo/internal/domain/schedule"

	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

// The refusals.
var (
	ErrNilRoster   = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil roster store")
	ErrNilSchedule = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil schedule store")
	ErrNilPlan     = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil plan")
	ErrNoStaff     = platformerrors.New("a standing lesson needs somebody to teach it")
)

// Grid is the slice of the materializer a write uses: a new standing slot gets
// its rows the way one made by hand does.
type Grid interface {
	SyncSeries(ctx context.Context, tx database.Tx, scope tenancy.Scope, seriesID string) (written, retracted int64, err error)
}

// Writer annotates and writes plans.
type Writer struct {
	roster   roster.Store
	schedule schedule.Store
	grid     Grid
}

// NewWriter builds a writer. grid may be nil, in which case a written series
// waits for the materializer's sweep to get its rows.
func NewWriter(rosterStore roster.Store, scheduleStore schedule.Store, grid Grid) (*Writer, error) {
	switch {
	case rosterStore == nil:
		return nil, ErrNilRoster
	case scheduleStore == nil:
		return nil, ErrNilSchedule
	}

	return &Writer{roster: rosterStore, schedule: scheduleStore, grid: grid}, nil
}

// Choices are what the owner decided on the preview.
type Choices struct {
	// SkipHouseholds and SkipLessons are keys the owner unticked. Skipping a
	// household skips its lessons.
	SkipHouseholds map[string]bool
	SkipLessons    map[string]bool

	// Staff teaches every lesson written: the owner who ran the import.
	Staff []string

	// IncludeInactive writes households with nothing ahead of them too.
	IncludeInactive bool
}

// Result is what a write did.
type Result struct {
	HouseholdsWritten  int
	HouseholdsOnRoster int
	HouseholdsSkipped  int
	OfferingsWritten   int
	LessonsWritten     int
}

// Annotate marks the households already on the studio's roster and the
// offerings the studio already sells by name.
func (w *Writer) Annotate(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, plan *Plan) error {
	if plan == nil {
		return ErrNilPlan
	}

	households, err := w.roster.ListHouseholds(ctx, q, scope, false)
	if err != nil {
		return platformerrors.Wrap(err, "reading the roster")
	}

	reachable := map[string]bool{}

	for _, household := range households {
		for _, contact := range household.Contacts {
			if contact.Email != "" {
				reachable["e:"+contact.Email] = true
			}

			if contact.Phone != "" {
				reachable["t:"+contact.Phone] = true
			}
		}
	}

	for _, household := range plan.Households {
		household.OnRoster = false

		for _, contact := range household.Contacts {
			if (contact.Email != "" && reachable["e:"+contact.Email]) || (contact.Phone != "" && reachable["t:"+contact.Phone]) {
				household.OnRoster = true
			}
		}
	}

	offerings, err := w.schedule.ListOfferings(ctx, q, scope, false)
	if err != nil {
		return platformerrors.Wrap(err, "reading the offerings")
	}

	sold := map[string]string{}

	for _, offering := range offerings {
		for _, name := range []string{offering.NameEN, offering.NameES} {
			if key := strings.ToLower(strings.TrimSpace(name)); key != "" {
				if _, taken := sold[key]; !taken {
					sold[key] = offering.ID
				}
			}
		}
	}

	for _, offering := range plan.Offerings {
		offering.ExistingID = sold[strings.ToLower(strings.TrimSpace(offering.Name))]
	}

	return nil
}

// Write annotates the plan against the roster as the transaction sees it and
// writes what the choices leave in: households, then the offerings their
// lessons need, then the lessons with their rows.
func (w *Writer) Write(ctx context.Context, tx database.Tx, scope tenancy.Scope, plan *Plan, choices Choices) (*Result, error) {
	if plan == nil {
		return nil, ErrNilPlan
	}

	if err := w.Annotate(ctx, tx, scope, plan); err != nil {
		return nil, err
	}

	result := &Result{}

	// attendees maps a planned attendee's key to the row written for them;
	// a lesson whose attendee is missing from it is on a household that was
	// not written.
	attendees := map[string]string{}

	for _, household := range plan.Households {
		switch {
		case household.OnRoster:
			result.HouseholdsOnRoster++

			continue
		case choices.SkipHouseholds[household.Key], !household.Active && !choices.IncludeInactive:
			result.HouseholdsSkipped++

			continue
		}

		if err := w.writeHousehold(ctx, tx, scope, household, attendees); err != nil {
			return nil, platformerrors.Wrapf(err, "adding %s", household.Name)
		}

		result.HouseholdsWritten++
	}

	offerings := map[string]*Offering{}
	for _, offering := range plan.Offerings {
		offerings[offering.Key] = offering
	}

	written := map[string]*schedule.Offering{}

	for _, lesson := range plan.Lessons {
		attendeeID, ok := attendees[lesson.AttendeeKey]
		if !ok || choices.SkipLessons[lesson.Key] {
			continue
		}

		if len(choices.Staff) == 0 {
			return nil, ErrNoStaff
		}

		offering, err := w.offeringFor(ctx, tx, scope, offerings[lesson.OfferingKey], written, result)
		if err != nil {
			return nil, err
		}

		series := &schedule.Series{
			OfferingID:    offering.ID,
			Staff:         choices.Staff,
			Attendees:     []string{attendeeID},
			StartsOn:      lesson.StartsOn,
			StartTime:     lesson.StartTime,
			Weekday:       lesson.Weekday,
			IntervalWeeks: lesson.IntervalWeeks,
		}

		if lesson.Price >= 0 && lesson.Price != offering.ListPrice {
			price := lesson.Price
			series.PriceOverride = &price
		}

		created, err := w.schedule.CreateSeries(ctx, tx, scope, series)
		if err != nil {
			return nil, platformerrors.Wrapf(err, "adding %s's lesson", lesson.AttendeeName)
		}

		if w.grid != nil {
			if _, _, err = w.grid.SyncSeries(ctx, tx, scope, created.ID); err != nil {
				return nil, platformerrors.Wrapf(err, "scheduling %s's lesson", lesson.AttendeeName)
			}
		}

		result.LessonsWritten++
	}

	return result, nil
}

// writeHousehold writes one household and its people, recording each
// attendee's row under its planned key.
func (w *Writer) writeHousehold(ctx context.Context, tx database.Tx, scope tenancy.Scope, planned *Household, attendees map[string]string) error {
	household, err := w.roster.CreateHousehold(ctx, tx, scope, &roster.Household{
		Name:     planned.Name,
		Language: planned.Language,
		Notes:    planned.Notes,
	})
	if err != nil {
		return err
	}

	// An adult student is a contact first and an attendee linked to it.
	contactOf := map[string]string{}

	for _, planned := range planned.Contacts {
		contact, createErr := w.roster.CreateContact(ctx, tx, scope, &roster.Contact{
			HouseholdID:  household.ID,
			Name:         planned.Name,
			Email:        planned.Email,
			Phone:        planned.Phone,
			Language:     household.Language,
			Relationship: planned.Relationship,
		})
		if createErr != nil {
			return createErr
		}

		if planned.AttendeeKey != "" {
			contactOf[planned.AttendeeKey] = contact.ID
		}
	}

	for _, planned := range planned.Attendees {
		attendee, createErr := w.roster.CreateAttendee(ctx, tx, scope, &roster.Attendee{
			HouseholdID: household.ID,
			Name:        planned.Name,
			Language:    household.Language,
			ContactID:   contactOf[planned.Key],
		})
		if createErr != nil {
			return createErr
		}

		attendees[planned.Key] = attendee.ID
	}

	return nil
}

// offeringFor is the offering a lesson is sold as: the studio's own when the
// names matched, else one written the first time a lesson needs it.
func (w *Writer) offeringFor(ctx context.Context, tx database.Tx, scope tenancy.Scope, planned *Offering, written map[string]*schedule.Offering, result *Result) (*schedule.Offering, error) {
	if planned == nil {
		return nil, platformerrors.New("a lesson names an offering the plan does not have")
	}

	if offering, ok := written[planned.Key]; ok {
		return offering, nil
	}

	var (
		offering *schedule.Offering
		err      error
	)

	if planned.ExistingID != "" {
		offering, err = w.schedule.GetOffering(ctx, tx, scope, planned.ExistingID)
	} else {
		// The Spanish name starts as the English one; the owner translates
		// it on the offerings page.
		offering, err = w.schedule.CreateOffering(ctx, tx, scope, &schedule.Offering{
			NameEN:    planned.Name,
			NameES:    planned.Name,
			Kind:      schedule.KindSingle,
			Duration:  planned.Duration,
			ListPrice: max(planned.ListPrice, 0),
		})
		if err == nil {
			result.OfferingsWritten++
		}
	}

	if err != nil {
		return nil, platformerrors.Wrapf(err, "adding the offering %s", planned.Name)
	}

	written[planned.Key] = offering

	return offering, nil
}
