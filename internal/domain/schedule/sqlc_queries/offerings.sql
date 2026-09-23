-- name: CreateOffering :one
INSERT INTO schedule_offerings (
	id,
	studio_id,
	name_en,
	name_es,
	kind,
	duration_minutes,
	list_price,
	lesson_count,
	expires_after_days,
	shareable
) VALUES (
	sqlc.arg(id),
	sqlc.arg(studio_id),
	sqlc.arg(name_en),
	sqlc.arg(name_es),
	sqlc.arg(kind),
	sqlc.arg(duration_minutes),
	sqlc.arg(list_price),
	sqlc.narg(lesson_count),
	sqlc.narg(expires_after_days),
	sqlc.arg(shareable)
)
RETURNING *;

-- name: GetOffering :one
SELECT *
FROM schedule_offerings
WHERE schedule_offerings.id = sqlc.arg(id)
	AND schedule_offerings.studio_id = sqlc.arg(studio_id)
	AND schedule_offerings.archived_at IS NULL;

-- name: ListOfferings :many
SELECT *
FROM schedule_offerings
WHERE schedule_offerings.studio_id = sqlc.arg(studio_id)
	AND (schedule_offerings.archived_at IS NULL OR sqlc.arg(include_archived)::boolean)
ORDER BY schedule_offerings.name_en;

-- name: UpdateOffering :one
UPDATE schedule_offerings SET
	name_en = sqlc.arg(name_en),
	name_es = sqlc.arg(name_es),
	duration_minutes = sqlc.arg(duration_minutes),
	list_price = sqlc.arg(list_price),
	lesson_count = sqlc.narg(lesson_count),
	expires_after_days = sqlc.narg(expires_after_days),
	shareable = sqlc.arg(shareable),
	last_updated_at = NOW()
WHERE schedule_offerings.id = sqlc.arg(id)
	AND schedule_offerings.studio_id = sqlc.arg(studio_id)
	AND schedule_offerings.archived_at IS NULL
RETURNING *;

-- name: ArchiveOffering :execrows
UPDATE schedule_offerings SET
	archived_at = NOW()
WHERE schedule_offerings.id = sqlc.arg(id)
	AND schedule_offerings.studio_id = sqlc.arg(studio_id)
	AND schedule_offerings.archived_at IS NULL;
