-- Permanent request identities use exactly the financial ledger's universe:
-- procurement receipts, plus legacy identities not yet promoted to a receipt.
-- No account cost, user debit, financial trigger or historical price is changed.
-- MAINTENANCE REQUIRED: preflight source sizes / runtime on a production-sized
-- copy. This one-time backfill scans permanent evidence and holds write locks.
-- The migration runner must execute this entire file in one transaction.
SET LOCAL lock_timeout = '5s';
-- Serialize maintenance with the independent drainer before touching rollups.
-- Live writers never acquire this advisory lock or any snapshot/counter lock.
SELECT pg_advisory_xact_lock(955949, 955);
-- Match live write order: 921's alphabetical AFTER triggers capture permanent
-- usage before procurement receipts; 949's receipt trigger then deletes legacy.
-- Avoid taking receipt first while an in-flight writer holds permanent usage.
LOCK TABLE kitty_permanent_usage, kitty_procurement_receipts, kitty_account_cost_legacy
    IN SHARE ROW EXCLUSIVE MODE;

CREATE TABLE IF NOT EXISTS kitty_account_usage_snapshots (
    api_key_id bigint NOT NULL,
    request_key text NOT NULL,
    account_id bigint NOT NULL,
    tokens numeric,
    source text NOT NULL CHECK (source IN ('receipt', 'legacy')),
    PRIMARY KEY (api_key_id, request_key),
    CHECK (tokens IS NULL OR (tokens >= 0 AND tokens < 'Infinity'::numeric AND tokens = trunc(tokens)))
);
CREATE INDEX IF NOT EXISTS kitty_account_usage_snapshot_account
    ON kitty_account_usage_snapshots(account_id);

CREATE TABLE IF NOT EXISTS kitty_account_lifetime_usage (
    account_id bigint PRIMARY KEY,
    requests bigint NOT NULL CHECK (requests >= 0),
    token_sum numeric NOT NULL CHECK (token_sum >= 0 AND token_sum < 'Infinity'::numeric AND token_sum = trunc(token_sum)),
    unknown_requests bigint NOT NULL CHECK (unknown_requests >= 0 AND unknown_requests <= requests)
);

-- JSON numbers such as 100.0 are mathematical integers and are valid. Strings,
-- missing/null fields, fractions, negative or nonfinite values are unknown.
-- Cache TTL sub-buckets are already included in cache_write: never add them.
CREATE OR REPLACE FUNCTION kitty_account_usage_tokens(m jsonb)
RETURNS numeric LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE k text; v numeric; total numeric := 0;
BEGIN
    IF m IS NULL OR jsonb_typeof(m) <> 'object' THEN RETURN NULL; END IF;
    IF m ? 'invalid_metrics' AND (m->'invalid_metrics') IS DISTINCT FROM 'false'::jsonb THEN
        RETURN NULL;
    END IF;
    FOREACH k IN ARRAY ARRAY['input', 'output', 'cache_read', 'cache_write'] LOOP
        IF jsonb_typeof(m->k) IS DISTINCT FROM 'number' THEN RETURN NULL; END IF;
        v := (m->>k)::numeric;
        IF v < 0 OR v >= 'Infinity'::numeric OR v <> trunc(v) THEN RETURN NULL; END IF;
        total := total + v;
    END LOOP;
    RETURN total;
EXCEPTION WHEN numeric_value_out_of_range OR invalid_text_representation THEN
    RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION kitty_account_usage_permanent_tokens(p kitty_permanent_usage)
RETURNS numeric LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN p.invalid_metrics IS FALSE THEN kitty_account_usage_tokens(
        jsonb_build_object('input', p.input_tokens, 'output', p.output_tokens,
            'cache_read', p.cache_read_tokens, 'cache_write', p.cache_creation_tokens)) END;
$$;

