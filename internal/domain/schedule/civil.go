package schedule

import (
	"fmt"
	"time"
)

// Date is a civil date: a year, a month and a day, with no zone and no time.
//
// A standing lesson is on a Tuesday, and a series starts on a day. Neither is
// an instant, and storing them as one is how a schedule drifts an hour twice a
// year. The instant is computed from a Date, a ClockTime and the studio's zone
// at the moment an occurrence is materialized, and only then.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf is the civil date t falls on, read in t's own location.
func DateOf(t time.Time) Date {
	year, month, day := t.Date()

	return Date{Year: year, Month: month, Day: day}
}

// Today is the civil date it is now in loc.
func Today(now time.Time, loc *time.Location) Date {
	return DateOf(now.In(orUTC(loc)))
}

// At is the instant this date and clock time name in loc.
//
// What a studio means by "Tuesdays at four" is four o'clock on the clock in
// the room, and that is what this returns on every Tuesday of the year,
// including the two when the clocks move. The UTC instant moves instead, which
// is the point of storing the rule as a wall clock.
//
// The two wall-clock times a year that are not ordinary are resolved by Go,
// and both answers are recorded here because they are product behavior rather
// than an implementation detail:
//
//   - An hour the spring forward skips does not exist. Go resolves it
//     backwards: 02:30 on the day US/Central springs forward comes back as
//     01:30 CST, an hour earlier than asked, not later.
//   - An hour the autumn back repeats happens twice. Go resolves it to the
//     first of the two, so the lesson is at the earlier 01:30 CDT and does not
//     happen again an hour later.
//
// Neither is a time anybody teaches at, which is why they are documented and
// tested rather than special-cased.
func (d Date) At(clock ClockTime, loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, clock.Hour, clock.Minute, 0, 0, orUTC(loc))
}

// Time renders the date for a DATE column: midnight UTC, which is how a driver
// round-trips one without a zone of its own.
func (d Date) Time() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
}

// AddDays returns the date n days away, normalizing as the calendar does.
func (d Date) AddDays(n int) Date {
	return DateOf(d.Time().AddDate(0, 0, n))
}

// Weekday is the day of the week this date falls on.
func (d Date) Weekday() time.Weekday {
	return d.Time().Weekday()
}

// Before, After and Equal order dates.
func (d Date) Before(other Date) bool { return d.Time().Before(other.Time()) }
func (d Date) After(other Date) bool  { return d.Time().After(other.Time()) }
func (d Date) Equal(other Date) bool  { return d == other }

// IsZero reports the absence of a date, which is how an open-ended series says
// it has no last day.
func (d Date) IsZero() bool { return d == Date{} }

// String is the ISO rendering.
func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day)
}

// DaysBetween is the number of whole days from d to other, negative when other
// is earlier.
func DaysBetween(d, other Date) int {
	return int(other.Time().Sub(d.Time()).Hours() / 24)
}

// ClockTime is a wall-clock time of day, to the minute.
//
// Fijo schedules nothing finer than a minute and stores no seconds, because a
// lesson at 16:00:00 and one at 16:00:30 are the same lesson written twice.
type ClockTime struct {
	Hour   int
	Minute int
}

// ClockTimeOf is the wall-clock time t reads in its own location.
func ClockTimeOf(t time.Time) ClockTime {
	return ClockTime{Hour: t.Hour(), Minute: t.Minute()}
}

// Valid reports whether this names a time of day.
func (c ClockTime) Valid() bool {
	return c.Hour >= 0 && c.Hour < 24 && c.Minute >= 0 && c.Minute < 60
}

// Minutes is the clock time as minutes since midnight, which is how it is
// stored.
func (c ClockTime) Minutes() int {
	return c.Hour*60 + c.Minute
}

// ClockTimeOfMinutes is the inverse of Minutes.
func ClockTimeOfMinutes(minutes int) ClockTime {
	return ClockTime{Hour: minutes / 60, Minute: minutes % 60}
}

// String is the 24-hour rendering.
func (c ClockTime) String() string {
	return fmt.Sprintf("%02d:%02d", c.Hour, c.Minute)
}

func orUTC(loc *time.Location) *time.Location {
	if loc == nil {
		return time.UTC
	}

	return loc
}
