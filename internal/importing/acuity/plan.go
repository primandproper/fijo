// Package acuity reads Acuity Scheduling's two CSV exports, the client list
// and the appointments report, into an import plan.
//
// Acuity keeps one flat record per name booked, and the first studio's plan
// has no API, so everything here is inference from those two files:
//
//   - People are their names. Records that share an email or a phone are one
//     household, because a parent who books for two children leaves two
//     records with one email.
//   - Whoever has an appointment ahead is a student. A reach (email or phone)
//     carried by one person alone is theirs, which makes an adult student
//     their own contact; one carried by several goes to a person on the
//     household who takes no lessons, and when there is nobody like that (a
//     parent who booked under the children's names) to a contact the plan
//     names "<household> family" for the owner to correct.
//   - A person's upcoming appointments of one type, on one weekday at one
//     time, at one- or two-week spacing, are a standing lesson starting on
//     the first of them. Anything else is a leftover with its reason.
//
// The headers this reads are a guess until a real export is in hand; see
// headers.go.
package acuity

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/primandproper/fijo/internal/domain/roster"
	"github.com/primandproper/fijo/internal/domain/schedule"
	"github.com/primandproper/fijo/internal/importing"

	platformerrors "github.com/primandproper/primitives-go/v2/errors"
)

// The file names a plan's columns are reported under.
const (
	ClientsFile      = "clients"
	AppointmentsFile = "appointments"
)

// defaultLength is an offering's length when no appointment of its type said.
const defaultLength = 30 * time.Minute

// maxRowWarnings caps the per-row warnings a plan carries, so a file of five
// hundred bad phone numbers is one screen and a count rather than five
// hundred lines.
const maxRowWarnings = 15

// Input is what a plan is made from.
type Input struct {
	// Clients and Appointments are the two files' text. Either may be empty,
	// but not both.
	Clients      string
	Appointments string

	// Language is what every household starts in. Acuity does not know.
	Language roster.Language

	// Today is the studio's today: appointments before it are history, read
	// only for the people and how to reach them.
	Today schedule.Date
}

// person is everybody the files mention by one name.
type person struct {
	key    string
	name   string
	last   string
	emails []string
	phones []string
	notes  []string
	future []*record
}

// planner holds one plan's working state.
type planner struct {
	people   map[string]*person
	ordered  []*person
	warnings []string
	in       Input
	dropped  int
}

// Plan reads the files into a plan.
func Plan(in Input) (*importing.Plan, error) {
	if strings.TrimSpace(in.Clients) == "" && strings.TrimSpace(in.Appointments) == "" {
		return nil, platformerrors.Wrap(ErrUnreadable, "Choose the client list, the appointments report, or both")
	}

	if !in.Language.Valid() {
		in.Language = roster.English
	}

	p := &planner{in: in, people: map[string]*person{}}
	plan := &importing.Plan{}

	if strings.TrimSpace(in.Clients) != "" {
		records, cols, unread, err := parseClients(in.Clients)
		if err != nil {
			return nil, err
		}

		plan.Columns = append(plan.Columns, report(ClientsFile, cols, clientFields)...)
		p.unread(ClientsFile, unread)

		for _, r := range records {
			who := p.person(r)
			if r.notes != "" {
				who.notes = appendNew(who.notes, r.notes)
			}
		}
	}

	if strings.TrimSpace(in.Appointments) != "" {
		records, cols, unread, unparsed, err := parseAppointments(in.Appointments)
		if err != nil {
			return nil, err
		}

		plan.Columns = append(plan.Columns, report(AppointmentsFile, cols, appointmentFields)...)
		p.unread(AppointmentsFile, unread)
		p.appointments(records, unparsed)
	}

	p.households(plan)
	p.lessons(plan)

	if p.dropped > 0 {
		p.warnings = append(p.warnings, fmt.Sprintf("%d more rows had an email or phone that could not be read.", p.dropped))
	}

	plan.Warnings = p.warnings

	return plan, nil
}

