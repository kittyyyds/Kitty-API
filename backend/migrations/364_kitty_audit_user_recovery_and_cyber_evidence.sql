-- Kitty audit port (ranxi 296/297/298/301): moderation user recovery state, exemptions and cyber-policy evidence.
-- Forward only. Ownership of previously disabled accounts is not inferred from audit logs.

SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '90s';
SET LOCAL search_path = public;

ALTER TABLE users ADD COLUMN IF NOT EXISTS moderation_status_revision BIGINT NOT NULL DEFAULT 0;
CREATE OR REPLACE FUNCTION content_moderation_status_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.moderation_status_revision := OLD.moderation_status_revision + 1;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS content_moderation_status_revision ON users;
-- UPDATE OF fires even for a repeated explicit administrator disable.
CREATE TRIGGER content_moderation_status_revision BEFORE UPDATE OF status ON users
FOR EACH ROW EXECUTE FUNCTION content_moderation_status_revision();

CREATE TABLE IF NOT EXISTS content_moderation_user_state (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    version BIGINT NOT NULL DEFAULT 0,
    generation BIGINT NOT NULL DEFAULT 0,
    auto_ban_exempt BOOLEAN NOT NULL DEFAULT FALSE,
    owned_status_revision BIGINT,
    reason VARCHAR(500) NOT NULL DEFAULT '',
    actor_id BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS content_moderation_user_hits (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    generation BIGINT NOT NULL,
    request_id VARCHAR(256) NOT NULL,
    action VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, generation, request_id)
);
CREATE INDEX IF NOT EXISTS idx_content_moderation_user_hits_window
ON content_moderation_user_hits(user_id, generation, created_at);
CREATE TABLE IF NOT EXISTS content_moderation_user_actions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    actor_id BIGINT NOT NULL,
    action VARCHAR(32) NOT NULL,
    reason VARCHAR(500) NOT NULL,
    version BIGINT NOT NULL,
    auto_ban_exempt BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_content_moderation_user_actions_user ON content_moderation_user_actions(user_id, id DESC);

-- Exemption types (ranxi 298 and 301) in final form. Existing auto-ban exemptions never imply review exemptions.
ALTER TABLE content_moderation_user_state
    ADD COLUMN IF NOT EXISTS keyword_exempt BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS api_audit_exempt BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS email_exempt BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE content_moderation_user_actions
    ADD COLUMN IF NOT EXISTS keyword_exempt BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS api_audit_exempt BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS email_exempt BOOLEAN NOT NULL DEFAULT FALSE;

-- Confirmed upstream cyber events only. No plaintext input is stored here.
CREATE TABLE IF NOT EXISTS cyber_policy_evidence (
 log_id BIGINT PRIMARY KEY REFERENCES content_moderation_logs(id) ON DELETE CASCADE,
 user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
 ciphertext TEXT NOT NULL CHECK (octet_length(ciphertext) BETWEEN 1 AND 524288),
 redaction_version TEXT NOT NULL,
 truncated BOOLEAN NOT NULL,
 original_bytes BIGINT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_cyber_policy_evidence_expiry ON cyber_policy_evidence(expires_at);
CREATE INDEX IF NOT EXISTS idx_cyber_policy_evidence_user ON cyber_policy_evidence(user_id);
