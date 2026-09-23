-- MaterializeOccurrence writes the row a series produces at an instant, and
-- does nothing if it is already there. The partial unique index on
-- (series_id, starts_at) is what makes running the worker twice harmless.
-- name: MaterializeOccurrence :one
INSERT INTO schedule_occurrences (
	id,
	studio_id,
	series_id,
	offering_id,
	starts_at,
	ends_at
) VALUES (
	sqlc.arg(id),
	sqlc.arg(studio_id),
	sqlc.arg(series_id),
	sqlc.arg(offering_id),
	sqlc.arg(starts_at),
	sqlc.arg(ends_at)
)
ON CONFLICT DO NOTHING
RETURNING *;

-- CreateOccurrence writes an appointment that no series produced: a one-off, or
-- the make-up scheduled against a cancellation.
-- name: CreateOccurrence :one
INSERT INTO schedule_occurrences (
	id,
	studio_id,
	series_id,
	offering_id,
	starts_at,
	ends_at,
	reason,
	replaces_occurrence_id
) VALUES (
	sqlc.arg(id),
	sqlc.arg(studio_id),
	sqlc.narg(series_id),
	sqlc.arg(offering_id),
	sqlc.arg(starts_at),
	sqlc.arg(ends_at),
	sqlc.arg(reason),
	sqlc.narg(replaces_occurrence_id)
)
RETURNING *;

-- name: GetOccurrence :one
SELECT *
FROM schedule_occurrences
WHERE schedule_occurrences.id = sqlc.arg(id)
	AND schedule_occurrences.studio_id = sqlc.arg(studio_id)
	AND schedule_occurrences.archived_at IS NULL;

-- ListOccurrencesInWindow is the week view: everything the studio has between
-- two instants, whatever state it is in, because a cancelled lesson is
-- something the owner needs to see in the grid.
-- name: ListOccurrencesInWindow :many
SELECT *
FROM schedule_occurrences
WHERE schedule_occurrences.studio_id = sqlc.arg(studio_id)
	AND schedule_occurrences.archived_at IS NULL
	AND schedule_occurrences.starts_at >= sqlc.arg(window_start)
	AND schedule_occurrences.starts_at < sqlc.arg(window_end)
ORDER BY schedule_occurrences.starts_at, schedule_occurrences.id;

-- name: ListOccurrencesForSeries :many
SELECT *
FROM schedule_occurrences
WHERE schedule_occurrences.studio_id = sqlc.arg(studio_id)
	AND schedule_occurrences.series_id = sqlc.arg(series_id)
	AND schedule_occurrences.archived_at IS NULL
ORDER BY schedule_occurrences.starts_at;

-- ListScheduledOccurrencesForSeriesFrom is what ending a series asks for: the
-- rows still standing on or after the day it now ends, which the command
-- cancels.
-- name: ListScheduledOccurrencesForSeriesFrom :many
SELECT *
FROM schedule_occurrences
WHERE schedule_occurrences.studio_id = sqlc.arg(studio_id)
	AND schedule_occurrences.series_id = sqlc.arg(series_id)
	AND schedule_occurrences.archived_at IS NULL
	AND schedule_occurrences.state = 'scheduled'
	AND schedule_occurrences.starts_at >= sqlc.arg(from_instant)
ORDER BY schedule_occurrences.starts_at;

-- ListScheduledOccurrencesInWindow is what closing the studio asks for.
-- name: ListScheduledOccurrencesInWindow :many
SELECT *
FROM schedule_occurrences
WHERE schedule_occurrences.studio_id = sqlc.arg(studio_id)
	AND schedule_occurrences.archived_at IS NULL
	AND schedule_occurrences.state = 'scheduled'
	AND schedule_occurrences.starts_at >= sqlc.arg(window_start)
	AND schedule_occurrences.starts_at < sqlc.arg(window_end)
ORDER BY schedule_occurrences.starts_at;

-- SetOccurrenceState is completed, no_show, and the revert back to scheduled.
-- It clears the cancellation columns, because a row that is no longer cancelled
-- must not still say who cancelled it.
-- name: SetOccurrenceState :one
UPDATE schedule_occurrences SET
	state = sqlc.arg(state),
	reason = sqlc.arg(reason),
	cancelled_by = NULL,
	cancelled_inside_notice = NULL,
	last_updated_at = NOW()
WHERE schedule_occurrences.id = sqlc.arg(id)
	AND schedule_occurrences.studio_id = sqlc.arg(studio_id)
	AND schedule_occurrences.archived_at IS NULL
	AND schedule_occurrences.state <> 'moved'
RETURNING *;

-- CancelOccurrence records whose decision it was and whether it fell inside the
-- studio's notice window, which is computed by the caller and then kept.
-- name: CancelOccurrence :one
UPDATE schedule_occurrences SET
	state = 'cancelled',
	cancelled_by = sqlc.arg(cancelled_by),
	cancelled_inside_notice = sqlc.arg(cancelled_inside_notice),
	reason = sqlc.arg(reason),
	last_updated_at = NOW()
WHERE schedule_occurrences.id = sqlc.arg(id)
	AND schedule_occurrences.studio_id = sqlc.arg(studio_id)
	AND schedule_occurrences.archived_at IS NULL
	AND schedule_occurrences.state = 'scheduled'
RETURNING *;

-- MarkOccurrenceMoved is the second half of a move: the row being left behind
-- names what it became.
-- name: MarkOccurrenceMoved :one
UPDATE schedule_occurrences SET
	state = 'moved',
	moved_to_occurrence_id = sqlc.arg(moved_to_occurrence_id),
	reason = sqlc.arg(reason),
	last_updated_at = NOW()
WHERE schedule_occurrences.id = sqlc.arg(id)
	AND schedule_occurrences.studio_id = sqlc.arg(studio_id)
	AND schedule_occurrences.archived_at IS NULL
	AND schedule_occurrences.state = 'scheduled'
RETURNING *;

-- name: AddOccurrenceStaff :exec
INSERT INTO schedule_occurrence_staff (
	occurrence_id,
	staff_user_id
) VALUES (
	sqlc.arg(occurrence_id),
	sqlc.arg(staff_user_id)
)
ON CONFLICT (occurrence_id, staff_user_id) DO NOTHING;

-- name: ClearOccurrenceStaff :exec
DELETE FROM schedule_occurrence_staff
WHERE schedule_occurrence_staff.occurrence_id = sqlc.arg(occurrence_id);

-- name: ListStaffForOccurrences :many
SELECT schedule_occurrence_staff.occurrence_id, schedule_occurrence_staff.staff_user_id
FROM schedule_occurrence_staff
WHERE schedule_occurrence_staff.occurrence_id = ANY(sqlc.arg(occurrence_ids)::text[])
ORDER BY schedule_occurrence_staff.occurrence_id, schedule_occurrence_staff.staff_user_id;
