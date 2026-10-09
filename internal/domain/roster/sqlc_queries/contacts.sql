-- name: CreateContact :one
INSERT INTO roster_contacts (
	id,
	studio_id,
	household_id,
	name,
	phone,
	language,
	relationship
) VALUES (
	sqlc.arg(id),
	sqlc.arg(studio_id),
	sqlc.arg(household_id),
	sqlc.arg(name),
	sqlc.arg(phone),
	sqlc.arg(language),
	sqlc.arg(relationship)
)
RETURNING *;

-- name: GetContact :one
SELECT *
FROM roster_contacts
WHERE roster_contacts.id = sqlc.arg(id)
	AND roster_contacts.studio_id = sqlc.arg(studio_id)
	AND roster_contacts.archived_at IS NULL;

-- name: ListContactsForHouseholds :many
SELECT *
FROM roster_contacts
WHERE roster_contacts.studio_id = sqlc.arg(studio_id)
	AND roster_contacts.household_id = ANY(sqlc.arg(household_ids)::text[])
	AND roster_contacts.archived_at IS NULL
ORDER BY roster_contacts.household_id, roster_contacts.created_at, roster_contacts.id;

-- name: UpdateContact :one
UPDATE roster_contacts SET
	name = sqlc.arg(name),
	phone = sqlc.arg(phone),
	language = sqlc.arg(language),
	relationship = sqlc.arg(relationship),
	last_updated_at = NOW()
WHERE roster_contacts.id = sqlc.arg(id)
	AND roster_contacts.studio_id = sqlc.arg(studio_id)
	AND roster_contacts.archived_at IS NULL
RETURNING *;

-- name: ArchiveContact :execrows
UPDATE roster_contacts SET
	archived_at = NOW()
WHERE roster_contacts.id = sqlc.arg(id)
	AND roster_contacts.studio_id = sqlc.arg(studio_id)
	AND roster_contacts.archived_at IS NULL;

-- UnlinkContact clears the contact from any attendee that named it, which is
-- what archiving an adult student's phone leaves behind: still an attendee,
-- no longer reachable.
-- name: UnlinkContact :exec
UPDATE roster_attendees SET
	contact_id = NULL,
	last_updated_at = NOW()
WHERE roster_attendees.contact_id = sqlc.arg(contact_id)
	AND roster_attendees.studio_id = sqlc.arg(studio_id);
