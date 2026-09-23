package schedule

import (
	"testing"
	"time"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func date(year int, month time.Month, day int) Date {
	return Date{Year: year, Month: month, Day: day}
}

func TestPlan(t *testing.T) {
	t.Parallel()

	// March 2026 begins on a Sunday, so the Tuesdays are the 3rd, 10th, 17th,
	// 24th and 31st.
	weekly := func() *Series {
		return &Series{
			Weekday:       time.Tuesday,
			StartTime:     ClockTime{Hour: 16},
			IntervalWeeks: 1,
			StartsOn:      date(2026, time.March, 1),
		}
	}

	t.Run("a weekly series runs every week", func(t *testing.T) {
		t.Parallel()

		dates := Plan(weekly(), date(2026, time.March, 1), date(2026, time.March, 31), nil)
		test.Eq(t, []Date{
			date(2026, time.March, 3),
			date(2026, time.March, 10),
			date(2026, time.March, 17),
			date(2026, time.March, 24),
			date(2026, time.March, 31),
		}, dates)
	})

	t.Run("a fortnightly series counts from its first run, not from its start date", func(t *testing.T) {
		t.Parallel()

		// It starts on a Sunday and runs on Tuesdays, so the first Tuesday is
		// week zero and the interval is counted from there.
		fortnightly := weekly()
		fortnightly.IntervalWeeks = 2

		dates := Plan(fortnightly, date(2026, time.March, 1), date(2026, time.March, 31), nil)
		test.Eq(t, []Date{
			date(2026, time.March, 3),
			date(2026, time.March, 17),
			date(2026, time.March, 31),
		}, dates)
	})

	t.Run("a window that begins mid-series keeps the fortnightly phase", func(t *testing.T) {
		t.Parallel()

		fortnightly := weekly()
		fortnightly.IntervalWeeks = 2

		// Asking from the 11th must still produce the 17th and the 31st, not
		// the 17th and a restarted count.
		dates := Plan(fortnightly, date(2026, time.March, 11), date(2026, time.March, 31), nil)
		test.Eq(t, []Date{
			date(2026, time.March, 17),
			date(2026, time.March, 31),
		}, dates)
	})

	t.Run("nothing before the series starts or after it ends", func(t *testing.T) {
		t.Parallel()

		ending := weekly()
		ending.StartsOn = date(2026, time.March, 10)
		ending.EndsOn = date(2026, time.March, 20)

		dates := Plan(ending, date(2026, time.March, 1), date(2026, time.March, 31), nil)
		test.Eq(t, []Date{
			date(2026, time.March, 10),
			date(2026, time.March, 17),
		}, dates)
	})

	t.Run("a closure takes the days it covers out", func(t *testing.T) {
		t.Parallel()

		closures := []*Closure{{
			StartsOn: date(2026, time.March, 16),
			EndsOn:   date(2026, time.March, 22),
		}}

		dates := Plan(weekly(), date(2026, time.March, 1), date(2026, time.March, 31), closures)
		test.Eq(t, []Date{
			date(2026, time.March, 3),
			date(2026, time.March, 10),
			date(2026, time.March, 24),
			date(2026, time.March, 31),
		}, dates)
	})

	t.Run("refuses what it cannot plan", func(t *testing.T) {
		t.Parallel()

		test.Nil(t, Plan(nil, date(2026, time.March, 1), date(2026, time.March, 31), nil))
		test.Nil(t, Plan(weekly(), Date{}, date(2026, time.March, 31), nil))
		test.Nil(t, Plan(weekly(), date(2026, time.March, 31), date(2026, time.March, 1), nil))

		// A window entirely after the series has ended.
		ended := weekly()
		ended.EndsOn = date(2026, time.February, 1)
		test.Nil(t, Plan(ended, date(2026, time.March, 1), date(2026, time.March, 31), nil))
	})
}

func TestDateAtAcrossDaylightSaving(t *testing.T) {
	t.Parallel()

	central, err := time.LoadLocation("US/Central")
	must.NoError(t, err)

	t.Run("a standing lesson keeps its wall clock and changes its instant", func(t *testing.T) {
		t.Parallel()

		// US daylight saving begins on 8 March 2026, between these two
		// Tuesdays. Four o'clock stays four o'clock and the UTC offset moves,
		// which is the whole reason a series stores a wall clock.
		before := date(2026, time.March, 3).At(ClockTime{Hour: 16}, central)
		after := date(2026, time.March, 10).At(ClockTime{Hour: 16}, central)

		test.Eq(t, 16, before.Hour())
		test.Eq(t, 16, after.Hour())

		_, beforeOffset := before.Zone()
		_, afterOffset := after.Zone()
		test.Eq(t, -6*60*60, beforeOffset)
		test.Eq(t, -5*60*60, afterOffset)

		// Seven calendar days apart, twenty-three hours of elapsed time.
		test.Eq(t, 23*time.Hour, after.Sub(before)-6*24*time.Hour)
	})

	t.Run("a wall clock the spring forward skips resolves backwards", func(t *testing.T) {
		t.Parallel()

		// 02:30 does not exist on 8 March 2026 in US/Central. Go answers with
		// the instant an hour earlier rather than an hour later, which is the
		// behavior civil.go documents.
		skipped := date(2026, time.March, 8).At(ClockTime{Hour: 2, Minute: 30}, central)
		test.Eq(t, 1, skipped.Hour())
		test.Eq(t, 30, skipped.Minute())

		zone, _ := skipped.Zone()
		test.Eq(t, "CST", zone)
	})

	t.Run("a wall clock the autumn back repeats resolves to the first of the two", func(t *testing.T) {
		t.Parallel()

		// 01:30 happens twice on 1 November 2026 in US/Central.
		ambiguous := date(2026, time.November, 1).At(ClockTime{Hour: 1, Minute: 30}, central)
		test.Eq(t, 1, ambiguous.Hour())

		zone, _ := ambiguous.Zone()
		test.Eq(t, "CDT", zone, test.Sprint("the earlier of the two, still on daylight time"))

		// The same instant as half an hour past midnight plus an hour, which is
		// what "the first one" means.
		test.Eq(t, date(2026, time.November, 1).At(ClockTime{Minute: 30}, central).Add(time.Hour).Unix(), ambiguous.Unix())
	})
}

func TestSeriesRuns(t *testing.T) {
	t.Parallel()

	series := &Series{
		Weekday:       time.Tuesday,
		IntervalWeeks: 1,
		StartsOn:      date(2026, time.March, 1),
	}

	test.True(t, series.Runs(date(2026, time.March, 3)))
	test.False(t, series.Runs(date(2026, time.March, 4)), test.Sprint("a Wednesday is not a Tuesday"))
	test.False(t, series.Runs(date(2026, time.February, 24)), test.Sprint("before it starts"))

	series.EndsOn = date(2026, time.March, 12)
	test.True(t, series.Runs(date(2026, time.March, 10)))
	test.False(t, series.Runs(date(2026, time.March, 17)), test.Sprint("after it ends"))
}

func TestClockTimeMinutesRoundTrip(t *testing.T) {
	t.Parallel()

	for _, clock := range []ClockTime{{}, {Hour: 9, Minute: 5}, {Hour: 16}, {Hour: 23, Minute: 59}} {
		test.Eq(t, clock, ClockTimeOfMinutes(clock.Minutes()))
		test.True(t, clock.Valid())
	}

	test.False(t, ClockTime{Hour: 24}.Valid())
	test.False(t, ClockTime{Minute: 60}.Valid())
	test.False(t, ClockTime{Hour: -1}.Valid())
}
