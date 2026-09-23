-- The schedule is Fijo's grid: what a studio sells, the standing slots it sells
-- them in, and the individual appointments those slots produce.
--
-- Every table here carries studio_id, which is the identity account the studio
-- is and the tenancy scope every row is read under. It references
-- identity_accounts because a studio that is archived should take its schedule
-- with it; the platform cannot declare that key itself, and the platform block
-- of this sequence has already run by the time this file does.
--
-- Attendees are not here. A series has an attendee set and so does an
-- occurrence, but attendees are roster nouns and the roster arrives in phase 2,
-- so the join tables that attach them land with it. Nothing in this file
-- assumes their absence beyond not naming them.

-- schedule_studio_settings is the studio's own half of the schedule's rules.
--
-- Durations here are whole minutes or whole days rather than INTERVAL, because
-- an INTERVAL does not cross database/sql into a Go value without a custom
-- type, and nothing Fijo measures needs finer than a minute.
--
-- The notice window is what "late" means when a cancellation is recorded: a
-- cancellation inside it is late, and the balance and the money treat it
-- differently. The horizon is how far ahead the materializer writes; it is a
-- setting rather than a constant because a studio that books a term at a time
-- and one that books a fortnight at a time want different answers, and because
-- a number that governs how many rows a worker writes should be visible.
CREATE TABLE IF NOT EXISTS schedule_studio_settings (
    studio_id           TEXT PRIMARY KEY REFERENCES identity_accounts (id) ON DELETE CASCADE,
    notice_window_minutes INTEGER NOT NULL DEFAULT 1440 CHECK (notice_window_minutes >= 0),
    horizon_weeks       INTEGER NOT NULL DEFAULT 8 CHECK (horizon_weeks BETWEEN 1 AND 52),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_updated_at     TIMESTAMPTZ
);

-- schedule_offerings is what a studio sells: a half-hour piano lesson, a pack
-- of five, a monthly arrangement.
--
-- Names are bilingual because every client-facing string in Fijo is, and an
-- offering's name is the most client-facing string there is — it appears in the
-- reminder text. Both are required: a studio that has not translated one has
-- written the same words twice, which is a choice the schema can see.
--
-- list_price is the advertised price in the smallest currency unit, and what it
-- means depends on the kind: per lesson for single, for the whole pack for
-- package, per month for recurring. Every agreement may override it, which is
-- why this column is advertised rather than charged.
CREATE TABLE IF NOT EXISTS schedule_offerings (
    id                  TEXT PRIMARY KEY,
    studio_id           TEXT NOT NULL REFERENCES identity_accounts (id) ON DELETE CASCADE,
    name_en             TEXT NOT NULL CHECK (name_en <> ''),
    name_es             TEXT NOT NULL CHECK (name_es <> ''),
    kind                TEXT NOT NULL CHECK (kind IN ('single', 'package', 'recurring')),
    duration_minutes    INTEGER NOT NULL CHECK (duration_minutes > 0),
    list_price          BIGINT NOT NULL CHECK (list_price >= 0),

    -- The package columns, which are present exactly when the kind is package.
    -- expires_after_days is null for a pack that never expires; shareable says the
    -- balance may be drawn on by any attendee on the household rather than the
    -- one it was bought for.
    lesson_count        INTEGER CHECK (lesson_count > 0),
    expires_after_days  INTEGER CHECK (expires_after_days > 0),
    shareable           BOOLEAN NOT NULL DEFAULT FALSE,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_updated_at     TIMESTAMPTZ,
    archived_at         TIMESTAMPTZ,

    CONSTRAINT schedule_offerings_package_columns CHECK (
        (kind = 'package' AND lesson_count IS NOT NULL)
        OR (kind <> 'package' AND lesson_count IS NULL AND expires_after_days IS NULL AND shareable IS FALSE)
    )
);

CREATE INDEX IF NOT EXISTS schedule_offerings_studio_idx
    ON schedule_offerings (studio_id) WHERE archived_at IS NULL;