CREATE OR REPLACE FUNCTION kitty_adjust_account_lifetime_usage(a bigint, r bigint, t numeric, u bigint)
RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    -- Insert a zero row first: negative deltas must not violate INSERT checks
    -- before ON CONFLICT can acquire the existing account's row lock.
    INSERT INTO kitty_account_lifetime_usage(account_id, requests, token_sum, unknown_requests)
        VALUES (a, 0, 0, 0) ON CONFLICT (account_id) DO NOTHING;
    UPDATE kitty_account_lifetime_usage SET requests = requests + r,
        token_sum = token_sum + t, unknown_requests = unknown_requests + u
        WHERE account_id = a;
END;
$$;

CREATE OR REPLACE FUNCTION kitty_account_usage_snapshot_rollup()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_unknown bigint := 0; new_unknown bigint := CASE WHEN NEW.tokens IS NULL THEN 1 ELSE 0 END;
BEGIN
    IF TG_OP = 'INSERT' THEN
        PERFORM kitty_adjust_account_lifetime_usage(NEW.account_id, 1, COALESCE(NEW.tokens, 0), new_unknown);
    ELSE
        old_unknown := CASE WHEN OLD.tokens IS NULL THEN 1 ELSE 0 END;
        IF OLD.account_id = NEW.account_id THEN
            PERFORM kitty_adjust_account_lifetime_usage(NEW.account_id, 0,
                COALESCE(NEW.tokens, 0) - COALESCE(OLD.tokens, 0), new_unknown - old_unknown);
        ELSIF OLD.account_id < NEW.account_id THEN
            -- Lock account rows in ascending order for cross-account corrections.
            PERFORM kitty_adjust_account_lifetime_usage(OLD.account_id, -1, -COALESCE(OLD.tokens, 0), -old_unknown);
            PERFORM kitty_adjust_account_lifetime_usage(NEW.account_id, 1, COALESCE(NEW.tokens, 0), new_unknown);
        ELSE
            PERFORM kitty_adjust_account_lifetime_usage(NEW.account_id, 1, COALESCE(NEW.tokens, 0), new_unknown);
            PERFORM kitty_adjust_account_lifetime_usage(OLD.account_id, -1, -COALESCE(OLD.tokens, 0), -old_unknown);
        END IF;
    END IF;
    RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION kitty_upsert_account_usage_snapshot(a bigint, k text, acct bigint, t numeric, origin text)
RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO kitty_account_usage_snapshots AS s(api_key_id, request_key, account_id, tokens, source)
        VALUES (a, k, acct, t, origin)
    ON CONFLICT (api_key_id, request_key) DO UPDATE
        SET account_id = EXCLUDED.account_id, tokens = EXCLUDED.tokens, source = EXCLUDED.source
        WHERE (EXCLUDED.source = 'receipt' OR s.source = 'legacy')
          AND (s.account_id, s.tokens, s.source) IS DISTINCT FROM
              (EXCLUDED.account_id, EXCLUDED.tokens, EXCLUDED.source);
END;
$$;

-- An append-only identity journal decouples source transactions from rollups.
-- Never deduplicate with a request-key UNIQUE constraint: concurrent identities
-- must not introduce a journal -> source/financial lock dependency. Sequence
-- allocation order is NOT commit order. No body, measurements or money stored.
CREATE TABLE IF NOT EXISTS kitty_account_usage_events (
    id bigserial PRIMARY KEY,
    api_key_id bigint NOT NULL,
    request_key text NOT NULL,
    account_id bigint NOT NULL
);
CREATE INDEX IF NOT EXISTS kitty_account_usage_event_account
    ON kitty_account_usage_events(account_id);
CREATE INDEX IF NOT EXISTS kitty_account_usage_event_identity
    ON kitty_account_usage_events(api_key_id, request_key);

CREATE OR REPLACE FUNCTION kitty_account_usage_receipt_capture()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO kitty_account_usage_events(api_key_id, request_key, account_id)
        VALUES (NEW.api_key_id, NEW.request_key, NEW.account_id);
    RETURN NULL;
