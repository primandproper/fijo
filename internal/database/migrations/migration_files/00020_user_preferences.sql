-- user_preferences is the side table identity's documentation tells a consumer
-- to keep for the columns that are its own. Fijo's one such column is the time
-- zone an owner or staff member reads the grid in, which is not the studio's:
-- the studio's zone is identity_accounts.time_zone, and a teacher who covers a
-- lesson from another city sees it in theirs.
--
-- scope is the identity directory's scope, which is what the user row it
-- extends carries, and not the studio: a person's preference follows them
-- across every studio they belong to.
CREATE TABLE IF NOT EXISTS user_preferences (
    user_id         TEXT PRIMARY KEY REFERENCES identity_users (id) ON DELETE CASCADE,
    scope           TEXT NOT NULL,
    time_zone       TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_updated_at TIMESTAMPTZ
);
