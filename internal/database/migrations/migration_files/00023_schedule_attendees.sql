-- Who a standing slot and its appointments are for.
--
-- These are the join tables 00021 left room for. A series is somebody's
-- standing lesson, so the schedule's own code refuses one with nobody on it;
-- the schema cannot say "at least one row in another table" without a trigger,
-- and the store is the only writer.
--
-- An attendee is never deleted out from under a lesson: the roster archives
-- rather than deletes, and refuses to archive somebody who still has a standing
-- slot. RESTRICT is the schema backing that up.

-- schedule_series_attendees is the default an occurrence inherits, the way
-- schedule_series_staff is for who teaches.
CREATE TABLE IF NOT EXISTS schedule_series_attendees (
    series_id           TEXT NOT NULL REFERENCES schedule_series (id) ON DELETE CASCADE,
    attendee_id         TEXT NOT NULL REFERENCES roster_attendees (id) ON DELETE RESTRICT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (series_id, attendee_id)
);

CREATE INDEX IF NOT EXISTS schedule_series_attendees_attendee_idx
    ON schedule_series_attendees (attendee_id);

-- schedule_occurrence_attendees is who this one appointment is for, copied from
-- the series when it is materialized, and from the original when a lesson is
-- moved or made up.
CREATE TABLE IF NOT EXISTS schedule_occurrence_attendees (
    occurrence_id       TEXT NOT NULL REFERENCES schedule_occurrences (id) ON DELETE CASCADE,
    attendee_id         TEXT NOT NULL REFERENCES roster_attendees (id) ON DELETE RESTRICT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (occurrence_id, attendee_id)
);

CREATE INDEX IF NOT EXISTS schedule_occurrence_attendees_attendee_idx
    ON schedule_occurrence_attendees (attendee_id);
