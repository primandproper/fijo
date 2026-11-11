package importing

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/primandproper/fijo/internal/domain/roster"
	"github.com/primandproper/fijo/internal/domain/schedule"

	"github.com/primandproper/platform-go/v15/audit"
	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/identifiers"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

// ResourceImport is what the audit entry for a whole import run names. The
// rows it wrote have their own entries: the roster's through its hooks, a
// one-off's as the import's own.
const ResourceImport = "importing.run"

// The metadata keys an import run's entry carries.
const (
	KeySource              = "source"
	KeyHouseholdsWritten   = "households_written"
	KeyHouseholdsUpdated   = "households_updated"
	KeyLessonsWritten      = "lessons_written"
	KeyAppointmentsWritten = "appointments_written"
	KeyOfferingsWritten    = "offerings_written"
)

// The refusals.
var (
	ErrNilRoster   = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil roster store")
	ErrNilSchedule = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil schedule store")
	ErrNilZones    = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil zone lookup")
	ErrNilRecorder = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil audit recorder")
	ErrNilPlan     = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil plan")
	ErrNoStaff     = platformerrors.New("a lesson needs somebody to teach it")
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
	zones    schedule.Zones
	recorder audit.Recorder
	grid     Grid
}

// NewWriter builds a writer. The zones say what instant an appointment's wall
// clock is, and the recorder is where a run and the one-offs it books are
// recorded. grid may be nil, in which case a written series waits for the
// materializer's sweep to get its rows.
func NewWriter(rosterStore roster.Store, scheduleStore schedule.Store, zones schedule.Zones, recorder audit.Recorder, grid Grid) (*Writer, error) {
	switch {
	case rosterStore == nil:
		return nil, ErrNilRoster
	case scheduleStore == nil:
		return nil, ErrNilSchedule
	case zones == nil:
		return nil, ErrNilZones
	case recorder == nil:
		return nil, ErrNilRecorder
	}

	return &Writer{roster: rosterStore, schedule: scheduleStore, zones: zones, recorder: recorder, grid: grid}, nil
}

// Choices are what the owner decided on the preview.
type Choices struct {
	SkipHouseholds   map[string]bool
	SkipLessons      map[string]bool
	SkipAppointments map[string]bool
	Actor            string
	Source           string
	Staff            []string
	IncludeInactive  bool
}

// Result is what a write did.
type Result struct {
	// HouseholdsWritten are new; HouseholdsUpdated were already here and had
	// something added; HouseholdsOnRoster were already here and had nothing
	// new.
	HouseholdsWritten   int
	HouseholdsUpdated   int
	HouseholdsOnRoster  int
	HouseholdsSkipped   int
	OfferingsWritten    int
	LessonsWritten      int
	AppointmentsWritten int
}

// studio is what the studio already has, as an annotation found it.
type studio struct {
	households map[string]*roster.Household

	// zone is the studio's, and nil when the plan has no appointments.
	zone *time.Location
}

// match is a live contact and the household it is on.
type match struct {
	contact   *roster.Contact
	household *roster.Household
}

// Annotate marks what of the plan the studio already has: the households,
// their people, their standing lessons and their appointments, and the
// offerings it already sells by name.
func (w *Writer) Annotate(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, plan *Plan) error {
	_, err := w.annotate(ctx, q, scope, plan)

	return err
}

func (w *Writer) annotate(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, plan *Plan) (*studio, error) {
	if plan == nil {
		return nil, ErrNilPlan
	}

	households, err := w.roster.ListHouseholds(ctx, q, scope, false)
	if err != nil {
		return nil, platformerrors.Wrap(err, "reading the roster")
	}

	known := &studio{households: make(map[string]*roster.Household, len(households))}
	reachable := map[string]match{}

	for _, household := range households {
		known.households[household.ID] = household

		for _, contact := range household.Contacts {
			if contact.Email != "" {
				reachable["e:"+contact.Email] = match{contact: contact, household: household}
			}

			if contact.Phone != "" {
				reachable["t:"+contact.Phone] = match{contact: contact, household: household}
			}
		}
	}

	for _, household := range plan.Households {
		annotateHousehold(household, reachable, known.households)
	}

	if err = w.annotateOfferings(ctx, q, scope, plan); err != nil {
		return nil, err
	}

	if err = w.annotateLessons(ctx, q, scope, plan, known); err != nil {
		return nil, err
	}

	if len(plan.Appointments) > 0 {
		if known.zone, err = w.zones.ZoneOf(ctx, scope.Owner()); err != nil {
			return nil, platformerrors.Wrap(err, "reading the studio's time zone")
		}

		if err = w.annotateAppointments(ctx, q, scope, plan, known); err != nil {
			return nil, err
		}
	}

	return known, nil
}

