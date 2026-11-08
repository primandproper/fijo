-- The reminder: an email to each contact on a household before each of its
-- lessons, in the contact's language, with replies going to the studio.
--
-- When one is owed is never stored ahead of time. A lesson is cancelled, moved,
-- re-timed or handed to a new student often enough that a reminder scheduled at
-- booking would have to be found and moved by every one of those writes; instead
-- the worker asks, once a minute, which reminders are owed now, reading the
-- occurrences as they are. What is stored is each reminder once it is owed, which
-- is the record that it went and the fence that makes it go once.

-- A household's reminders: how many minutes before a lesson each of its two
-- goes out, or NULL for one it does not want. A day and an hour before unless
-- the owner says otherwise; a week before is as early as one can be.
ALTER TABLE roster_households
    ADD COLUMN IF NOT EXISTS first_reminder_minutes INTEGER DEFAULT 1440
        CHECK (first_reminder_minutes BETWEEN 15 AND 10080),
    ADD COLUMN IF NOT EXISTS second_reminder_minutes INTEGER DEFAULT 60
        CHECK (second_reminder_minutes BETWEEN 15 AND 10080);

-- A contact the owner has said gets no reminders. Not opted_out, which is the
-- carrier's STOP for a phone and is the sms package's to write.
ALTER TABLE roster_contacts
    ADD COLUMN IF NOT EXISTS reminders_off BOOLEAN NOT NULL DEFAULT FALSE;

-- messaging_studios is a studio's switch, and where a reply goes.
--
-- reminders_on is off until the owner turns it on, because the first studio
-- moves from a system that is still sending its own: two reminders for one
-- lesson is the cutover going wrong in front of every client. reminders_on_since
-- is when it was last turned on, and a reminder owed before it is not sent, so
-- turning it on is not a burst of reminders for the day already under way.
--
-- reply_to is the owner's own address, which a client's reply lands in; Fijo
-- holds no inbox of its own in v1.
CREATE TABLE IF NOT EXISTS messaging_studios (
    studio_id           TEXT PRIMARY KEY REFERENCES identity_accounts (id) ON DELETE CASCADE,
    reminders_on        BOOLEAN NOT NULL DEFAULT FALSE,
    reminders_on_since  TIMESTAMPTZ,
    reply_to            TEXT NOT NULL DEFAULT ''
                        CHECK (reply_to = '' OR reply_to ~ '^[^@[:space:]]+@[^@[:space:]]+$'),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_updated_at     TIMESTAMPTZ,

    CONSTRAINT messaging_studios_on_since CHECK (NOT reminders_on OR reminders_on_since IS NOT NULL)
);

-- messaging_reminders is one reminder owed to one contact about one lesson.
--
-- The unique key is what makes it go once across the fleet: a worker that finds
-- one owed inserts it, and a second worker finding the same one inserts
-- nothing. offset_minutes is which of the household's two it is, so the hour
-- before is its own row after the day before.
--
-- What was sent is kept as it was rendered, because the owner reading "what did
-- they get?" wants the words and not today's rendering of them. status is queued
-- until the email provider has it, then sent; skipped is a lesson that stopped
-- being owed a reminder between being found and being sent (cancelled, moved),
-- and failed is the provider refusing it past every retry.
CREATE TABLE IF NOT EXISTS messaging_reminders (
    id                  TEXT PRIMARY KEY,
    studio_id           TEXT NOT NULL REFERENCES identity_accounts (id) ON DELETE CASCADE,
    occurrence_id       TEXT NOT NULL REFERENCES schedule_occurrences (id) ON DELETE CASCADE,
    household_id        TEXT NOT NULL REFERENCES roster_households (id) ON DELETE CASCADE,
    contact_id          TEXT NOT NULL REFERENCES roster_contacts (id) ON DELETE CASCADE,
    offset_minutes      INTEGER NOT NULL CHECK (offset_minutes > 0),
    channel             TEXT NOT NULL DEFAULT 'email' CHECK (channel IN ('email')),
    to_address          TEXT NOT NULL CHECK (to_address <> ''),
    to_name             TEXT NOT NULL DEFAULT '',
    language            TEXT NOT NULL CHECK (language IN ('en', 'es')),
    subject             TEXT NOT NULL,
    body_html           TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'queued'
                        CHECK (status IN ('queued', 'sent', 'skipped', 'failed')),
    reason              TEXT NOT NULL DEFAULT '',
    attempts            INTEGER NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at             TIMESTAMPTZ,
    last_updated_at     TIMESTAMPTZ,

    CONSTRAINT messaging_reminders_sent_at CHECK ((status = 'sent') = (sent_at IS NOT NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS messaging_reminders_once_idx
    ON messaging_reminders (occurrence_id, contact_id, offset_minutes);

CREATE INDEX IF NOT EXISTS messaging_reminders_household_idx
    ON messaging_reminders (household_id, created_at DESC);

CREATE INDEX IF NOT EXISTS messaging_reminders_studio_idx
    ON messaging_reminders (studio_id, created_at DESC);

-- The due read scans scheduled lessons in the week ahead.
CREATE INDEX IF NOT EXISTS schedule_occurrences_upcoming_idx
    ON schedule_occurrences (starts_at) WHERE state = 'scheduled' AND archived_at IS NULL;
