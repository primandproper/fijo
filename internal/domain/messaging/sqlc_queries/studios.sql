-- name: GetStudioSettings :one
SELECT *
FROM messaging_studios
WHERE messaging_studios.studio_id = sqlc.arg(studio_id);

-- SaveStudioSettings writes the switch and the reply address. Turning
-- reminders on stamps when, and leaving them on keeps the stamp it had: the
-- stamp is what a reminder owed before it is held back by.
-- name: SaveStudioSettings :one
INSERT INTO messaging_studios (
	studio_id,
	reminders_on,
	reminders_on_since,
	reply_to
) VALUES (
	sqlc.arg(studio_id),
	sqlc.arg(reminders_on),
	CASE WHEN sqlc.arg(reminders_on)::boolean THEN NOW() END,
	sqlc.arg(reply_to)
)
ON CONFLICT (studio_id) DO UPDATE SET
	reminders_on = EXCLUDED.reminders_on,
	reminders_on_since = CASE
		WHEN EXCLUDED.reminders_on AND NOT messaging_studios.reminders_on THEN NOW()
		ELSE messaging_studios.reminders_on_since
	END,
	reply_to = EXCLUDED.reply_to,
	last_updated_at = NOW()
RETURNING *;