// annotateHousehold finds the household already here that any of the planned
// contacts' emails or phones is on, and the people on it the plan names.
func annotateHousehold(planned *Household, reachable map[string]match, households map[string]*roster.Household) {
	planned.ExistingID, planned.ExistingName = "", ""

	for _, contact := range planned.Contacts {
		contact.ExistingID = ""

		for _, reach := range reachesOf(contact) {
			found, ok := reachable[reach]
			if !ok {
				continue
			}

			contact.ExistingID = found.contact.ID

			switch planned.ExistingID {
			case "":
				planned.ExistingID, planned.ExistingName = found.household.ID, found.household.Name
			case found.household.ID:
			default:
				warnOnce(planned, reach[2:]+" is on "+found.household.Name+", a different client here. Only what is missing from "+planned.ExistingName+" is added.")
			}

			break
		}
	}

	for _, attendee := range planned.Attendees {
		attendee.ExistingID = ""
	}

	existing, ok := households[planned.ExistingID]
	if !ok {
		return
	}

	byName := map[string]string{}
	byContact := map[string]string{}

	for _, attendee := range existing.Attendees {
		if key := nameKey(attendee.Name); key != "" {
			if _, taken := byName[key]; !taken {
				byName[key] = attendee.ID
			}
		}

		if attendee.ContactID != "" {
			byContact[attendee.ContactID] = attendee.ID
		}
	}

	// A planned adult student whose email is already on a contact here is
	// the attendee that contact is, whatever name the owner typed.
	contactOf := map[string]string{}

	for _, contact := range planned.Contacts {
		if contact.AttendeeKey != "" && contact.ExistingID != "" {
			contactOf[contact.AttendeeKey] = contact.ExistingID
		}
	}

	for _, attendee := range planned.Attendees {
		if id := byName[nameKey(attendee.Name)]; id != "" {
			attendee.ExistingID = id
		} else if id = byContact[contactOf[attendee.Key]]; id != "" {
			attendee.ExistingID = id
		}
	}
}

func (w *Writer) annotateOfferings(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, plan *Plan) error {
	offerings, err := w.schedule.ListOfferings(ctx, q, scope, false)
	if err != nil {
		return platformerrors.Wrap(err, "reading the offerings")
	}

	sold := map[string]string{}

	for _, offering := range offerings {
		for _, name := range []string{offering.NameEN, offering.NameES} {
			if key := nameKey(name); key != "" {
				if _, taken := sold[key]; !taken {
					sold[key] = offering.ID
				}
			}
		}
	}

	for _, offering := range plan.Offerings {
		offering.ExistingID = sold[nameKey(offering.Name)]
	}

	return nil
}

// annotateLessons marks a planned standing lesson the household already has:
// one of its people has a standing lesson on that weekday at that time,
// still running when the planned one would start. The weekday and the time
// are the match, not the person or the offering, because the owner may have
// typed the student's name or the offering differently.
func (w *Writer) annotateLessons(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, plan *Plan, known *studio) error {
	people := peopleOf(plan, known)

	var series []*schedule.Series

	for _, lesson := range plan.Lessons {
		lesson.OnRoster = false

		ids := people[lesson.HouseholdKey]
		if len(ids) == 0 {
			continue
		}

		if series == nil {
			var err error

			if series, err = w.schedule.ListSeries(ctx, q, scope, false); err != nil {
				return platformerrors.Wrap(err, "reading the standing lessons")
			}
		}

		for _, s := range series {
			if s.Weekday == lesson.Weekday && s.StartTime == lesson.StartTime &&
				(s.EndsOn.IsZero() || !s.EndsOn.Before(lesson.StartsOn)) && overlaps(s.Attendees, ids) {
				lesson.OnRoster = true

				break
			}
		}
	}

	return nil
}

