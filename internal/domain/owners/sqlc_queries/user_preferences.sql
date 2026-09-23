-- name: GetUserPreferences :one
SELECT
	user_preferences.user_id,
	user_preferences.scope,
	user_preferences.time_zone,
	user_preferences.created_at,
	user_preferences.last_updated_at
FROM user_preferences
WHERE user_preferences.user_id = sqlc.arg(user_id)
	AND user_preferences.scope = sqlc.arg(scope);

-- name: UpsertUserTimeZone :one
INSERT INTO user_preferences (
	user_id,
	scope,
	time_zone
) VALUES (
	sqlc.arg(user_id),
	sqlc.arg(scope),
	sqlc.arg(time_zone)
)
ON CONFLICT (user_id) DO UPDATE SET
	time_zone = EXCLUDED.time_zone,
	last_updated_at = NOW()
WHERE user_preferences.scope = EXCLUDED.scope
RETURNING
	user_preferences.user_id,
	user_preferences.scope,
	user_preferences.time_zone,
	user_preferences.created_at,
	user_preferences.last_updated_at;