// report is which header each field was read from.
func report(file string, cols *columns, fields []field) []*importing.Column {
	out := make([]*importing.Column, 0, len(fields))

	for _, f := range fields {
		out = append(out, &importing.Column{File: file, Field: string(f), Header: cols.header(f)})
	}

	return out
}

func (p *planner) unread(file string, headers []string) {
	if len(headers) > 0 {
		p.warnings = append(p.warnings, fmt.Sprintf("Columns in the %s file that were not read: %s.", file, strings.Join(headers, ", ")))
	}
}

func (p *planner) rowWarningf(format string, args ...any) {
	if len(p.warnings) < maxRowWarnings {
		p.warnings = append(p.warnings, fmt.Sprintf(format, args...))

		return
	}

	p.dropped++
}

// person finds or makes the person a record names, and takes its reach.
func (p *planner) person(r *record) *person {
	key := strings.ToLower(r.name)

	who, ok := p.people[key]
	if !ok {
		who = &person{key: key, name: r.name, last: r.last}
		p.people[key] = who
		p.ordered = append(p.ordered, who)
	}

	if who.last == "" {
		who.last = r.last
	}

	if r.email != "" {
		if email, err := roster.NormalizeEmail(r.email); err == nil {
			who.emails = appendNew(who.emails, email)
		} else {
			p.rowWarningf("Row %d: %q is not an email address, so it was left off %s.", r.line, r.email, r.name)
		}
	}

	if r.phone != "" {
		if phone, err := roster.NormalizePhone(r.phone); err == nil {
			who.phones = appendNew(who.phones, phone)
		} else {
			p.rowWarningf("Row %d: %q is not a phone number we can read, so it was left off %s.", r.line, r.phone, r.name)
		}
	}

	return who
}

// appointments files each appointment under its person, keeping the upcoming
// ones and counting the rest.
func (p *planner) appointments(records []*record, unparsed int) {
	var canceled, past int

	seen := map[string]bool{}

	for _, r := range records {
		who := p.person(r)

		switch {
		case r.canceled:
			canceled++
		case !r.scheduled:
		case r.date.Before(p.in.Today):
			past++
		default:
			// The same appointment twice (an export run over overlapping
			// ranges and pasted together) is one appointment.
			if r.id != "" {
				if seen[r.id] {
					continue
				}

				seen[r.id] = true
			}

			who.future = append(who.future, r)
		}
	}

	if canceled > 0 {
		p.warnings = append(p.warnings, fmt.Sprintf("%d canceled appointments were left out.", canceled))
	}

	if past > 0 {
		p.warnings = append(p.warnings, fmt.Sprintf("%d past appointments were read only for who the clients are.", past))
	}

	if unparsed > 0 {
		p.warnings = append(p.warnings, fmt.Sprintf("%d appointments had a time we could not read and were left out.", unparsed))
	}
}

// households groups people who share an email or a phone into households.
func (p *planner) households(plan *importing.Plan) {
	groups := newUnion()

	for _, who := range p.ordered {
		groups.add("p:" + who.key)

		for _, email := range who.emails {
			groups.join("p:"+who.key, "e:"+email)
		}

		for _, phone := range who.phones {
			groups.join("p:"+who.key, "t:"+phone)
		}
	}

	members := map[string][]*person{}

	var roots []string

	for _, who := range p.ordered {
		root := groups.find("p:" + who.key)
		if _, ok := members[root]; !ok {
			roots = append(roots, root)
		}

		members[root] = append(members[root], who)
	}

	for _, root := range roots {
		plan.Households = append(plan.Households, p.household(members[root]))
	}
}

// household plans one group's household: its name, contacts and students.
func (p *planner) household(people []*person) *importing.Household {
	keys := make([]string, 0, len(people))
	for _, who := range people {
		keys = append(keys, who.key)
	}

	slices.Sort(keys)

	household := &importing.Household{
		Key:      "h:" + keys[0],
		Name:     householdName(people),
		Language: p.in.Language,
		Notes:    householdNotes(people),
	}

	for _, who := range people {
		if len(who.future) > 0 {
			household.Active = true
			household.Attendees = append(household.Attendees, &importing.Attendee{Key: attendeeKey(who), Name: who.name})
		}
	}

	household.Contacts, household.Warnings = contactsOf(household, people)

	if len(household.Contacts) == 0 {
		household.Warnings = append(household.Warnings, "Nobody here has an email or a phone, so they will get no reminders.")
	}

	if len(people) > 1 && !household.Active {
		household.Warnings = append(household.Warnings, "Grouped because they share an email or phone. Untick if that is wrong.")
	}

	return household
}