// annotateAppointments marks a planned appointment the household already has:
// a lesson of one of its people starts at that moment, in any state. A lesson
// the owner cancelled or moved here counts, so a re-run does not put back
// what the owner took off.
func (w *Writer) annotateAppointments(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, plan *Plan, known *studio) error {
	people := peopleOf(plan, known)

	var from, until time.Time

	for _, appointment := range plan.Appointments {
		appointment.OnRoster = false

		if len(people[appointment.HouseholdKey]) == 0 {
			continue
		}

		at := appointment.On.At(appointment.StartTime, known.zone)
		if from.IsZero() || at.Before(from) {
			from = at
		}

		if at.After(until) {
			until = at
		}
	}

	if from.IsZero() {
		return nil
	}

	occurrences, err := w.schedule.ListOccurrencesInWindow(ctx, q, scope, from, until.Add(time.Minute))
	if err != nil {
		return platformerrors.Wrap(err, "reading the lessons already booked")
	}

	for _, appointment := range plan.Appointments {
		ids := people[appointment.HouseholdKey]
		if len(ids) == 0 {
			continue
		}

		at := appointment.On.At(appointment.StartTime, known.zone)

		for _, occurrence := range occurrences {
			if occurrence.StartsAt.Equal(at) && overlaps(occurrence.Attendees, ids) {
				appointment.OnRoster = true

				break
			}
		}
	}

	return nil
}

// peopleOf maps each planned household that is already here to the
// attendees it has here.
func peopleOf(plan *Plan, known *studio) map[string][]string {
	out := map[string][]string{}

	for _, household := range plan.Households {
		existing, ok := known.households[household.ExistingID]
		if !ok {
			continue
		}

		for _, attendee := range existing.Attendees {
			out[household.Key] = append(out[household.Key], attendee.ID)
		}
	}

	return out
}

// Write annotates the plan against the roster as the transaction sees it and
// writes what the choices leave in: each household (or what an existing one
// is missing), then the lessons and appointments it needs, with the
// offerings they are sold as.
func (w *Writer) Write(ctx context.Context, tx database.Tx, scope tenancy.Scope, plan *Plan, choices *Choices) (*Result, error) {
	if plan == nil {
		return nil, ErrNilPlan
	}

	if choices == nil {
		choices = &Choices{}
	}

	known, err := w.annotate(ctx, tx, scope, plan)
	if err != nil {
		return nil, err
	}

	run := &writing{
		Writer:    w,
		tx:        tx,
		scope:     scope,
		choices:   choices,
		known:     known,
		result:    &Result{},
		attendees: map[string]string{},
		offerings: map[string]*Offering{},
		written:   map[string]*schedule.Offering{},
	}

	for _, offering := range plan.Offerings {
		run.offerings[offering.Key] = offering
	}

	lessons := map[string][]*Lesson{}

	for _, lesson := range plan.Lessons {
		if !lesson.OnRoster && !choices.SkipLessons[lesson.Key] {
			lessons[lesson.HouseholdKey] = append(lessons[lesson.HouseholdKey], lesson)
		}
	}

	appointments := map[string][]*Appointment{}

	for _, appointment := range plan.Appointments {
		if !appointment.OnRoster && !choices.SkipAppointments[appointment.Key] {
			appointments[appointment.HouseholdKey] = append(appointments[appointment.HouseholdKey], appointment)
		}
	}

	for _, household := range plan.Households {
		if choices.SkipHouseholds[household.Key] || (!household.Active && !choices.IncludeInactive) {
			run.result.HouseholdsSkipped++

			continue
		}

		if err = run.household(ctx, household, lessons[household.Key], appointments[household.Key]); err != nil {
			return nil, platformerrors.Wrapf(err, "adding %s", household.Name)
		}
	}

	result := run.result

	if err = run.record(ctx, ResourceImport, identifiers.New(), map[string]string{
		KeySource:              choices.Source,
		KeyHouseholdsWritten:   strconv.Itoa(result.HouseholdsWritten),
		KeyHouseholdsUpdated:   strconv.Itoa(result.HouseholdsUpdated),
		KeyLessonsWritten:      strconv.Itoa(result.LessonsWritten),
		KeyAppointmentsWritten: strconv.Itoa(result.AppointmentsWritten),
		KeyOfferingsWritten:    strconv.Itoa(result.OfferingsWritten),
	}); err != nil {
		return nil, platformerrors.Wrap(err, "recording the import")
	}

	return result, nil
}

// writing is one Write's state.
type writing struct {
	tx database.Tx
	*Writer
	known   *studio
	result  *Result
	choices *Choices

	// attendees maps a planned attendee's key to their row, written or
	// already here.
	attendees map[string]string

	// offerings are the plan's by key, and written the rows each has become.
	offerings map[string]*Offering
	written   map[string]*schedule.Offering

	scope tenancy.Scope
}