END;
$$;
CREATE OR REPLACE FUNCTION kitty_account_usage_legacy_capture()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO kitty_account_usage_events(api_key_id, request_key, account_id)
        VALUES (NEW.api_key_id, NEW.request_key, NEW.account_id);
    RETURN NULL;
END;
$$;
CREATE OR REPLACE FUNCTION kitty_account_usage_permanent_capture()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    -- An orphan permanent row has no financial account identity. A later
    -- legacy/receipt insert queues its own event. This MVCC read takes no row
    -- locks; receipt promotion/correction always appends its own committed event.
    INSERT INTO kitty_account_usage_events(api_key_id, request_key, account_id)
    SELECT l.api_key_id, l.request_key, l.account_id FROM kitty_account_cost_legacy l
    WHERE (l.api_key_id, l.request_key) = (NEW.api_key_id, NEW.request_key)
      AND NOT EXISTS (SELECT FROM kitty_procurement_receipts r
          WHERE (r.api_key_id, r.request_key) = (l.api_key_id, l.request_key));
    RETURN NULL;
END;
$$;

-- Call ONLY in an independent short transaction with no source/financial write
-- locks. Commit before the read-side snapshot/pending query. A busy drainer
-- returns immediately; pending accounts must be reported as unknown by readers.
CREATE OR REPLACE FUNCTION kitty_drain_account_usage_events(batch_size integer DEFAULT 2048)
RETURNS integer LANGUAGE plpgsql AS $$
DECLARE captured_ids bigint[]; item record;
BEGIN
    IF batch_size IS NULL OR batch_size < 1 OR batch_size > 2048 THEN
        RAISE EXCEPTION 'account usage batch size must be 1..2048' USING ERRCODE = '22023';
    END IF;
    IF NOT pg_try_advisory_xact_lock(955949, 955) THEN RETURN 0; END IF;

    SELECT array_agg(batch.id ORDER BY batch.id) INTO captured_ids
    FROM (SELECT id FROM kitty_account_usage_events ORDER BY id LIMIT batch_size) batch;
    IF captured_ids IS NULL THEN RETURN 0; END IF;

    -- Resolve current canonical evidence, not event-time measurements. Each
    -- identity is processed once per batch. Only this processor writes snapshots
    -- and counters; live triggers touch neither. OLD snapshot preserves the old
    -- account for account moves. No financial, source-row or event-row lock.
    FOR item IN
        SELECT e.api_key_id, e.request_key, COALESCE(r.account_id, l.account_id) AS account_id,
            CASE WHEN r.api_key_id IS NOT NULL THEN kitty_account_usage_tokens(r.measurements)
                 ELSE kitty_account_usage_permanent_tokens(p) END AS tokens,
            CASE WHEN r.api_key_id IS NOT NULL THEN 'receipt' ELSE 'legacy' END AS source
        FROM (SELECT DISTINCT api_key_id, request_key FROM kitty_account_usage_events
              WHERE id = ANY(captured_ids)) e
        LEFT JOIN kitty_procurement_receipts r
          ON (r.api_key_id, r.request_key) = (e.api_key_id, e.request_key)
        LEFT JOIN kitty_account_cost_legacy l
          ON (l.api_key_id, l.request_key) = (e.api_key_id, e.request_key) AND r.api_key_id IS NULL
        LEFT JOIN kitty_permanent_usage p
          ON (p.api_key_id, p.request_key) = (e.api_key_id, e.request_key) AND r.api_key_id IS NULL
        WHERE r.api_key_id IS NOT NULL OR l.api_key_id IS NOT NULL
        ORDER BY e.api_key_id, e.request_key
    LOOP
        PERFORM kitty_upsert_account_usage_snapshot(item.api_key_id, item.request_key,
            item.account_id, item.tokens, item.source);
    END LOOP;
    -- Delete EXACTLY the visible captured set. A smaller sequence id may still
    -- be uncommitted at capture time; id <= max(captured_ids) would lose it.
    DELETE FROM kitty_account_usage_events WHERE id = ANY(captured_ids);
    RETURN cardinality(captured_ids);
