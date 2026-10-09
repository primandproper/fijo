-- name: CreateHousehold :one
INSERT INTO roster_households (
	id,
	studio_id,
	name,
	language,
	notes
) VALUES (
	sqlc.arg(id),
	sqlc.arg(studio_id),
	sqlc.arg(name),
	sqlc.arg(language),
	sqlc.arg(notes)
)
RETURNING *;

-- name: GetHousehold :one
SELECT *
FROM roster_households
WHERE roster_households.id = sqlc.arg(id)
	AND roster_households.studio_id = sqlc.arg(studio_id)
	AND roster_households.archived_at IS NULL;

-- name: ListHouseholds :many
SELECT *
FROM roster_households
WHERE roster_households.studio_id = sqlc.arg(studio_id)
	AND (roster_households.archived_at IS NULL OR sqlc.arg(include_archived)::boolean)
ORDER BY lower(roster_households.name), roster_households.id;

-- name: UpdateHousehold :one
UPDATE roster_households SET
	name = sqlc.arg(name),
	language = sqlc.arg(language),
	notes = sqlc.arg(notes),
	last_updated_at = NOW()
WHERE roster_households.id = sqlc.arg(id)
	AND roster_households.studio_id = sqlc.arg(studio_id)
	AND roster_households.archived_at IS NULL
RETURNING *;

-- ArchiveHousehold retires the household and everybody on it in one statement,
-- so a household is never archived with live people still hanging off it.
-- name: ArchiveHousehold :one
WITH archived AS (
	UPDATE roster_households SET
		archived_at = NOW()
	WHERE roster_households.id = sqlc.arg(id)
		AND roster_households.studio_id = sqlc.arg(studio_id)
		AND roster_households.archived_at IS NULL
	RETURNING roster_households.id
), contacts AS (
	UPDATE roster_contacts SET
		archived_at = NOW()
	WHERE roster_contacts.household_id IN (SELECT archived.id FROM archived)
		AND roster_contacts.archived_at IS NULL
), attendees AS (
	UPDATE roster_attendees SET
		archived_at = NOW()
	WHERE roster_attendees.household_id IN (SELECT archived.id FROM archived)
		AND roster_attendees.archived_at IS NULL
)
SELECT archived.id FROM archived;
