-- Kitty 0.2.24.3-kitty.3: A-level schema parity (additive & idempotent).
-- Adds:
--   1. api_keys.fallback_group_id (optional fallback group for API keys)
--   2. user_group_rate_multipliers.denied_models (per-user denied models in a group)
--   3. account_performance_monitors & account_performance_events (V2 account performance rules)
--   4. new_api_site_authorizations & new_api_account_bindings + identity guard trigger

SET LOCAL lock_timeout = '5s';

-- 1. API Key fallback group
ALTER TABLE public.api_keys
    ADD COLUMN IF NOT EXISTS fallback_group_id BIGINT REFERENCES public.groups(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_api_keys_fallback_group_id
    ON public.api_keys (fallback_group_id)
    WHERE fallback_group_id IS NOT NULL;

-- 2. User group denied models
ALTER TABLE public.user_group_rate_multipliers
    ADD COLUMN IF NOT EXISTS denied_models JSONB;

-- 3. Account performance monitors & events (V2 schema matching Kitty .36)
CREATE TABLE IF NOT EXISTS public.account_performance_monitors (
    account_id BIGINT PRIMARY KEY REFERENCES public.accounts(id) ON DELETE CASCADE,
    config JSONB NOT NULL DEFAULT '{"version":2,"enabled":false,"min_samples":20,"auto_recover":true,"cooldown_minutes":5,"recovery_samples":3}',
    revision BIGINT NOT NULL DEFAULT 0,
    paused BOOLEAN NOT NULL DEFAULT false,
    pause_generation BIGINT NOT NULL DEFAULT 0,
    paused_at TIMESTAMPTZ,
    reason TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    trial_started_at TIMESTAMPTZ,
    trial_requests BIGINT NOT NULL DEFAULT 0,
    next_trial_at TIMESTAMPTZ,
    metrics_since TIMESTAMPTZ
);

ALTER TABLE public.account_performance_monitors ADD COLUMN IF NOT EXISTS trial_started_at TIMESTAMPTZ;
ALTER TABLE public.account_performance_monitors ADD COLUMN IF NOT EXISTS trial_requests BIGINT NOT NULL DEFAULT 0;
ALTER TABLE public.account_performance_monitors ADD COLUMN IF NOT EXISTS next_trial_at TIMESTAMPTZ;
ALTER TABLE public.account_performance_monitors ADD COLUMN IF NOT EXISTS metrics_since TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS account_performance_paused_idx
    ON public.account_performance_monitors (account_id)
    WHERE paused;

CREATE TABLE IF NOT EXISTS public.account_performance_events (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES public.accounts(id) ON DELETE CASCADE,
    action TEXT NOT NULL CHECK (action IN ('configure','pause','release','trial','recover','retire')),
    actor_id BIGINT,
    revision BIGINT NOT NULL,
    pause_generation BIGINT NOT NULL,
    evidence JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS account_performance_events_account_idx
    ON public.account_performance_events (account_id, id DESC);

-- 4. New API upstream billing site authorizations & account bindings
CREATE SEQUENCE IF NOT EXISTS public.new_api_site_authorizations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

CREATE TABLE IF NOT EXISTS public.new_api_site_authorizations (
    id BIGINT NOT NULL DEFAULT nextval('public.new_api_site_authorizations_id_seq'::regclass),
    site_url TEXT NOT NULL,
    upstream_user_id BIGINT NOT NULL,
    access_token_ciphertext TEXT NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT new_api_site_authorizations_revision_check CHECK (revision > 0),
    CONSTRAINT new_api_site_authorizations_upstream_user_id_check CHECK (upstream_user_id > 0)
);

ALTER SEQUENCE public.new_api_site_authorizations_id_seq OWNED BY public.new_api_site_authorizations.id;

DO $kitty_schema$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='public.new_api_site_authorizations'::regclass AND conname='new_api_site_authorizations_pkey') THEN
        ALTER TABLE ONLY public.new_api_site_authorizations
            ADD CONSTRAINT new_api_site_authorizations_pkey PRIMARY KEY (id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='public.new_api_site_authorizations'::regclass AND conname='new_api_site_authorizations_site_url_upstream_user_id_key') THEN
        ALTER TABLE ONLY public.new_api_site_authorizations
            ADD CONSTRAINT new_api_site_authorizations_site_url_upstream_user_id_key UNIQUE (site_url, upstream_user_id);
    END IF;
END $kitty_schema$;

CREATE TABLE IF NOT EXISTS public.new_api_account_bindings (
    account_id BIGINT NOT NULL,
    authorization_id BIGINT NOT NULL,
    upstream_token_id BIGINT NOT NULL,
    account_fingerprint TEXT NOT NULL,
    token_group TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT new_api_account_bindings_upstream_token_id_check CHECK (upstream_token_id > 0)
);

DO $kitty_schema$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='public.new_api_account_bindings'::regclass AND conname='new_api_account_bindings_pkey') THEN
        ALTER TABLE ONLY public.new_api_account_bindings
            ADD CONSTRAINT new_api_account_bindings_pkey PRIMARY KEY (account_id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='public.new_api_account_bindings'::regclass AND conname='new_api_account_bindings_account_id_fkey') THEN
        ALTER TABLE ONLY public.new_api_account_bindings
            ADD CONSTRAINT new_api_account_bindings_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='public.new_api_account_bindings'::regclass AND conname='new_api_account_bindings_authorization_id_fkey') THEN
        ALTER TABLE ONLY public.new_api_account_bindings
            ADD CONSTRAINT new_api_account_bindings_authorization_id_fkey FOREIGN KEY (authorization_id) REFERENCES public.new_api_site_authorizations(id) ON DELETE CASCADE;
    END IF;
END $kitty_schema$;

CREATE INDEX IF NOT EXISTS new_api_account_bindings_authorization_id_idx
    ON public.new_api_account_bindings USING btree (authorization_id);

CREATE OR REPLACE FUNCTION public.invalidate_new_api_account_binding()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $function$
BEGIN
    IF OLD.platform IS DISTINCT FROM NEW.platform
       OR OLD.type IS DISTINCT FROM NEW.type
       OR OLD.proxy_id IS DISTINCT FROM NEW.proxy_id
       OR OLD.credentials -> 'api_key' IS DISTINCT FROM NEW.credentials -> 'api_key'
       OR OLD.credentials -> 'base_url' IS DISTINCT FROM NEW.credentials -> 'base_url'
       OR OLD.credentials -> 'header_override_enabled' IS DISTINCT FROM NEW.credentials -> 'header_override_enabled'
       OR OLD.credentials -> 'header_overrides' IS DISTINCT FROM NEW.credentials -> 'header_overrides'
    THEN
        DELETE FROM new_api_account_bindings WHERE account_id = OLD.id;
        IF FOUND OR OLD.extra ->> 'upstream_billing_provider' = 'new_api' THEN
            NEW.extra := COALESCE(NEW.extra, '{}'::jsonb) - 'upstream_billing_provider' - 'upstream_billing_probe';
        END IF;
    END IF;
    RETURN NEW;
END;
$function$;

DROP TRIGGER IF EXISTS new_api_account_binding_identity_guard ON public.accounts;
CREATE TRIGGER new_api_account_binding_identity_guard
    BEFORE UPDATE ON public.accounts
    FOR EACH ROW EXECUTE FUNCTION public.invalidate_new_api_account_binding();