END;
$$;

-- Sources are locked, so initial/repeated backfill can use set-based aggregation
-- instead of one hot-account counter UPDATE per historical request.
DROP TRIGGER IF EXISTS kitty_account_usage_snapshot_change ON kitty_account_usage_snapshots;
INSERT INTO kitty_account_usage_snapshots AS s(api_key_id, request_key, account_id, tokens, source)
SELECT r.api_key_id, r.request_key, r.account_id, kitty_account_usage_tokens(r.measurements), 'receipt'
FROM kitty_procurement_receipts r
UNION ALL
SELECT l.api_key_id, l.request_key, l.account_id, kitty_account_usage_permanent_tokens(p), 'legacy'
FROM kitty_account_cost_legacy l
LEFT JOIN kitty_permanent_usage p ON (p.api_key_id, p.request_key) = (l.api_key_id, l.request_key)
WHERE NOT EXISTS (SELECT FROM kitty_procurement_receipts r
    WHERE (r.api_key_id, r.request_key) = (l.api_key_id, l.request_key))
ON CONFLICT (api_key_id, request_key) DO UPDATE
    SET account_id = EXCLUDED.account_id, tokens = EXCLUDED.tokens, source = EXCLUDED.source
    WHERE (EXCLUDED.source = 'receipt' OR s.source = 'legacy')
      AND (s.account_id, s.tokens, s.source) IS DISTINCT FROM
          (EXCLUDED.account_id, EXCLUDED.tokens, EXCLUDED.source);

-- Rebuild only our own counters; never delete stable request snapshots or touch
-- the financial rollup. Reapplying this migration is count/token idempotent.
DELETE FROM kitty_account_lifetime_usage;
INSERT INTO kitty_account_lifetime_usage(account_id, requests, token_sum, unknown_requests)
SELECT account_id, COUNT(*), COALESCE(SUM(tokens), 0), COUNT(*) FILTER (WHERE tokens IS NULL)
FROM kitty_account_usage_snapshots GROUP BY account_id;

CREATE TRIGGER kitty_account_usage_snapshot_change AFTER INSERT OR UPDATE OF account_id, tokens
    ON kitty_account_usage_snapshots FOR EACH ROW EXECUTE FUNCTION kitty_account_usage_snapshot_rollup();
DROP TRIGGER IF EXISTS kitty_account_usage_receipt_write ON kitty_procurement_receipts;
CREATE TRIGGER kitty_account_usage_receipt_write AFTER INSERT OR UPDATE OF account_id, measurements
    ON kitty_procurement_receipts FOR EACH ROW EXECUTE FUNCTION kitty_account_usage_receipt_capture();
DROP TRIGGER IF EXISTS kitty_account_usage_legacy_write ON kitty_account_cost_legacy;
CREATE TRIGGER kitty_account_usage_legacy_write AFTER INSERT OR UPDATE OF account_id
    ON kitty_account_cost_legacy FOR EACH ROW EXECUTE FUNCTION kitty_account_usage_legacy_capture();
DROP TRIGGER IF EXISTS kitty_account_usage_permanent_write ON kitty_permanent_usage;
CREATE TRIGGER kitty_account_usage_permanent_write AFTER INSERT OR UPDATE OF input_tokens, output_tokens,
    cache_read_tokens, cache_creation_tokens, invalid_metrics ON kitty_permanent_usage
    FOR EACH ROW EXECUTE FUNCTION kitty_account_usage_permanent_capture();
-- Intentionally no DELETE hook on legacy: migration 949 deletes legacy during
-- receipt promotion. The queued receipt resolves the same stable identity once.
-- No FKs to live accounts/API keys/usage_logs: retention must not erase evidence.
-- Repeat migrations preserve pending events. Backfill already resolves their
-- canonical state, so draining those identities later is an idempotent no-op.
-- Remove only the obsolete synchronous helper from the pre-release prototype.
DROP FUNCTION IF EXISTS kitty_capture_account_usage_legacy(bigint, text);