func attendeeKey(who *person) string { return "a:" + who.key }

// householdName is the shared last name ("García") when everybody has it, and
// the first person's whole name otherwise.
func householdName(people []*person) string {
	if len(people) == 1 {
		return people[0].name
	}

	last := people[0].last
	for _, who := range people[1:] {
		if !strings.EqualFold(who.last, last) {
			last = ""
		}
	}

	if last != "" {
		return last
	}

	return people[0].name
}

func householdNotes(people []*person) string {
	if len(people) == 1 {
		return strings.Join(people[0].notes, "\n")
	}

	var lines []string

	for _, who := range people {
		for _, note := range who.notes {
			lines = append(lines, who.name+": "+note)
		}
	}

	return strings.Join(lines, "\n")
}

// contactsOf decides who on a household gets the reminders, by who carries
// which email and phone. See the package documentation for the rule.
func contactsOf(household *importing.Household, people []*person) (contacts []*importing.Contact, warnings []string) {
	carriers := map[string]int{}

	for _, who := range people {
		for _, reach := range reachOf(who) {
			carriers[reach]++
		}
	}

	taken := map[string]bool{}

	byPerson := map[string]*importing.Contact{}

	// Each person who alone carries an email or a phone is a contact with it.
	for _, who := range people {
		email := firstOwned(who.emails, "e:", carriers, taken)
		phone := firstOwned(who.phones, "t:", carriers, taken)

		if email == "" && phone == "" {
			continue
		}

		contact := &importing.Contact{Name: who.name, Email: email, Phone: phone, Relationship: roster.Guardian}
		if len(who.future) > 0 {
			contact.Relationship = roster.Self
			contact.AttendeeKey = attendeeKey(who)
		}

		contacts = append(contacts, contact)
		byPerson[who.key] = contact
	}

	// What several people carry goes to somebody who takes no lessons, or
	// to a contact the plan has to name.
	var sharedEmail, sharedPhone string

	for _, who := range people {
		sharedEmail = firstElse(sharedEmail, firstShared(who.emails, "e:", carriers, taken))
		sharedPhone = firstElse(sharedPhone, firstShared(who.phones, "t:", carriers, taken))
	}

	if sharedEmail != "" || sharedPhone != "" {
		holder, promoted := sharedHolder(people, byPerson)

		switch {
		case holder != nil && (holder.Email == "" || sharedEmail == "") && (holder.Phone == "" || sharedPhone == ""):
			holder.Email = firstElse(holder.Email, sharedEmail)
			holder.Phone = firstElse(holder.Phone, sharedPhone)

			if promoted {
				contacts = append(contacts, holder)
			}
		default:
			contacts = append(contacts, &importing.Contact{
				Name:         household.Name + " family",
				Email:        sharedEmail,
				Phone:        sharedPhone,
				Relationship: roster.Guardian,
				GuessedName:  true,
			})
			warnings = append(warnings, "Acuity has no name for whoever owns the shared email or phone, so the contact is called “"+household.Name+" family”. Rename them after the import.")
		}

		taken["e:"+sharedEmail], taken["t:"+sharedPhone] = true, true
	}

	for _, who := range people {
		for _, reach := range reachOf(who) {
			if !taken[reach] {
				warnings = append(warnings, fmt.Sprintf("%s also has %s, which was not kept: a contact holds one email and one phone.", who.name, reach[2:]))
				taken[reach] = true
			}
		}
	}

	return contacts, warnings
}

