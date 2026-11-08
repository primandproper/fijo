-- A contact is reached by email, by phone, or both (decided 2026-11-07: email
-- reaches the first studio's clients better than texts, and an import from
-- Acuity brings emails that often have no phone beside them).
--
-- email is stored trimmed and lowercased by the store. It is unique among a
-- studio's live contacts, as the phone is: the Acuity import groups records by
-- it and a re-import matches on it, and one address on two households would
-- leave both choices to chance.
--
-- opted_out stays the carrier's STOP for the phone; an email opt-out arrives
-- with the reminders that read it.
ALTER TABLE roster_contacts ALTER COLUMN phone DROP NOT NULL;

ALTER TABLE roster_contacts ADD COLUMN IF NOT EXISTS email TEXT
    CHECK (email = lower(btrim(email)) AND email ~ '^[^@[:space:]]+@[^@[:space:]]+$');

ALTER TABLE roster_contacts ADD CONSTRAINT roster_contacts_reachable
    CHECK (phone IS NOT NULL OR email IS NOT NULL);

CREATE UNIQUE INDEX IF NOT EXISTS roster_contacts_studio_email_idx
    ON roster_contacts (studio_id, email) WHERE archived_at IS NULL;
