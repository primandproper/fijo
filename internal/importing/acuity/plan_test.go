package acuity

import (
	"strings"
	"testing"
	"time"

	"github.com/primandproper/fijo/internal/domain/roster"
	"github.com/primandproper/fijo/internal/domain/schedule"
	"github.com/primandproper/fijo/internal/importing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// today is a Monday.
var today = schedule.Date{Year: 2026, Month: time.November, Day: 9}

// The client list as Acuity's help center and third-party guides describe it.
const clientList = byteOrderMark + "Client Name,Client Email Address,Client Phone Number,Notes on the Client File,Days Since Last Appointment\n" +
	"Lucía García,lucia.garcia@example.com,(512) 555-0101,Pickup is grandma on Thursdays.,\n" +
	"Sofía García,Lucia.Garcia@example.com,,,\n" +
	"Ana Ruiz,ana@example.com,512-555-0102,,\n" +
	"Old Client,old@example.com,,,400\n" +
	"Bad Email,not-an-email,,,\n"

// The appointments report in the older, shorter headers, with an intake
// form's question at the end.
const appointments = "Start Time,End Time,First Name,Last Name,Phone,Email,Type,Calendar,Appointment Price,Paid?,Canceled,Appointment ID,Which piece are you working on?\n" +
	`"November 3, 2026 4:00pm","November 3, 2026 4:30pm",Sofía,García,(512) 555-0101,lucia.garcia@example.com,Piano 30,Studio,45.00,yes,,1000,` + "\n" +
	`"November 10, 2026 4:00pm","November 10, 2026 4:30pm",Sofía,García,(512) 555-0101,lucia.garcia@example.com,Piano 30,Studio,45.00,no,,1001,Für Elise` + "\n" +
	`"November 17, 2026 4:00pm","November 17, 2026 4:30pm",Sofía,García,(512) 555-0101,lucia.garcia@example.com,Piano 30,Studio,45.00,no,,1002,` + "\n" +
	`"November 17, 2026 4:00pm","November 17, 2026 4:30pm",Sofía,García,(512) 555-0101,lucia.garcia@example.com,Piano 30,Studio,45.00,no,,1002,` + "\n" +
	`"November 24, 2026 4:00pm","November 24, 2026 4:30pm",Sofía,García,(512) 555-0101,lucia.garcia@example.com,Piano 30,Studio,45.00,no,,1003,` + "\n" +
	`"November 10, 2026 4:30pm","November 10, 2026 5:00pm",Mateo,García,(512) 555-0101,lucia.garcia@example.com,Guitar 30,Studio,40.00,no,,1004,` + "\n" +
	`"November 17, 2026 4:30pm","November 17, 2026 5:00pm",Mateo,García,(512) 555-0101,lucia.garcia@example.com,Guitar 30,Studio,40.00,no,,1005,` + "\n" +
	`"November 12, 2026 6:00pm","November 12, 2026 7:00pm",Ana,Ruiz,512-555-0102,ana@example.com,Voice 60,Studio,80.00,no,,1006,` + "\n" +
	`"November 19, 2026 6:00pm","November 19, 2026 7:00pm",Ana,Ruiz,512-555-0102,ana@example.com,Voice 60,Studio,70.00,no,,1007,` + "\n" +
	`"December 3, 2026 6:00pm","December 3, 2026 7:00pm",Ana,Ruiz,512-555-0102,ana@example.com,Voice 60,Studio,70.00,no,,1008,` + "\n" +
	`"November 26, 2026 6:00pm","November 26, 2026 7:00pm",Ana,Ruiz,512-555-0102,ana@example.com,Voice 60,Studio,70.00,no,yes,1009,` + "\n" +
	`"November 14, 2026 10:00am","November 14, 2026 10:45am",Lily,Chen,,wei.chen@example.com,Piano 45,Studio,60.00,no,,1010,` + "\n" +
	`"November 28, 2026 10:00am","November 28, 2026 10:45am",Lily,Chen,,wei.chen@example.com,Piano 45,Studio,60.00,no,,1011,` + "\n" +
	`"December 12, 2026 10:00am","December 12, 2026 10:45am",Lily,Chen,,wei.chen@example.com,Piano 45,Studio,60.00,no,,1012,` + "\n" +
	`"November 20, 2026 3:00pm","November 20, 2026 3:30pm",Ben,Ortiz,,ben@example.com,Piano 30,Studio,45.00,no,,1013,` + "\n"

func TestPlan(t *testing.T) {
	t.Parallel()

	plan, err := Plan(Input{Clients: clientList, Appointments: appointments, Today: today, Language: roster.Spanish})
	must.NoError(t, err)

	households := map[string]*importing.Household{}
	for _, h := range plan.Households {
		households[h.Name] = h
	}

	t.Run("a parent who books under the children's names is their contact", func(t *testing.T) {
		t.Parallel()

		garcia := households["García"]
		must.NotNil(t, garcia)
		test.True(t, garcia.Active)
		test.Eq(t, roster.Spanish, garcia.Language)
		test.Eq(t, "Lucía García: Pickup is grandma on Thursdays.", garcia.Notes)

		must.SliceLen(t, 1, garcia.Contacts)
		test.Eq(t, importing.Contact{Name: "Lucía García", Email: "lucia.garcia@example.com", Phone: "+15125550101", Relationship: roster.Guardian}, *garcia.Contacts[0])

		names := []string{}
		for _, a := range garcia.Attendees {
			names = append(names, a.Name)
		}

		test.Eq(t, []string{"Sofía García", "Mateo García"}, names)
	})

	t.Run("an adult student is their own contact", func(t *testing.T) {
		t.Parallel()

		ana := households["Ana Ruiz"]
		must.NotNil(t, ana)
		must.SliceLen(t, 1, ana.Contacts)
		test.Eq(t, roster.Self, ana.Contacts[0].Relationship)
		test.Eq(t, "a:ana ruiz", ana.Contacts[0].AttendeeKey)
		test.Eq(t, "+15125550102", ana.Contacts[0].Phone)
	})

	t.Run("somebody with nothing ahead is inactive", func(t *testing.T) {
		t.Parallel()

		test.False(t, households["Old Client"].Active)
		test.SliceEmpty(t, households["Bad Email"].Contacts)
		test.SliceNotEmpty(t, households["Bad Email"].Warnings)
	})

	t.Run("upcoming appointments in one slot are a standing lesson", func(t *testing.T) {
		t.Parallel()

		lessons := map[string]*importing.Lesson{}
		for _, l := range plan.Lessons {
			lessons[l.AttendeeName] = l
		}

		must.MapLen(t, 4, lessons)

		sofia := lessons["Sofía García"]
		test.Eq(t, time.Tuesday, sofia.Weekday)
		test.Eq(t, schedule.ClockTime{Hour: 16}, sofia.StartTime)
		test.Eq(t, 1, sofia.IntervalWeeks)
		test.Eq(t, schedule.Date{Year: 2026, Month: time.November, Day: 10}, sofia.StartsOn)
		test.Eq(t, 3, sofia.Seen, test.Sprint("the past, and the duplicate 1002, are not counted"))
		test.Eq(t, int64(4500), sofia.Price)
		test.Eq(t, households["García"].Key, sofia.HouseholdKey)
		test.SliceEmpty(t, sofia.Warnings)

		ana := lessons["Ana Ruiz"]
		test.Eq(t, time.Thursday, ana.Weekday)
		test.Eq(t, int64(7000), ana.Price, test.Sprint("the price most of them had"))
		must.SliceLen(t, 1, ana.Warnings, must.Sprint("the canceled 26th leaves a week without one"))
		test.StrContains(t, ana.Warnings[0], "1 of the weeks")

		lily := lessons["Lily Chen"]
		test.Eq(t, 2, lily.IntervalWeeks)
		test.Eq(t, schedule.ClockTime{Hour: 10}, lily.StartTime)
	})

	t.Run("a single appointment is booked on its own", func(t *testing.T) {
		t.Parallel()

		must.SliceLen(t, 1, plan.Appointments)

		ben := plan.Appointments[0]
		test.Eq(t, "Ben Ortiz", ben.AttendeeName)
		test.Eq(t, time.Friday, ben.On.Weekday())
		test.Eq(t, schedule.ClockTime{Hour: 15}, ben.StartTime)
		test.StrContains(t, ben.Reason, "Only one appointment ahead")
		test.NotEq(t, "", ben.HouseholdKey, test.Sprint("a one-off belongs to a household like a lesson does"))
	})

	t.Run("each type is an offering with the length and price most had", func(t *testing.T) {
		t.Parallel()

		offerings := map[string]*importing.Offering{}
		for _, o := range plan.Offerings {
			offerings[o.Name] = o
		}

		must.MapLen(t, 4, offerings)
		test.Eq(t, 30*time.Minute, offerings["Piano 30"].Duration)
		test.Eq(t, int64(4500), offerings["Piano 30"].ListPrice)
		test.Eq(t, 45*time.Minute, offerings["Piano 45"].Duration)
		test.Eq(t, time.Hour, offerings["Voice 60"].Duration)
	})

	t.Run("the plan says what it read and what it did not", func(t *testing.T) {
		t.Parallel()

		read := map[string]string{}
		for _, c := range plan.Columns {
			read[c.File+"/"+c.Field] = c.Header
		}

		test.Eq(t, "Client Name", read["clients/name"])
		test.Eq(t, "", read["clients/first name"])
		test.Eq(t, "Start Time", read["appointments/start"])
		test.Eq(t, "Type", read["appointments/appointment type"])

		all := strings.Join(plan.Warnings, "\n")
		test.StrContains(t, all, "Which piece are you working on?")
		test.StrContains(t, all, "Days Since Last Appointment")
		test.StrContains(t, all, "1 canceled appointments")
		test.StrContains(t, all, "1 past appointments")
		test.StrContains(t, all, `"not-an-email"`)
	})

	t.Run("the same files make the same keys", func(t *testing.T) {
		t.Parallel()

		again, againErr := Plan(Input{Clients: clientList, Appointments: appointments, Today: today, Language: roster.Spanish})
		must.NoError(t, againErr)

		for i, h := range plan.Households {
			test.Eq(t, h.Key, again.Households[i].Key)
		}

		for i, l := range plan.Lessons {
			test.Eq(t, l.Key, again.Lessons[i].Key)
		}
	})
}

func TestPlanReadsOtherSpellings(t *testing.T) {
	t.Parallel()

	// The help center's longer names, ISO times, and a 24-hour clock.
	report := "Start time and date,End time and date,Client first name,Client last name,Client email,Client phone,Appointment type,Price\n" +
		"2026-11-11 15:00,2026-11-11 15:30,Iris,Moreno,iris@example.com,+52 55 1234 5678,Violin,$50\n" +
		"2026-11-18 15:00,2026-11-18 15:30,Iris,Moreno,iris@example.com,+52 55 1234 5678,Violin,$50\n"

	plan, err := Plan(Input{Appointments: report, Today: today})
	must.NoError(t, err)

	must.SliceLen(t, 1, plan.Households)
	test.Eq(t, roster.English, plan.Households[0].Language)
	must.SliceLen(t, 1, plan.Households[0].Contacts)
	test.Eq(t, "+525512345678", plan.Households[0].Contacts[0].Phone)

	must.SliceLen(t, 1, plan.Lessons)
	test.Eq(t, time.Wednesday, plan.Lessons[0].Weekday)
	test.Eq(t, schedule.ClockTime{Hour: 15}, plan.Lessons[0].StartTime)
	test.Eq(t, int64(5000), plan.Lessons[0].Price)
	must.SliceLen(t, 1, plan.Offerings)
	test.Eq(t, 30*time.Minute, plan.Offerings[0].Duration)
}

func TestPlanNamesAParentAcuityNeverMet(t *testing.T) {
	t.Parallel()

	// Two children booked under one email, and no record of whoever owns it.
	report := "Date,Time,Name,Email,Type\n" +
		"11/10/2026,4:00 PM,Sofía García,family@example.com,Piano\n" +
		"11/17/2026,4:00 PM,Sofía García,family@example.com,Piano\n" +
		"11/10/2026,4:30 PM,Mateo García,family@example.com,Piano\n" +
		"11/17/2026,4:30 PM,Mateo García,family@example.com,Piano\n"

	plan, err := Plan(Input{Appointments: report, Today: today})
	must.NoError(t, err)

	must.SliceLen(t, 1, plan.Households)
	household := plan.Households[0]
	test.Eq(t, "García", household.Name)
	must.SliceLen(t, 1, household.Contacts)
	test.Eq(t, "García family", household.Contacts[0].Name)
	test.True(t, household.Contacts[0].GuessedName)
	test.Eq(t, "family@example.com", household.Contacts[0].Email)
	must.SliceLen(t, 2, household.Attendees)
	test.SliceLen(t, 2, plan.Lessons)
	test.Eq(t, defaultLength, plan.Offerings[0].Duration, test.Sprint("no end time and no duration"))
}

func TestPlanBooksMonthlyOneAtATime(t *testing.T) {
	t.Parallel()

	report := "Start Time,Name\n" +
		"\"November 10, 2026 4:00 pm\",Ana Ruiz\n" +
		"\"December 8, 2026 4:00 pm\",Ana Ruiz\n"

	plan, err := Plan(Input{Appointments: report, Today: today})
	must.NoError(t, err)

	test.SliceEmpty(t, plan.Lessons)
	must.SliceLen(t, 2, plan.Appointments, must.Sprint("every one of them is booked on its own"))
	test.NotEq(t, plan.Appointments[0].Key, plan.Appointments[1].Key)

	for _, appointment := range plan.Appointments {
		test.StrContains(t, appointment.Reason, "every 4 weeks")
	}
}

func TestPlanRefuses(t *testing.T) {
	t.Parallel()

	for name, in := range map[string]Input{
		"no files":            {},
		"no name column":      {Clients: "Email,Phone\nana@example.com,5125550100\n"},
		"no start column":     {Appointments: "Name,Type\nAna Ruiz,Piano\n"},
		"only a header":       {Clients: ""},
		"nothing but a blank": {Appointments: "\n\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := Plan(in)
			test.ErrorIs(t, err, ErrUnreadable)
		})
	}
}

func TestParseDateTime(t *testing.T) {
	t.Parallel()

	want := schedule.Date{Year: 2026, Month: time.November, Day: 10}

	for _, raw := range []string{
		"November 10, 2026 4:00pm",
		"November 10, 2026 4:00 PM",
		"november 10, 2026 4:00 p.m.",
		"Nov 10, 2026 4:00 PM",
		"Tuesday, November 10, 2026 4:00 pm",
		"2026-11-10 16:00",
		"2026-11-10T16:00:00",
		"2026-11-10T16:00:00-06:00",
		"11/10/2026 4:00 PM",
		"11/10/2026 16:00",
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			date, clock, ok := parseDateTime(raw)
			must.True(t, ok)
			test.Eq(t, want, date)
			test.Eq(t, schedule.ClockTime{Hour: 16}, clock)
		})
	}

	_, _, ok := parseDateTime("next Tuesday")
	test.False(t, ok)
}

func TestParsePrice(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]int64{"$45.00": 4500, "45": 4500, "1,200.50": 120050, "": -1, "free": -1, "-5": -1} {
		test.Eq(t, want, parsePrice(raw), test.Sprintf("%q", raw))
	}
}