-- schedule_series is the standing slot: Tuesdays at four, every week, with
-- Maria, from the ninth of March until further notice.
--
-- start_minute is a wall-clock time in the studio's zone, as minutes since
-- midnight, and starts_on is a date. Both are deliberate: a standing lesson is
-- at four o'clock whatever the clocks do, so the absolute instant is computed
-- per occurrence when the row is materialized rather than stored here. ends_on
-- is null for "until otherwise noted", which is how most of them are.
--
-- The time is an integer rather than TIME for the same reason the durations
-- above are: a TIME column arrives at database/sql as a string, and a wall
-- clock to the minute is exactly a number of minutes.
--
-- interval_weeks is one or two and nothing else. Fijo's recurrence is weekly
-- with an interval, not RRULE, and the constraint is the schema saying so.
CREATE TABLE IF NOT EXISTS schedule_series (
    id                  TEXT PRIMARY KEY,
    studio_id           TEXT NOT NULL REFERENCES identity_accounts (id) ON DELETE CASCADE,
    offering_id         TEXT NOT NULL REFERENCES schedule_offerings (id) ON DELETE RESTRICT,
    weekday             SMALLINT NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    start_minute        SMALLINT NOT NULL CHECK (start_minute BETWEEN 0 AND 1439),
    interval_weeks      SMALLINT NOT NULL DEFAULT 1 CHECK (interval_weeks IN (1, 2)),
    starts_on           DATE NOT NULL,
    ends_on             DATE,
    price_override      BIGINT CHECK (price_override >= 0),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_updated_at     TIMESTAMPTZ,
    archived_at         TIMESTAMPTZ,

    CONSTRAINT schedule_series_ends_after_it_starts CHECK (ends_on IS NULL OR ends_on >= starts_on)
);

CREATE INDEX IF NOT EXISTS schedule_series_studio_idx
    ON schedule_series (studio_id) WHERE archived_at IS NULL;

-- schedule_series_staff is who teaches the standing slot.
--
-- It is a set rather than a column because a group class can have two teachers,
-- and it is the default an occurrence inherits rather than the truth about any
-- given week — a cover teacher is a change to one occurrence and never to the
-- series.
CREATE TABLE IF NOT EXISTS schedule_series_staff (
    series_id           TEXT NOT NULL REFERENCES schedule_series (id) ON DELETE CASCADE,
    staff_user_id       TEXT NOT NULL REFERENCES identity_users (id) ON DELETE RESTRICT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (series_id, staff_user_id)
);

CREATE INDEX IF NOT EXISTS schedule_series_staff_user_idx
    ON schedule_series_staff (staff_user_id);

-- schedule_closures is a date range the studio is shut.
--
-- It exists as a row rather than as a bulk cancellation because the
-- materializer runs again: a closure that had only cancelled the occurrences
-- standing at the time would be undone the next time the horizon moved. The
-- range is inclusive at both ends, which is how a person says "we are closed
-- the twenty-fourth through the twenty-sixth".
CREATE TABLE IF NOT EXISTS schedule_closures (
    id                  TEXT PRIMARY KEY,
    studio_id           TEXT NOT NULL REFERENCES identity_accounts (id) ON DELETE CASCADE,
    starts_on           DATE NOT NULL,
    ends_on             DATE NOT NULL,
    reason              TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at         TIMESTAMPTZ,

    CONSTRAINT schedule_closures_ends_after_it_starts CHECK (ends_on >= starts_on)
);

CREATE INDEX IF NOT EXISTS schedule_closures_studio_range_idx
    ON schedule_closures (studio_id, starts_on, ends_on) WHERE archived_at IS NULL;

