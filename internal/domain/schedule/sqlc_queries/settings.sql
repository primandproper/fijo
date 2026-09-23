-- name: GetStudioSettings :one
SELECT
	schedule_studio_settings.studio_id,
	schedule_studio_settings.notice_window_minutes,
	schedule_studio_settings.horizon_weeks,
	schedule_studio_settings.created_at,
	schedule_studio_settings.last_updated_at
FROM schedule_studio_settings
WHERE schedule_studio_settings.studio_id = sqlc.arg(studio_id);

-- name: UpsertStudioSettings :one
INSERT INTO schedule_studio_settings (
	studio_id,
	notice_window_minutes,
	horizon_weeks
) VALUES (
	sqlc.arg(studio_id),
	sqlc.arg(notice_window_minutes),
	sqlc.arg(horizon_weeks)
)
ON CONFLICT (studio_id) DO UPDATE SET
	notice_window_minutes = EXCLUDED.notice_window_minutes,
	horizon_weeks = EXCLUDED.horizon_weeks,
	last_updated_at = NOW()
RETURNING
	schedule_studio_settings.studio_id,
	schedule_studio_settings.notice_window_minutes,
	schedule_studio_settings.horizon_weeks,
	schedule_studio_settings.created_at,
	schedule_studio_settings.last_updated_at;
