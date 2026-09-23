-- name: CreateClosure :one
INSERT INTO schedule_closures (
	id,
	studio_id,
	starts_on,
	ends_on,
	reason
) VALUES (
	sqlc.arg(id),
	sqlc.arg(studio_id),
	sqlc.arg(starts_on),
	sqlc.arg(ends_on),
	sqlc.arg(reason)
)
RETURNING *;

-- name: ListClosures :many
SELECT *
FROM schedule_closures
WHERE schedule_closures.studio_id = sqlc.arg(studio_id)
	AND schedule_closures.archived_at IS NULL
ORDER BY schedule_closures.starts_on;

-- ListClosuresForStudios is the materializer's question, asked once for the
-- studios whose series it is about to walk.
-- name: ListClosuresForStudios :many
SELECT *
FROM schedule_closures
WHERE schedule_closures.studio_id = ANY(sqlc.arg(studio_ids)::text[])
	AND schedule_closures.archived_at IS NULL
ORDER BY schedule_closures.studio_id, schedule_closures.starts_on;

-- name: ArchiveClosure :execrows
UPDATE schedule_closures SET
	archived_at = NOW()
WHERE schedule_closures.id = sqlc.arg(id)
	AND schedule_closures.studio_id = sqlc.arg(studio_id)
	AND schedule_closures.archived_at IS NULL;
