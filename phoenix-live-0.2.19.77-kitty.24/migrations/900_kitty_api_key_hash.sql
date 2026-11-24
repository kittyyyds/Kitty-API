-- Kitty: authenticate API keys migrated from the Kitty main site.
--
-- Kitty stores api_keys.key as '$sha256$' || sha256(raw key, UTF-8, lowercase hex) || '$' || display mask.
-- The digest is plain SHA-256: no salt, no HMAC, no server secret. Keys created by CS keep the
-- upstream plaintext storage; both formats coexist. Additive only: no existing row is rewritten.

-- Same names and types as Kitty, so migrated rows can be copied column by column.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_hash character varying(64);
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_prefix character varying(16);

-- Auth lookup matches the digest segment: substr(key, 9, 64) = sha256(raw).
CREATE INDEX IF NOT EXISTS idx_api_keys_kitty_key_digest ON api_keys (substr(key, 9, 64));

-- Auth cache key for a stored value. A digest-stored key's cache key is its embedded digest
-- (= sha256(raw) = the key the gateway caches under); plaintext keys keep the upstream formula.
CREATE OR REPLACE FUNCTION kitty_api_key_cache_digest(stored_key TEXT)
RETURNS TEXT LANGUAGE sql IMMUTABLE STRICT AS $kitty$
    SELECT CASE WHEN stored_key ~ '^[$]sha256[$][0-9a-f]{64}[$]'
        THEN substr(stored_key, 9, 64)
        ELSE encode(sha256(convert_to(stored_key, 'UTF8')), 'hex') END;
$kitty$;

-- Kitty also has historical hash-only rows (key empty, key_hash = sha256(raw), key_prefix for display).
-- Store them in the same digest format on import so lookup, display and invalidation need one path.
-- sha256('') = e3b0c442...: an empty key that Kitty's digest migration converted is not a credential.
CREATE OR REPLACE FUNCTION kitty_api_keys_fill_digest_key()
RETURNS TRIGGER LANGUAGE plpgsql AS $kitty$
BEGIN
    IF NEW.key_hash ~ '^[0-9a-fA-F]{64}$'
       AND (NEW.key IS NULL OR NEW.key = ''
            OR NEW.key ~ '^[$]sha256[$]e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855[$]') THEN
        NEW.key := '$sha256$' || lower(NEW.key_hash) || '$' || COALESCE(NEW.key_prefix, '') || '********';
    END IF;
    RETURN NEW;
END;
$kitty$;

DROP TRIGGER IF EXISTS trg_api_keys_kitty_fill_digest_key ON api_keys;
CREATE TRIGGER trg_api_keys_kitty_fill_digest_key
BEFORE INSERT OR UPDATE OF key, key_hash, key_prefix ON api_keys
FOR EACH ROW EXECUTE FUNCTION kitty_api_keys_fill_digest_key();

-- Durable invalidation triggers: same bodies as the latest upstream versions
-- (184 / 193), with the cache key computed by kitty_api_key_cache_digest.
CREATE OR REPLACE FUNCTION enqueue_auth_cache_invalidation(raw_key TEXT)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
    IF raw_key IS NULL OR raw_key = '' THEN
        RETURN;
    END IF;
    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    VALUES (kitty_api_key_cache_digest(raw_key));
END;
$$;

CREATE OR REPLACE FUNCTION enqueue_user_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_user_id BIGINT;
BEGIN
    target_user_id := OLD.id;
    IF TG_OP = 'UPDATE'
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.role IS NOT DISTINCT FROM NEW.role
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at THEN
        RETURN NEW;
    END IF;

    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    SELECT kitty_api_key_cache_digest(k.key)
    FROM api_keys AS k
    WHERE k.user_id = target_user_id
      AND k.deleted_at IS NULL
      AND k.key <> '';
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION enqueue_group_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_group_id BIGINT;
BEGIN
    target_group_id := OLD.id;
    IF TG_OP = 'UPDATE'
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.is_exclusive IS NOT DISTINCT FROM NEW.is_exclusive
       AND OLD.allow_image_generation IS NOT DISTINCT FROM NEW.allow_image_generation
       AND OLD.platform IS NOT DISTINCT FROM NEW.platform
       AND OLD.subscription_type IS NOT DISTINCT FROM NEW.subscription_type
       AND OLD.rate_multiplier IS NOT DISTINCT FROM NEW.rate_multiplier
       AND OLD.peak_rate_enabled IS NOT DISTINCT FROM NEW.peak_rate_enabled
       AND OLD.peak_start IS NOT DISTINCT FROM NEW.peak_start
       AND OLD.peak_end IS NOT DISTINCT FROM NEW.peak_end
       AND OLD.peak_rate_multiplier IS NOT DISTINCT FROM NEW.peak_rate_multiplier
       AND OLD.profit_control_enabled IS NOT DISTINCT FROM NEW.profit_control_enabled
       AND OLD.profit_min_margin IS NOT DISTINCT FROM NEW.profit_min_margin
       AND OLD.profit_safety_buffer IS NOT DISTINCT FROM NEW.profit_safety_buffer
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at THEN
        RETURN NEW;
    END IF;

    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    SELECT kitty_api_key_cache_digest(k.key)
    FROM api_keys AS k
    WHERE k.group_id = target_group_id
      AND k.deleted_at IS NULL
      AND k.key <> '';
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION enqueue_allowed_group_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_user_id BIGINT;
    target_group_id BIGINT;
BEGIN
    IF TG_OP = 'UPDATE'
       AND (OLD.user_id IS DISTINCT FROM NEW.user_id
            OR OLD.group_id IS DISTINCT FROM NEW.group_id) THEN
        IF EXISTS (
            SELECT 1 FROM groups g
            WHERE g.id = OLD.group_id AND g.is_exclusive = TRUE
        ) THEN
            INSERT INTO auth_cache_invalidation_outbox (cache_key)
            SELECT kitty_api_key_cache_digest(k.key)
            FROM api_keys AS k
            WHERE k.user_id = OLD.user_id
              AND k.group_id = OLD.group_id
              AND k.deleted_at IS NULL
              AND k.key <> '';
        END IF;
        target_user_id := NEW.user_id;
        target_group_id := NEW.group_id;
    ELSIF TG_OP = 'UPDATE' THEN
        RETURN NEW;
    ELSIF TG_OP = 'INSERT' THEN
        target_user_id := NEW.user_id;
        target_group_id := NEW.group_id;
    ELSE
        target_user_id := OLD.user_id;
        target_group_id := OLD.group_id;
    END IF;

    IF EXISTS (
        SELECT 1 FROM groups g
        WHERE g.id = target_group_id AND g.is_exclusive = TRUE
    ) THEN
        INSERT INTO auth_cache_invalidation_outbox (cache_key)
        SELECT kitty_api_key_cache_digest(k.key)
        FROM api_keys AS k
        WHERE k.user_id = target_user_id
          AND k.group_id = target_group_id
          AND k.deleted_at IS NULL
          AND k.key <> '';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;
