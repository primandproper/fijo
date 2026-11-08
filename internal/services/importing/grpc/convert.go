package importinggrpc

import (
	"github.com/primandproper/fijo/internal/domain/roster"
	"github.com/primandproper/fijo/internal/grpc/fijopb"
	"github.com/primandproper/fijo/internal/importing"
)

func previewToProto(plan *importing.Plan) *fijopb.ImportPreview {
	out := &fijopb.ImportPreview{
		Columns:    make([]*fijopb.ImportColumn, 0, len(plan.Columns)),
		Households: make([]*fijopb.ImportHousehold, 0, len(plan.Households)),
		Offerings:  make([]*fijopb.ImportOffering, 0, len(plan.Offerings)),
		Lessons:    make([]*fijopb.ImportLesson, 0, len(plan.Lessons)),
		Leftovers:  make([]*fijopb.ImportLeftover, 0, len(plan.Leftovers)),
		Warnings:   plan.Warnings,
	}

	for _, c := range plan.Columns {
		out.Columns = append(out.Columns, &fijopb.ImportColumn{File: c.File, Field: c.Field, Header: c.Header})
	}

	for _, h := range plan.Households {
		household := &fijopb.ImportHousehold{
			Key:      h.Key,
			Name:     h.Name,
			Language: languageToProto(h.Language),
			Notes:    h.Notes,
			Warnings: h.Warnings,
			Active:   h.Active,
			OnRoster: h.OnRoster,
		}

		for _, c := range h.Contacts {
			household.Contacts = append(household.Contacts, &fijopb.ImportContact{
				Name:         c.Name,
				Email:        c.Email,
				Phone:        c.Phone,
				Relationship: relationshipToProto(c.Relationship),
				GuessedName:  c.GuessedName,
			})
		}

		for _, a := range h.Attendees {
			household.Attendees = append(household.Attendees, &fijopb.ImportAttendee{Key: a.Key, Name: a.Name})
		}

		out.Households = append(out.Households, household)
	}

	for _, o := range plan.Offerings {
		out.Offerings = append(out.Offerings, &fijopb.ImportOffering{
			Key:                o.Key,
			Name:               o.Name,
			DurationMinutes:    int32(o.Duration.Minutes()),
			ListPrice:          o.ListPrice,
			ExistingOfferingId: o.ExistingID,
		})
	}

	for _, l := range plan.Lessons {
		out.Lessons = append(out.Lessons, &fijopb.ImportLesson{
			Key:           l.Key,
			HouseholdKey:  l.HouseholdKey,
			AttendeeName:  l.AttendeeName,
			OfferingKey:   l.OfferingKey,
			Weekday:       int32(l.Weekday),
			StartMinutes:  int32(l.StartTime.Minutes()),
			IntervalWeeks: int32(l.IntervalWeeks),
			StartsOn:      l.StartsOn.String(),
			Price:         l.Price,
			Seen:          int32(l.Seen),
			Warnings:      l.Warnings,
		})
	}

	for _, l := range plan.Leftovers {
		dates := make([]string, 0, len(l.Dates))
		for _, d := range l.Dates {
			dates = append(dates, d.String())
		}

		out.Leftovers = append(out.Leftovers, &fijopb.ImportLeftover{Who: l.Who, What: l.What, Reason: l.Reason, Dates: dates})
	}

	return out
}

func languageToProto(language roster.Language) fijopb.Language {
	switch language {
	case roster.English:
		return fijopb.Language_LANGUAGE_EN
	case roster.Spanish:
		return fijopb.Language_LANGUAGE_ES
	default:
		return fijopb.Language_LANGUAGE_UNSPECIFIED
	}
}

func languageFromProto(language fijopb.Language) roster.Language {
	switch language {
	case fijopb.Language_LANGUAGE_ES:
		return roster.Spanish
	default:
		return roster.English
	}
}

func relationshipToProto(relationship roster.Relationship) fijopb.Relationship {
	switch relationship {
	case roster.Guardian:
		return fijopb.Relationship_RELATIONSHIP_GUARDIAN
	case roster.Self:
		return fijopb.Relationship_RELATIONSHIP_SELF
	default:
		return fijopb.Relationship_RELATIONSHIP_UNSPECIFIED
	}
}
