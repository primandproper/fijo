package messaging

import (
	"testing"
	"time"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestCandidateOwed(t *testing.T) {
	t.Parallel()

	startsAt := time.Date(2026, time.November, 10, 22, 0, 0, 0, time.UTC)
	longAgo := startsAt.Add(-30 * 24 * time.Hour)

	candidate := func() *Candidate {
		return &Candidate{
			StartsAt:            startsAt,
			OccurrenceCreatedAt: longAgo,
			RemindersOnSince:    longAgo,
			FirstReminder:       24 * time.Hour,
			SecondReminder:      time.Hour,
		}
	}

	for _, tc := range []struct {
		now   time.Time
		edit  func(*Candidate)
		name  string
		want  time.Duration
		owned bool
	}{
		{
			name: "nothing before the day before",
			now:  startsAt.Add(-25 * time.Hour),
		},
		{
			name: "the day before, on the minute",
			now:  startsAt.Add(-24 * time.Hour), want: 24 * time.Hour, owned: true,
		},
		{
			name: "the day before, again, once it is written",
			now:  startsAt.Add(-23 * time.Hour),
			edit: func(c *Candidate) { c.SmallestWritten = 24 * time.Hour },
		},
		{
			name: "the hour before, after the day before went",
			now:  startsAt.Add(-time.Hour), want: time.Hour, owned: true,
			edit: func(c *Candidate) { c.SmallestWritten = 24 * time.Hour },
		},
		{
			name: "only the hour before, for a worker that missed the day",
			now:  startsAt.Add(-30 * time.Minute), want: time.Hour, owned: true,
		},
		{
			name: "nothing once the hour before is written",
			now:  startsAt.Add(-30 * time.Minute),
			edit: func(c *Candidate) { c.SmallestWritten = time.Hour },
		},
		{
			name: "nothing once the lesson starts",
			now:  startsAt,
		},
		{
			name: "the order of the two does not matter",
			now:  startsAt.Add(-2 * time.Hour), want: 24 * time.Hour, owned: true,
			edit: func(c *Candidate) { c.FirstReminder, c.SecondReminder = time.Hour, 24*time.Hour },
		},
		{
			name: "a household with one reminder",
			now:  startsAt.Add(-2 * time.Hour), want: 3 * time.Hour, owned: true,
			edit: func(c *Candidate) { c.FirstReminder, c.SecondReminder = 3*time.Hour, 0 },
		},
		{
			name: "a household with none",
			now:  startsAt.Add(-time.Minute),
			edit: func(c *Candidate) { c.FirstReminder, c.SecondReminder = 0, 0 },
		},
		{
			name: "not the day before for a studio that turned reminders on this morning",
			now:  startsAt.Add(-20 * time.Hour),
			edit: func(c *Candidate) { c.RemindersOnSince = startsAt.Add(-22 * time.Hour) },
		},
		{
			name: "but the hour before, for the same studio",
			now:  startsAt.Add(-time.Hour), want: time.Hour, owned: true,
			edit: func(c *Candidate) { c.RemindersOnSince = startsAt.Add(-22 * time.Hour) },
		},
		{
			name: "not the day before for a lesson booked this morning",
			now:  startsAt.Add(-5 * time.Hour),
			edit: func(c *Candidate) { c.OccurrenceCreatedAt = startsAt.Add(-6 * time.Hour) },
		},
		{
			name: "the day before for a lesson booked a day and a half out",
			now:  startsAt.Add(-24 * time.Hour), want: 24 * time.Hour, owned: true,
			edit: func(c *Candidate) { c.OccurrenceCreatedAt = startsAt.Add(-36 * time.Hour) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := candidate()
			if tc.edit != nil {
				tc.edit(c)
			}

			got, owed := c.Owed(tc.now)
			test.Eq(t, tc.owned, owed)
			test.Eq(t, tc.want, got)
		})
	}
}

func TestSettingsValidate(t *testing.T) {
	t.Parallel()

	must.NoError(t, (&Settings{StudioID: "s"}).Validate(), must.Sprint("off with no reply address is the starting state"))
	must.NoError(t, (&Settings{StudioID: "s", RemindersOn: true, ReplyTo: "maria@example.com"}).Validate())

	test.ErrorIs(t, (&Settings{StudioID: "s", RemindersOn: true}).Validate(), ErrNoReplyTo)
	test.ErrorIs(t, (&Settings{StudioID: "s", ReplyTo: "not an address"}).Validate(), ErrInvalidSettings)
	test.ErrorIs(t, (&Settings{}).Validate(), ErrInvalidSettings)
	test.ErrorIs(t, (*Settings)(nil).Validate(), ErrNilInput)
}
