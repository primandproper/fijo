-- The roster is who a studio teaches: households, the contacts on them who get
-- the texts, and the attendees who take the lessons.
--
-- None of these people are users. A client never has a password; their door is
-- a phone number, and the owner is the only person who signs in to write any of
-- this. Every table carries studio_id, the identity account the studio is, and
-- every read and write is scoped by it.
--
-- Guardianship is implicit: every contact on a household is treated as a
-- guardian of every attendee on it. A table that says which contact may speak
-- for which child waits for the case that needs it (separated parents with
-- different children).

-- roster_households is the client account: the García family, or Ana Ruiz on
-- her own.
--
-- language is the household's default, which a contact or attendee added to it
-- starts with. Reminder offsets land here with the reminder text in phase 3.
CREATE TABLE IF NOT EXISTS roster_households (
    id                  TEXT PRIMARY KEY,
    studio_id           TEXT NOT NULL REFERENCES identity_accounts (id) ON DELETE CASCADE,
    name                TEXT NOT NULL CHECK (name <> ''),
    language            TEXT NOT NULL CHECK (language IN ('en', 'es')),
    notes               TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_updated_at     TIMESTAMPTZ,
    archived_at         TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS roster_households_studio_idx
    ON roster_households (studio_id) WHERE archived_at IS NULL;

-- roster_contacts is a person with a phone: who receives the texts, and who
-- pays.
--
-- phone is E.164. It is unique among a studio's live contacts because an
-- inbound text is filed onto a household by the number it came from, and a
-- number on two households would leave that choice to chance.
--
-- relationship is guardian for a parent and self for an adult who takes the
-- lessons; the second is also an attendee whose contact_id names this row.
-- opted_out is a carrier fact (STOP) recorded when the sms package reports it,
-- and verified_at is set when the contact proves the phone is theirs. Neither
-- is written by anything yet.
CREATE TABLE IF NOT EXISTS roster_contacts (
    id                  TEXT PRIMARY KEY,
    studio_id           TEXT NOT NULL REFERENCES identity_accounts (id) ON DELETE CASCADE,
    household_id        TEXT NOT NULL REFERENCES roster_households (id) ON DELETE CASCADE,
    name                TEXT NOT NULL CHECK (name <> ''),
    phone               TEXT NOT NULL CHECK (phone ~ '^\+[1-9][0-9]{6,14}$'),
    language            TEXT NOT NULL CHECK (language IN ('en', 'es')),
    relationship        TEXT NOT NULL CHECK (relationship IN ('guardian', 'self')),
    opted_out           BOOLEAN NOT NULL DEFAULT FALSE,
    verified_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_updated_at     TIMESTAMPTZ,
    archived_at         TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS roster_contacts_household_idx
    ON roster_contacts (household_id) WHERE archived_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS roster_contacts_studio_phone_idx
    ON roster_contacts (studio_id, phone) WHERE archived_at IS NULL;

-- roster_attendees is a person who takes lessons. A child is an attendee with
-- no contact; an adult student is an attendee whose contact_id names their own
-- contact row, so they are one person and not two.
CREATE TABLE IF NOT EXISTS roster_attendees (
    id                  TEXT PRIMARY KEY,
    studio_id           TEXT NOT NULL REFERENCES identity_accounts (id) ON DELETE CASCADE,
    household_id        TEXT NOT NULL REFERENCES roster_households (id) ON DELETE CASCADE,
    name                TEXT NOT NULL CHECK (name <> ''),
    language            TEXT NOT NULL CHECK (language IN ('en', 'es')),
    contact_id          TEXT REFERENCES roster_contacts (id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_updated_at     TIMESTAMPTZ,
    archived_at         TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS roster_attendees_household_idx
    ON roster_attendees (household_id) WHERE archived_at IS NULL;
