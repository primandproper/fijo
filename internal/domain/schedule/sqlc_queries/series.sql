-- name: CreateSeries :one
INSERT INTO schedule_series (
	id,
	studio_id,
	offering_id,
	weekday,
	start_minute,
	interval_weeks,
	starts_on,
	ends_on,
	price_override
) VALUES (
	sqlc.arg(id),
	sqlc.arg(studio_id),
	sqlc.arg(offering_id),
	sqlc.arg(weekday),
	sqlc.arg(start_minute),
	sqlc.arg(interval_weeks),
	sqlc.arg(starts_on),
	sqlc.narg(ends_on),
	sqlc.narg(price_override)
)
RETURNING *;

-- name: GetSeries :one
SELECT *
FROM schedule_series
WHERE schedule_series.id = sqlc.arg(id)
	AND schedule_series.studio_id = sqlc.arg(studio_id)
	AND schedule_series.archived_at IS NULL;

-- name: ListSeries :many
SELECT *
FROM schedule_series
WHERE schedule_series.studio_id = sqlc.arg(studio_id)
	AND (schedule_series.archived_at IS NULL OR sqlc.arg(include_archived)::boolean)
ORDER BY schedule_series.weekday, schedule_series.start_minute;

-- ListLiveSeries is the materializer's question: every series that could still
-- produce an occurrence on or before the horizon. It crosses studios, because
-- the worker sweeps a deployment rather than a tenant.
-- name: ListLiveSeries :many
SELECT *
FROM schedule_series
WHERE schedule_series.archived_at IS NULL
	AND schedule_series.starts_on <= sqlc.arg(horizon)::date
	AND (schedule_series.ends_on IS NULL OR schedule_series.ends_on >= sqlc.arg(today)::date)
ORDER BY schedule_series.studio_id, schedule_series.id;

-- name: UpdateSeries :one
UPDATE schedule_series SET
	offering_id = sqlc.arg(offering_id),
	weekday = sqlc.arg(weekday),
	start_minute = sqlc.arg(start_minute),
	interval_weeks = sqlc.arg(interval_weeks),
	starts_on = sqlc.arg(starts_on),
	ends_on = sqlc.narg(ends_on),
	price_override = sqlc.narg(price_override),
	last_updated_at = NOW()
WHERE schedule_series.id = sqlc.arg(id)
	AND schedule_series.studio_id = sqlc.arg(studio_id)
	AND schedule_series.archived_at IS NULL
RETURNING *;

-- EndSeries closes a standing slot from a date. It is not an archive: the
-- series and everything it already produced stay readable.
-- name: EndSeries :one
UPDATE schedule_series SET
	ends_on = sqlc.arg(ends_on),
	last_updated_at = NOW()
WHERE schedule_series.id = sqlc.arg(id)
	AND schedule_series.studio_id = sqlc.arg(studio_id)
	AND schedule_series.archived_at IS NULL
RETURNING *;

-- name: ArchiveSeries :execrows
UPDATE schedule_series SET
	archived_at = NOW()
WHERE schedule_series.id = sqlc.arg(id)
	AND schedule_series.studio_id = sqlc.arg(studio_id)
	AND schedule_series.archived_at IS NULL;

-- name: AddSeriesStaff :exec
INSERT INTO schedule_series_staff (
	series_id,
	staff_user_id
) VALUES (
	sqlc.arg(series_id),
	sqlc.arg(staff_user_id)
)
ON CONFLICT (series_id, staff_user_id) DO NOTHING;

-- name: ClearSeriesStaff :exec
DELETE FROM schedule_series_staff
WHERE schedule_series_staff.series_id = sqlc.arg(series_id);

-- name: ListStaffForSeries :many
SELECT schedule_series_staff.series_id, schedule_series_staff.staff_user_id
FROM schedule_series_staff
WHERE schedule_series_staff.series_id = ANY(sqlc.arg(series_ids)::text[])
ORDER BY schedule_series_staff.series_id, schedule_series_staff.staff_user_id;

-- AddSeriesAttendee puts one of the studio's live attendees on a slot. It is an
-- INSERT ... SELECT so the attendee is read under the slot's studio: the join
-- table has no studio of its own, and an identifier from another studio, or
-- one archived, writes nothing and the store refuses it.
-- name: AddSeriesAttendee :execrows
INSERT INTO schedule_series_attendees (
	series_id,
	attendee_id
)
SELECT sqlc.arg(series_id), roster_attendees.id
FROM roster_attendees
WHERE roster_attendees.id = sqlc.arg(attendee_id)
	AND roster_attendees.studio_id = sqlc.arg(studio_id)
	AND roster_attendees.archived_at IS NULL
ON CONFLICT (series_id, attendee_id) DO NOTHING;

-- name: ClearSeriesAttendees :exec
DELETE FROM schedule_series_attendees
WHERE schedule_series_attendees.series_id = sqlc.arg(series_id);

-- name: ListAttendeesForSeries :many
SELECT schedule_series_attendees.series_id, schedule_series_attendees.attendee_id
FROM schedule_series_attendees
WHERE schedule_series_attendees.series_id = ANY(sqlc.arg(series_ids)::text[])
ORDER BY schedule_series_attendees.series_id, schedule_series_attendees.created_at, schedule_series_attendees.attendee_id;

-- ListLiveSeriesForAttendees is what the roster asks before archiving somebody:
-- the slots naming them that have not ended. A slot ending today still counts.
-- name: ListLiveSeriesForAttendees :many
SELECT DISTINCT schedule_series_attendees.attendee_id, schedule_series.id
FROM schedule_series_attendees
	JOIN schedule_series ON schedule_series.id = schedule_series_attendees.series_id
WHERE schedule_series.studio_id = sqlc.arg(studio_id)
	AND schedule_series.archived_at IS NULL
	AND (schedule_series.ends_on IS NULL OR schedule_series.ends_on >= sqlc.arg(today)::date)
	AND schedule_series_attendees.attendee_id = ANY(sqlc.arg(attendee_ids)::text[])
ORDER BY schedule_series_attendees.attendee_id, schedule_series.id;