-- schedule_occurrences is one appointment: the row everything else points at.
--
-- Reminders, threads, balances and the audit trail all key on an occurrence,
-- never on the series, because the series is a rule and the occurrence is the
-- thing that happens. A one-off appointment is an occurrence with no series,
-- which is why series_id is nullable.
--
-- starts_at and ends_at are absolute instants, computed from the series'
-- wall-clock time in the studio's zone when the row was written. Storing the
-- instant is what lets a reminder be scheduled and a week be read without
-- every query knowing the studio's zone.
--
-- The state machine is scheduled -> completed | cancelled | no_show | moved.
-- cancelled_by says whose decision it was, and inside_notice is computed
-- against the studio's notice window at the moment of cancelling and then kept,
-- because the window can be changed afterwards and the judgement should not
-- move with it.
--
-- The two occurrence pointers are different acts. moved_to names the occurrence
-- this one became when it was moved, and is set on the row being left behind.
-- replaces names the cancelled occurrence a make-up was scheduled against, and
-- is set on the new row. Neither cascades: a history that deletes itself is not
-- one.
CREATE TABLE IF NOT EXISTS schedule_occurrences (
    id                      TEXT PRIMARY KEY,
    studio_id               TEXT NOT NULL REFERENCES identity_accounts (id) ON DELETE CASCADE,
    series_id               TEXT REFERENCES schedule_series (id) ON DELETE SET NULL,
    offering_id             TEXT NOT NULL REFERENCES schedule_offerings (id) ON DELETE RESTRICT,
    starts_at               TIMESTAMPTZ NOT NULL,
    ends_at                 TIMESTAMPTZ NOT NULL,
    state                   TEXT NOT NULL DEFAULT 'scheduled'
                            CHECK (state IN ('scheduled', 'completed', 'cancelled', 'no_show', 'moved')),
    cancelled_by            TEXT CHECK (cancelled_by IN ('household', 'studio', 'closure')),
    cancelled_inside_notice BOOLEAN,
    reason                  TEXT NOT NULL DEFAULT '',
    moved_to_occurrence_id  TEXT REFERENCES schedule_occurrences (id) ON DELETE SET NULL,
    replaces_occurrence_id  TEXT REFERENCES schedule_occurrences (id) ON DELETE SET NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_updated_at         TIMESTAMPTZ,
    archived_at             TIMESTAMPTZ,

    CONSTRAINT schedule_occurrences_ends_after_it_starts CHECK (ends_at > starts_at),

    -- Whose cancellation it was is recorded exactly when it was cancelled.
    CONSTRAINT schedule_occurrences_cancellation_columns CHECK (
        (state = 'cancelled' AND cancelled_by IS NOT NULL)
        OR (state <> 'cancelled' AND cancelled_by IS NULL AND cancelled_inside_notice IS NULL)
    ),

    -- A moved occurrence names what it became, and nothing else does.
    CONSTRAINT schedule_occurrences_move_columns CHECK (
        (state = 'moved' AND moved_to_occurrence_id IS NOT NULL)
        OR (state <> 'moved' AND moved_to_occurrence_id IS NULL)
    )
);

-- The materializer's idempotency. A series produces at most one live occurrence
-- at a given instant, so running the worker twice over the same horizon writes
-- the same rows once. Archived rows are excluded so that a cancelled occurrence
-- does not block a make-up scheduled into the same slot.
CREATE UNIQUE INDEX IF NOT EXISTS schedule_occurrences_series_instant_idx
    ON schedule_occurrences (series_id, starts_at)
    WHERE series_id IS NOT NULL AND archived_at IS NULL;

CREATE INDEX IF NOT EXISTS schedule_occurrences_studio_window_idx
    ON schedule_occurrences (studio_id, starts_at) WHERE archived_at IS NULL;

CREATE INDEX IF NOT EXISTS schedule_occurrences_replaces_idx
    ON schedule_occurrences (replaces_occurrence_id) WHERE replaces_occurrence_id IS NOT NULL;

-- schedule_occurrence_staff is who is teaching this one.
--
-- It is copied from the series when the occurrence is materialized and edited
-- per occurrence thereafter, which is the whole of what "staff is reassignable
-- per occurrence" means.
CREATE TABLE IF NOT EXISTS schedule_occurrence_staff (
    occurrence_id       TEXT NOT NULL REFERENCES schedule_occurrences (id) ON DELETE CASCADE,
    staff_user_id       TEXT NOT NULL REFERENCES identity_users (id) ON DELETE RESTRICT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (occurrence_id, staff_user_id)
);

CREATE INDEX IF NOT EXISTS schedule_occurrence_staff_user_idx
    ON schedule_occurrence_staff (staff_user_id);