// sharedHolder is the contact that takes the shared reach: the first contact
// who takes no lessons, or else a person who takes no lessons and owns
// nothing yet, made a contact (promoted, which the caller lists).
func sharedHolder(people []*person, byPerson map[string]*importing.Contact) (holder *importing.Contact, promoted bool) {
	for _, who := range people {
		if contact, ok := byPerson[who.key]; ok && len(who.future) == 0 {
			return contact, false
		}
	}

	for _, who := range people {
		if _, ok := byPerson[who.key]; !ok && len(who.future) == 0 {
			contact := &importing.Contact{Name: who.name, Relationship: roster.Guardian}
			byPerson[who.key] = contact

			return contact, true
		}
	}

	return nil, false
}

func reachOf(who *person) []string {
	out := make([]string, 0, len(who.emails)+len(who.phones))
	for _, email := range who.emails {
		out = append(out, "e:"+email)
	}

	for _, phone := range who.phones {
		out = append(out, "t:"+phone)
	}

	return out
}

func firstOwned(values []string, prefix string, carriers map[string]int, taken map[string]bool) string {
	for _, value := range values {
		if carriers[prefix+value] == 1 && !taken[prefix+value] {
			taken[prefix+value] = true

			return value
		}
	}

	return ""
}

func firstShared(values []string, prefix string, carriers map[string]int, taken map[string]bool) string {
	for _, value := range values {
		if carriers[prefix+value] > 1 && !taken[prefix+value] {
			return value
		}
	}

	return ""
}

func firstElse(value, otherwise string) string {
	if value != "" {
		return value
	}

	return otherwise
}

// slot is what makes appointments one standing lesson.
type slot struct {
	kind    string
	weekday time.Weekday
	start   schedule.ClockTime
}

// lessons reads each student's upcoming appointments into standing lessons
// and leftovers, and the lessons' types into offerings.
func (p *planner) lessons(plan *importing.Plan) {
	householdOf := map[string]string{}

	for _, household := range plan.Households {
		for _, attendee := range household.Attendees {
			householdOf[attendee.Key] = household.Key
		}
	}

	kinds := map[string][]*record{}

	var kindOrder []string

	for _, who := range p.ordered {
		bySlot := map[slot][]*record{}

		var slots []slot

		for _, r := range who.future {
			s := slot{kind: kindName(r.kind), weekday: r.date.Weekday(), start: r.start}
			if _, ok := bySlot[s]; !ok {
				slots = append(slots, s)
			}

			bySlot[s] = append(bySlot[s], r)
		}

		for i := range slots {
			s := &slots[i]

			key := strings.ToLower(s.kind)
			if _, ok := kinds[key]; !ok {
				kindOrder = append(kindOrder, key)
			}

			kinds[key] = append(kinds[key], bySlot[*s]...)

			lesson, leftover := p.lesson(who, s, bySlot[*s])
			if leftover != nil {
				plan.Leftovers = append(plan.Leftovers, leftover)

				continue
			}

			lesson.HouseholdKey = householdOf[lesson.AttendeeKey]
			plan.Lessons = append(plan.Lessons, lesson)
		}
	}

	for _, key := range kindOrder {
		plan.Offerings = append(plan.Offerings, offeringOf(key, kinds[key]))
	}
}

// kindName is an appointment type, or "Lesson" for a row without one.
func kindName(kind string) string {
	if kind == "" {
		return "Lesson"
	}

	return kind
}