// household writes one planned household, or what the one already here is
// missing, and then its lessons and appointments.
func (r *writing) household(ctx context.Context, planned *Household, lessons []*Lesson, appointments []*Appointment) error {
	if len(lessons)+len(appointments) > 0 && len(r.choices.Staff) == 0 {
		return ErrNoStaff
	}

	switch existing, ok := r.known.households[planned.ExistingID]; {
	case !ok:
		if err := r.newHousehold(ctx, planned); err != nil {
			return err
		}

		r.result.HouseholdsWritten++
	default:
		added, err := r.extendHousehold(ctx, existing, planned, lessons, appointments)
		if err != nil {
			return err
		}

		if added || len(lessons)+len(appointments) > 0 {
			r.result.HouseholdsUpdated++
		} else {
			r.result.HouseholdsOnRoster++
		}
	}

	for _, lesson := range lessons {
		if err := r.lesson(ctx, lesson); err != nil {
			return platformerrors.Wrapf(err, "adding %s's lesson", lesson.AttendeeName)
		}
	}

	for _, appointment := range appointments {
		if err := r.appointment(ctx, appointment); err != nil {
			return platformerrors.Wrapf(err, "booking %s's appointment on %s", appointment.AttendeeName, appointment.On)
		}
	}

	return nil
}

// newHousehold writes one household and all its people.
func (r *writing) newHousehold(ctx context.Context, planned *Household) error {
	household, err := r.roster.CreateHousehold(ctx, r.tx, r.scope, &roster.Household{
		Name:     planned.Name,
		Language: planned.Language,
		Notes:    planned.Notes,
	})
	if err != nil {
		return err
	}

	contactOf, err := r.contacts(ctx, household, planned.Contacts)
	if err != nil {
		return err
	}

	return r.people(ctx, household, planned.Attendees, contactOf)
}

// extendHousehold adds to a household already here the contacts whose email
// and phone are both new, and the people who have something to book and are
// not on it yet. It changes nothing already there, and reports whether it
// added anybody.
func (r *writing) extendHousehold(ctx context.Context, existing *roster.Household, planned *Household, lessons []*Lesson, appointments []*Appointment) (bool, error) {
	booked := map[string]bool{}

	for _, lesson := range lessons {
		booked[lesson.AttendeeKey] = true
	}

	for _, appointment := range appointments {
		booked[appointment.AttendeeKey] = true
	}

	var contacts []*Contact

	contactOf := map[string]string{}

	for _, contact := range planned.Contacts {
		switch {
		case !contact.OnRoster():
			contacts = append(contacts, contact)
		case contact.AttendeeKey != "":
			contactOf[contact.AttendeeKey] = contact.ExistingID
		}
	}

	written, err := r.contacts(ctx, existing, contacts)
	if err != nil {
		return false, err
	}

	maps.Copy(contactOf, written)

	var people []*Attendee

	for _, attendee := range planned.Attendees {
		switch {
		case attendee.OnRoster():
			r.attendees[attendee.Key] = attendee.ExistingID
		case booked[attendee.Key]:
			people = append(people, attendee)
		}
	}

	if err = r.people(ctx, existing, people, contactOf); err != nil {
		return false, err
	}

	return len(contacts)+len(people) > 0, nil
}

// contacts writes contacts onto a household, and maps each planned adult
// student among them to their contact's row.
func (r *writing) contacts(ctx context.Context, household *roster.Household, planned []*Contact) (map[string]string, error) {
	contactOf := map[string]string{}

	for _, contact := range planned {
		written, err := r.roster.CreateContact(ctx, r.tx, r.scope, &roster.Contact{
			HouseholdID:  household.ID,
			Name:         contact.Name,
			Email:        contact.Email,
			Phone:        contact.Phone,
			Language:     household.Language,
			Relationship: contact.Relationship,
		})
		if err != nil {
			return nil, err
		}

		if contact.AttendeeKey != "" {
			contactOf[contact.AttendeeKey] = written.ID
		}
	}

	return contactOf, nil
}

// people writes attendees onto a household, an adult student linked to the
// contact they are.
func (r *writing) people(ctx context.Context, household *roster.Household, planned []*Attendee, contactOf map[string]string) error {
	for _, attendee := range planned {
		written, err := r.roster.CreateAttendee(ctx, r.tx, r.scope, &roster.Attendee{
			HouseholdID: household.ID,
			Name:        attendee.Name,
			Language:    household.Language,
			ContactID:   contactOf[attendee.Key],
		})
		if err != nil {
			return err
		}

		r.attendees[attendee.Key] = written.ID
	}

	return nil
}

