-- name: CreateAttendee :one
INSERT INTO roster_attendees (
	id,
	studio_id,
	household_id,
	name,
	language,
	contact_id
) VALUES (
	sqlc.arg(id),
	sqlc.arg(studio_id),
	sqlc.arg(household_id),
	sqlc.arg(name),
	sqlc.arg(language),
	sqlc.narg(contact_id)
)
RETURNING *;

-- name: GetAttendee :one
SELECT *
FROM roster_attendees
WHERE roster_attendees.id = sqlc.arg(id)
	AND roster_attendees.studio_id = sqlc.arg(studio_id)
	AND roster_attendees.archived_at IS NULL;

-- name: ListAttendeesForHouseholds :many
SELECT *
FROM roster_attendees
WHERE roster_attendees.studio_id = sqlc.arg(studio_id)
	AND roster_attendees.household_id = ANY(sqlc.arg(household_ids)::text[])
	AND roster_attendees.archived_at IS NULL
ORDER BY roster_attendees.household_id, roster_attendees.created_at, roster_attendees.id;

-- name: ListAttendeesForHousehold :many
SELECT roster_attendees.id
FROM roster_attendees
WHERE roster_attendees.studio_id = sqlc.arg(studio_id)
	AND roster_attendees.household_id = sqlc.arg(household_id)
	AND roster_attendees.archived_at IS NULL;

-- name: UpdateAttendee :one
UPDATE roster_attendees SET
	name = sqlc.arg(name),
	language = sqlc.arg(language),
	contact_id = sqlc.narg(contact_id),
	last_updated_at = NOW()
WHERE roster_attendees.id = sqlc.arg(id)
	AND roster_attendees.studio_id = sqlc.arg(studio_id)
	AND roster_attendees.archived_at IS NULL
RETURNING *;

-- name: ArchiveAttendee :execrows
UPDATE roster_attendees SET
	archived_at = NOW()
WHERE roster_attendees.id = sqlc.arg(id)
	AND roster_attendees.studio_id = sqlc.arg(studio_id)
	AND roster_attendees.archived_at IS NULL;
