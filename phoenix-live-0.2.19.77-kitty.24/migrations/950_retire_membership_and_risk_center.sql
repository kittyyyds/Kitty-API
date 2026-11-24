-- An existing temporary hold must finish before its worker is retired. Never
-- activate a manually disabled user (no deadline), or release a hold early.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM users WHERE deleted_at IS NULL AND status = 'disabled'
               AND usage_policy_unban_at > CURRENT_TIMESTAMP) THEN
        RAISE EXCEPTION 'A temporary usage-policy hold is still active; retry after its deadline';
    END IF;
END $$;

UPDATE users SET status = 'active', usage_policy_unban_at = NULL, updated_at = CURRENT_TIMESTAMP
WHERE deleted_at IS NULL AND status = 'disabled' AND usage_policy_unban_at <= CURRENT_TIMESTAMP;

ALTER TABLE users DROP COLUMN IF EXISTS vip, DROP COLUMN IF EXISTS usage_policy_unban_at;
DELETE FROM settings WHERE key IN ('risk_control_enabled', 'usage_policy_config');
-- The independent group policy shares the moderation log table. Do not drop
-- its records or the upstream policy evidence stored alongside them.
