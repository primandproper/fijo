-- ReminderCandidates is every (lesson, household, contact) in the week ahead
-- whose household's earlier reminder has come due, in a studio with reminders
-- on: the rows which reminder, if any, is owed now is decided from. One row per
-- contact per lesson, with the household's attendees on the lesson gathered,
-- and the smallest offset already written for the pair so the caller can tell
-- a reminder owed from one already sent.
--
-- other_attendees is the household's attendees on the lesson other than the
-- contact, newline-separated in name order (a name is one line), and
-- smallest_written is zero when nothing has been written for the pair.
-- name: ReminderCandidates :many
SELECT
	schedule_occurrences.id AS occurrence_id,
	schedule_occurrences.studio_id,
	schedule_occurrences.starts_at,
	schedule_occurrences.created_at AS occurrence_created_at,
	schedule_offerings.name_en AS offering_name_en,
	schedule_offerings.name_es AS offering_name_es,
	roster_households.id AS household_id,
	roster_households.first_reminder_minutes,
	roster_households.second_reminder_minutes,
	roster_contacts.id AS contact_id,
	roster_contacts.name AS contact_name,
	roster_contacts.email AS contact_email,
	roster_contacts.language AS contact_language,
	messaging_studios.reminders_on_since,
	messaging_studios.reply_to,
	COALESCE(
		string_agg(roster_attendees.name, E'\n' ORDER BY roster_attendees.name)
			FILTER (WHERE roster_attendees.contact_id IS DISTINCT FROM roster_contacts.id),
		''
	)::text AS other_attendees,
	bool_or(roster_attendees.contact_id IS NOT DISTINCT FROM roster_contacts.id)::boolean AS contact_attends,
	COALESCE((
		SELECT min(messaging_reminders.offset_minutes)
		FROM messaging_reminders
		WHERE messaging_reminders.occurrence_id = schedule_occurrences.id
			AND messaging_reminders.contact_id = roster_contacts.id
	), 0)::integer AS smallest_written
FROM schedule_occurrences
JOIN messaging_studios ON messaging_studios.studio_id = schedule_occurrences.studio_id
	AND messaging_studios.reminders_on
JOIN schedule_offerings ON schedule_offerings.id = schedule_occurrences.offering_id
JOIN schedule_occurrence_attendees ON schedule_occurrence_attendees.occurrence_id = schedule_occurrences.id
JOIN roster_attendees ON roster_attendees.id = schedule_occurrence_attendees.attendee_id
JOIN roster_households ON roster_households.id = roster_attendees.household_id
	AND roster_households.archived_at IS NULL
JOIN roster_contacts ON roster_contacts.household_id = roster_households.id
	AND roster_contacts.archived_at IS NULL
	AND NOT roster_contacts.reminders_off
	AND roster_contacts.email IS NOT NULL
WHERE schedule_occurrences.state = 'scheduled'
	AND schedule_occurrences.archived_at IS NULL
	AND schedule_occurrences.starts_at > sqlc.arg(now)::timestamptz
	AND schedule_occurrences.starts_at <= sqlc.arg(horizon)::timestamptz
	AND schedule_occurrences.starts_at - GREATEST(roster_households.first_reminder_minutes, roster_households.second_reminder_minutes) * INTERVAL '1 minute'
		<= sqlc.arg(now)::timestamptz
GROUP BY
	schedule_occurrences.id,
	schedule_offerings.id,
	roster_households.id,
	roster_contacts.id,
	messaging_studios.studio_id
ORDER BY schedule_occurrences.starts_at, schedule_occurrences.id, roster_contacts.id;

-- WriteReminder records one reminder as owed. A second writer of the same one
-- writes nothing, which is how it goes once across the fleet.
-- name: WriteReminder :execrows
INSERT INTO messaging_reminders (
	id,
	studio_id,
	occurrence_id,
	household_id,
	contact_id,
	offset_minutes,
	to_address,
	to_name,
	language,
	subject,
	body_html
) VALUES (
	sqlc.arg(id),
	sqlc.arg(studio_id),
	sqlc.arg(occurrence_id),
	sqlc.arg(household_id),
	sqlc.arg(contact_id),
	sqlc.arg(offset_minutes),
	sqlc.arg(to_address),
	sqlc.arg(to_name),
	sqlc.arg(language),
	sqlc.arg(subject),
	sqlc.arg(body_html)
)
ON CONFLICT (occurrence_id, contact_id, offset_minutes) DO NOTHING;

-- LockReminderForDelivery reads a reminder with what decides whether it should
-- still go, and holds it, so a second firing of the same timer waits and then
-- finds it sent.
-- name: LockReminderForDelivery :one
SELECT
	messaging_reminders.*,
	schedule_occurrences.state AS occurrence_state,
	schedule_occurrences.starts_at AS occurrence_starts_at,
	(roster_contacts.archived_at IS NULL AND NOT roster_contacts.reminders_off)::boolean AS contact_wants_it,
	messaging_studios.reminders_on,
	messaging_studios.reply_to
FROM messaging_reminders
JOIN schedule_occurrences ON schedule_occurrences.id = messaging_reminders.occurrence_id
JOIN roster_contacts ON roster_contacts.id = messaging_reminders.contact_id
JOIN messaging_studios ON messaging_studios.studio_id = messaging_reminders.studio_id
WHERE messaging_reminders.id = sqlc.arg(id)
FOR UPDATE OF messaging_reminders;

-- name: MarkReminderSent :exec
UPDATE messaging_reminders SET
	status = 'sent',
	sent_at = NOW(),
	attempts = attempts + 1,
	reason = '',
	last_updated_at = NOW()
WHERE messaging_reminders.id = sqlc.arg(id);

-- MarkReminderUnsent retires a reminder that did not go: skipped, for a lesson
-- that stopped being owed one, or failed, for a provider that refused it
-- past every try.
-- name: MarkReminderUnsent :exec
UPDATE messaging_reminders SET
	status = sqlc.arg(status),
	reason = sqlc.arg(reason),
	last_updated_at = NOW()
WHERE messaging_reminders.id = sqlc.arg(id);

-- name: RecordReminderAttempt :one
UPDATE messaging_reminders SET
	attempts = attempts + 1,
	reason = sqlc.arg(reason),
	last_updated_at = NOW()
WHERE messaging_reminders.id = sqlc.arg(id)
RETURNING messaging_reminders.attempts;

-- ListReminders reads a studio's reminders, newest first, with the lesson's
-- time and the contact's name as they are now; household_id narrows it to one
-- household when it is not empty.
-- name: ListReminders :many
SELECT
	messaging_reminders.*,
	schedule_occurrences.starts_at AS occurrence_starts_at,
	roster_contacts.name AS contact_name,
	roster_households.name AS household_name
FROM messaging_reminders
JOIN schedule_occurrences ON schedule_occurrences.id = messaging_reminders.occurrence_id
JOIN roster_contacts ON roster_contacts.id = messaging_reminders.contact_id
JOIN roster_households ON roster_households.id = messaging_reminders.household_id
WHERE messaging_reminders.studio_id = sqlc.arg(studio_id)
	AND (sqlc.arg(household_id)::text = '' OR messaging_reminders.household_id = sqlc.arg(household_id)::text)
ORDER BY messaging_reminders.created_at DESC, messaging_reminders.id DESC
LIMIT sqlc.arg(row_limit)::integer;