// lesson writes a standing lesson and puts it on the grid.
func (r *writing) lesson(ctx context.Context, lesson *Lesson) error {
	attendeeID, ok := r.attendees[lesson.AttendeeKey]
	if !ok {
		return platformerrors.New("a lesson names somebody the plan does not have")
	}

	offering, err := r.offeringFor(ctx, r.offerings[lesson.OfferingKey])
	if err != nil {
		return err
	}

	series := &schedule.Series{
		OfferingID:    offering.ID,
		Staff:         r.choices.Staff,
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

	created, err := r.schedule.CreateSeries(ctx, r.tx, r.scope, series)
	if err != nil {
		return err
	}

	if r.grid != nil {
		if _, _, err = r.grid.SyncSeries(ctx, r.tx, r.scope, created.ID); err != nil {
			return platformerrors.Wrap(err, "putting it on the grid")
		}
	}

	r.result.LessonsWritten++

	return nil
}

// appointment books a one-off at the appointment's wall clock in the
// studio's zone.
func (r *writing) appointment(ctx context.Context, appointment *Appointment) error {
	attendeeID, ok := r.attendees[appointment.AttendeeKey]
	if !ok {
		return platformerrors.New("an appointment names somebody the plan does not have")
	}

	offering, err := r.offeringFor(ctx, r.offerings[appointment.OfferingKey])
	if err != nil {
		return err
	}

	oneOff := &schedule.OneOff{
		OfferingID: offering.ID,
		StartsAt:   appointment.On.At(appointment.StartTime, r.known.zone),
		Duration:   appointment.Duration,
		Staff:      r.choices.Staff,
		Attendees:  []string{attendeeID},
	}

	occurrence, err := oneOff.Occurrence(offering)
	if err != nil {
		return err
	}

	written, err := r.schedule.CreateOccurrence(ctx, r.tx, r.scope, occurrence)
	if err != nil {
		return err
	}

	// A one-off booked by hand is Book's, and carries its entry; one booked
	// by an import carries the import's, so its history says where it came
	// from.
	if err = r.record(ctx, schedule.ResourceOccurrence, written.ID, map[string]string{
		schedule.KeyCommand:  schedule.CommandImport,
		schedule.KeyStartsAt: written.StartsAt.UTC().Format(time.RFC3339),
		KeySource:            r.choices.Source,
	}); err != nil {
		return err
	}

	r.result.AppointmentsWritten++

	return nil
}

// record writes one created-entry in the studio's scope, on the import's
// transaction.
func (r *writing) record(ctx context.Context, resourceType, id string, metadata map[string]string) error {
	actor := audit.Actor{ID: r.choices.Actor, Type: audit.ActorUser}
	if actor.ID == "" {
		actor = audit.Actor{ID: audit.ActorUnattributed, Type: audit.ActorSystem}
	}

	maps.DeleteFunc(metadata, func(_, value string) bool { return value == "" })

	return r.recorder.Record(ctx, r.tx, r.scope, &audit.Entry{
		ID:           identifiers.New(),
		ResourceType: resourceType,
		ResourceID:   id,
		EventType:    audit.EventCreated,
		Actor:        actor,
		Metadata:     metadata,
	})
}

// offeringFor is the offering a lesson is sold as: the studio's own when the
// names matched, else one written the first time a lesson needs it.
func (r *writing) offeringFor(ctx context.Context, planned *Offering) (*schedule.Offering, error) {
	if planned == nil {
		return nil, platformerrors.New("a lesson names an offering the plan does not have")
	}

	if offering, ok := r.written[planned.Key]; ok {
		return offering, nil
	}

	var (
		offering *schedule.Offering
		err      error
	)

	if planned.ExistingID != "" {
		offering, err = r.schedule.GetOffering(ctx, r.tx, r.scope, planned.ExistingID)
	} else {
		// The Spanish name starts as the English one; the owner translates
		// it on the offerings page.
		offering, err = r.schedule.CreateOffering(ctx, r.tx, r.scope, &schedule.Offering{
			NameEN:    planned.Name,
			NameES:    planned.Name,
			Kind:      schedule.KindSingle,
			Duration:  planned.Duration,
			ListPrice: max(planned.ListPrice, 0),
		})
		if err == nil {
			r.result.OfferingsWritten++
		}
	}

	if err != nil {
		return nil, platformerrors.Wrapf(err, "adding the offering %s", planned.Name)
	}

	r.written[planned.Key] = offering

	return offering, nil
}

func reachesOf(contact *Contact) []string {
	var out []string

	if contact.Email != "" {
		out = append(out, "e:"+contact.Email)
	}

	if contact.Phone != "" {
		out = append(out, "t:"+contact.Phone)
	}

	return out
}

func nameKey(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

func overlaps(a, b []string) bool {
	for _, id := range a {
		if slices.Contains(b, id) {
			return true
		}
	}

	return false
}

func warnOnce(household *Household, warning string) {
	if !slices.Contains(household.Warnings, warning) {
		household.Warnings = append(household.Warnings, warning)
	}
}