// lesson reads one person's appointments in one slot as a standing lesson, or
// says why they are not one.
func (p *planner) lesson(who *person, s *slot, records []*record) (lesson *importing.Lesson, leftover *importing.Leftover) {
	dates := make([]schedule.Date, 0, len(records))
	for _, r := range records {
		if !slices.Contains(dates, r.date) {
			dates = append(dates, r.date)
		}
	}

	slices.SortFunc(dates, func(a, b schedule.Date) int { return schedule.DaysBetween(b, a) })

	what := fmt.Sprintf("%s, %ss at %s", s.kind, s.weekday, clock12(s.start))

	if len(dates) == 1 {
		return nil, &importing.Leftover{
			Who: who.name, What: what, Dates: dates,
			Reason: "Only one appointment ahead, so it is not clear it repeats. Add it by hand if it does.",
		}
	}

	// Every gap is a whole number of weeks, since every date is on the same
	// weekday; the spacing is their greatest common divisor.
	spacing := 0
	for i := 1; i < len(dates); i++ {
		spacing = gcd(spacing, schedule.DaysBetween(dates[i-1], dates[i]))
	}

	weeks := spacing / 7
	if weeks != 1 && weeks != 2 {
		return nil, &importing.Leftover{
			Who: who.name, What: what, Dates: dates,
			Reason: fmt.Sprintf("These are every %d weeks, and a standing lesson is weekly or every two weeks.", weeks),
		}
	}

	lesson = &importing.Lesson{
		Key:           fmt.Sprintf("l:%s|%s|%d|%d", who.key, strings.ToLower(s.kind), int(s.weekday), s.start.Minutes()),
		AttendeeKey:   attendeeKey(who),
		AttendeeName:  who.name,
		OfferingKey:   "o:" + strings.ToLower(s.kind),
		Weekday:       s.weekday,
		StartTime:     s.start,
		IntervalWeeks: weeks,
		StartsOn:      dates[0],
		Price:         modePrice(records),
		Seen:          len(dates),
	}

	expected := schedule.DaysBetween(dates[0], dates[len(dates)-1])/spacing + 1
	if missing := expected - len(dates); missing > 0 {
		lesson.Warnings = append(lesson.Warnings, fmt.Sprintf("Acuity has no appointment in %d of the weeks between %s and %s. Fijo will put a lesson in each; cancel the ones that are off.", missing, dates[0], dates[len(dates)-1]))
	}

	return lesson, nil
}

// offeringOf is what a type's appointments say it is: the length and price
// most of them had.
func offeringOf(key string, records []*record) *importing.Offering {
	offering := &importing.Offering{
		Key:       "o:" + key,
		Name:      kindName(records[0].kind),
		Duration:  modeDuration(records),
		ListPrice: modePrice(records),
	}

	if offering.Duration == 0 {
		offering.Duration = defaultLength
	}

	return offering
}

func modePrice(records []*record) int64 {
	counts := map[int64]int{}
	best, bestCount := int64(-1), 0

	for _, r := range records {
		if r.price < 0 {
			continue
		}

		counts[r.price]++
		if c := counts[r.price]; c > bestCount || (c == bestCount && r.price < best) {
			best, bestCount = r.price, c
		}
	}

	return best
}

func modeDuration(records []*record) time.Duration {
	counts := map[time.Duration]int{}

	var (
		best      time.Duration
		bestCount int
	)

	for _, r := range records {
		if r.duration <= 0 {
			continue
		}

		counts[r.duration]++
		if c := counts[r.duration]; c > bestCount || (c == bestCount && r.duration < best) {
			best, bestCount = r.duration, c
		}
	}

	// Durations are whole minutes in Fijo.
	return best.Truncate(time.Minute)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}

	if a < 0 {
		return -a
	}

	return a
}

// clock12 is "4:00 pm", which is how an owner says a lesson's time.
func clock12(c schedule.ClockTime) string {
	return time.Date(2000, 1, 1, c.Hour, c.Minute, 0, 0, time.UTC).Format("3:04 pm")
}

func appendNew(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}

	return append(values, value)
}

// union is a disjoint set over string keys: who shares a reach with whom.
type union struct {
	parent map[string]string
}

func newUnion() *union { return &union{parent: map[string]string{}} }

func (u *union) add(key string) {
	if _, ok := u.parent[key]; !ok {
		u.parent[key] = key
	}
}

func (u *union) find(key string) string {
	u.add(key)

	for u.parent[key] != key {
		u.parent[key] = u.parent[u.parent[key]]
		key = u.parent[key]
	}

	return key
}

// join merges two keys' sets. The root is the earlier-added key's, so a
// household's identity does not depend on which of its reaches came last.
func (u *union) join(a, b string) {
	rootA, rootB := u.find(a), u.find(b)
	if rootA != rootB {
		u.parent[rootB] = rootA
	}
}
