-- Kitty 第 6 阶段：采购成本（供应商、钱包、授权、绑定、采购、成本规则、调整、收据）。
-- 表名、字段名、类型、默认值、约束名与主站 354_kitty_business_and_media.sql 一致，便于原样迁数据。
-- 主键用 BIGSERIAL 写法，生成的序列名（<表>_id_seq）、主键名（<表>_pkey）与主站相同；
-- 迁数据后要把各 *_id_seq 推到 MAX(id)。
-- 收据由 921 里建在 usage_logs 上的触发器 kitty_procurement_write 生成。
-- kitty_procurement_cost 与主站逐字一致：只有 cache_write_30m + cache_write_12h 恰好等于
-- cache_write 时才按 12h 价拆分。CS 的 usage_logs 没有 30m/12h 列，921 里写 0，
-- 所以 Claude 的 5m/1h 缓存写入不会被拆到 OpenAI 的 12h 价格槽。

SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS kitty_suppliers (
    id BIGSERIAL PRIMARY KEY,
    name text NOT NULL CHECK ((length(name) >= 1) AND (length(name) <= 120)),
    site_type text NOT NULL CHECK (site_type = ANY (ARRAY['sub2api'::text, 'newapi'::text, 'manual'::text])),
    base_url text DEFAULT ''::text NOT NULL,
    notes text DEFAULT ''::text NOT NULL,
    active boolean DEFAULT true NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS kitty_suppliers_name ON kitty_suppliers USING btree (lower(name));

CREATE TABLE IF NOT EXISTS kitty_supplier_wallets (
    id BIGSERIAL PRIMARY KEY,
    supplier_id bigint NOT NULL REFERENCES kitty_suppliers(id),
    name text NOT NULL,
    identity text NOT NULL,
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'::text),
    credit_currency text NOT NULL CHECK (credit_currency ~ '^[A-Z]{3}$'::text),
    low_balance numeric CHECK ((low_balance >= (0)::numeric) AND (low_balance < 'Infinity'::numeric)),
    sync_interval_minutes integer DEFAULT 0 NOT NULL CHECK ((sync_interval_minutes = 0) OR ((sync_interval_minutes >= 5) AND (sync_interval_minutes <= 1440))),
    auth_mode text DEFAULT 'none'::text NOT NULL CHECK (auth_mode = ANY (ARRAY['none'::text, 'token'::text, 'password'::text])),
    username text DEFAULT ''::text NOT NULL,
    remote_user_id text DEFAULT ''::text NOT NULL,
    secret_cipher text DEFAULT ''::text NOT NULL,
    balance numeric,
    balance_unit text DEFAULT ''::text NOT NULL,
    balance_source text DEFAULT 'unknown'::text NOT NULL,
    observed_at timestamp with time zone,
    attempted_at timestamp with time zone,
    sync_error text DEFAULT ''::text NOT NULL,
    active boolean DEFAULT true NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT kitty_supplier_wallets_supplier_id_identity_key UNIQUE (supplier_id, identity)
);

CREATE TABLE IF NOT EXISTS kitty_supplier_bindings (
    id BIGSERIAL PRIMARY KEY,
    account_id bigint NOT NULL,
    wallet_id bigint NOT NULL REFERENCES kitty_supplier_wallets(id),
    started_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    ended_at timestamp with time zone,
    CONSTRAINT kitty_supplier_bindings_check CHECK ((ended_at IS NULL) OR (ended_at >= started_at))
);
CREATE UNIQUE INDEX IF NOT EXISTS kitty_supplier_binding_current ON kitty_supplier_bindings USING btree (account_id) WHERE (ended_at IS NULL);
CREATE INDEX IF NOT EXISTS kitty_supplier_binding_history ON kitty_supplier_bindings USING btree (account_id, started_at DESC);
CREATE INDEX IF NOT EXISTS kitty_supplier_binding_wallet ON kitty_supplier_bindings USING btree (wallet_id) WHERE (ended_at IS NULL);

CREATE TABLE IF NOT EXISTS kitty_supplier_cost_rules (
    id BIGSERIAL PRIMARY KEY,
    wallet_id bigint NOT NULL REFERENCES kitty_supplier_wallets(id),
    model text NOT NULL,
    method text NOT NULL CHECK (method = ANY (ARRAY['standard_multiplier'::text, 'account_credits'::text, 'tokens'::text, 'image'::text, 'video_second'::text, 'video_item'::text, 'request'::text])),
    multiplier numeric DEFAULT 1 NOT NULL CHECK ((multiplier >= (0)::numeric) AND (multiplier < 'Infinity'::numeric)),
    input_price numeric DEFAULT 0 NOT NULL CHECK ((input_price >= (0)::numeric) AND (input_price < 'Infinity'::numeric)),
    output_price numeric DEFAULT 0 NOT NULL CHECK ((output_price >= (0)::numeric) AND (output_price < 'Infinity'::numeric)),
    cache_read_price numeric DEFAULT 0 NOT NULL CHECK ((cache_read_price >= (0)::numeric) AND (cache_read_price < 'Infinity'::numeric)),
    cache_write_price numeric DEFAULT 0 NOT NULL CHECK ((cache_write_price >= (0)::numeric) AND (cache_write_price < 'Infinity'::numeric)),
    unit_price numeric DEFAULT 0 NOT NULL CHECK ((unit_price >= (0)::numeric) AND (unit_price < 'Infinity'::numeric)),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'::text),
    note text DEFAULT ''::text NOT NULL,
    fx_to_usd numeric CHECK ((fx_to_usd > (0)::numeric) AND (fx_to_usd < 'Infinity'::numeric)),
    started_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    ended_at timestamp with time zone,
    cache_write_12h_price numeric CHECK ((cache_write_12h_price >= (0)::numeric) AND (cache_write_12h_price < 'Infinity'::numeric)),
    CONSTRAINT kitty_supplier_cost_rules_check CHECK ((ended_at IS NULL) OR (ended_at >= started_at))
);
CREATE UNIQUE INDEX IF NOT EXISTS kitty_supplier_rule_current ON kitty_supplier_cost_rules USING btree (wallet_id, model) WHERE (ended_at IS NULL);
CREATE INDEX IF NOT EXISTS kitty_supplier_rule_history ON kitty_supplier_cost_rules USING btree (wallet_id, model, started_at DESC);

CREATE TABLE IF NOT EXISTS kitty_supplier_credit_rates (
    id BIGSERIAL PRIMARY KEY,
    wallet_id bigint NOT NULL REFERENCES kitty_supplier_wallets(id),
    cash_basis numeric NOT NULL,
    credit_basis numeric NOT NULL,
    rate numeric,
    recorded_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL
);
CREATE INDEX IF NOT EXISTS kitty_supplier_credit_rate_history ON kitty_supplier_credit_rates USING btree (wallet_id, recorded_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS kitty_supplier_purchases (
    id BIGSERIAL PRIMARY KEY,
    wallet_id bigint NOT NULL REFERENCES kitty_supplier_wallets(id),
    operation_id text NOT NULL UNIQUE,
    kind text NOT NULL CHECK (kind = ANY (ARRAY['topup'::text, 'refund'::text, 'opening'::text])),
    cash numeric NOT NULL CHECK ((cash >= (0)::numeric) AND (cash < 'Infinity'::numeric)),
    cash_currency text NOT NULL CHECK (cash_currency ~ '^[A-Z]{3}$'::text),
    fx_rate numeric NOT NULL CHECK ((fx_rate > (0)::numeric) AND (fx_rate < 'Infinity'::numeric)),
    credits numeric NOT NULL CHECK ((credits >= (0)::numeric) AND (credits < 'Infinity'::numeric)),
    bonus numeric DEFAULT 0 NOT NULL CHECK ((bonus >= (0)::numeric) AND (bonus < 'Infinity'::numeric)),
    reference text DEFAULT ''::text NOT NULL,
    note text DEFAULT ''::text NOT NULL,
    occurred_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    voided_at timestamp with time zone,
    revision bigint DEFAULT 1 NOT NULL
);
CREATE INDEX IF NOT EXISTS kitty_supplier_purchase_wallet ON kitty_supplier_purchases USING btree (wallet_id, created_at);

CREATE TABLE IF NOT EXISTS kitty_supplier_remote_operations (
    operation_id text PRIMARY KEY,
    wallet_id bigint NOT NULL REFERENCES kitty_supplier_wallets(id),
    key_name text NOT NULL,
    group_identity text NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL CHECK (state = ANY (ARRAY['pending'::text, 'succeeded'::text, 'unconfirmed'::text])),
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    completed_at timestamp with time zone
);

CREATE TABLE IF NOT EXISTS kitty_procurement_receipts (
    api_key_id bigint NOT NULL,
    request_key text NOT NULL,
    source_log_id bigint NOT NULL,
    account_id bigint NOT NULL,
    user_id bigint NOT NULL,
    group_id bigint,
    model text NOT NULL,
    upstream_model text NOT NULL,
    upstream_request_id text,
    supplier_id bigint,
    supplier_name text,
    wallet_id bigint,
    wallet_name text,
    rule_id bigint,
    rule_snapshot jsonb DEFAULT '{}'::jsonb NOT NULL,
    credit_cost numeric,
    estimated_cost numeric,
    cost_currency text,
    request_valuation numeric,
    measurements jsonb NOT NULL,
    cost_status text DEFAULT 'unknown'::text NOT NULL,
    observed_at timestamp with time zone NOT NULL,
    captured_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT kitty_procurement_receipts_pkey PRIMARY KEY (api_key_id, request_key)
);
CREATE INDEX IF NOT EXISTS kitty_procurement_receipt_account ON kitty_procurement_receipts USING btree (account_id, observed_at);
CREATE INDEX IF NOT EXISTS kitty_procurement_receipt_supplier ON kitty_procurement_receipts USING btree (supplier_id, observed_at);
CREATE INDEX IF NOT EXISTS kitty_procurement_receipt_time ON kitty_procurement_receipts USING btree (observed_at);

CREATE TABLE IF NOT EXISTS kitty_procurement_adjustments (
    id BIGSERIAL PRIMARY KEY,
    api_key_id bigint NOT NULL,
    request_key text NOT NULL,
    operation_id text NOT NULL UNIQUE,
    amount numeric NOT NULL CHECK ((amount >= (0)::numeric) AND (amount < 'Infinity'::numeric)),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'::text),
    reference text NOT NULL CHECK (length(reference) > 0),
    fx_to_usd numeric CHECK ((fx_to_usd > (0)::numeric) AND (fx_to_usd < 'Infinity'::numeric)),
    note text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    FOREIGN KEY (api_key_id, request_key) REFERENCES kitty_procurement_receipts(api_key_id, request_key)
);
CREATE INDEX IF NOT EXISTS kitty_procurement_adjustment_receipt ON kitty_procurement_adjustments USING btree (api_key_id, request_key, id DESC);

CREATE TABLE IF NOT EXISTS kitty_provider_operations_state (
    singleton boolean DEFAULT true NOT NULL PRIMARY KEY CHECK (singleton),
    started_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL
);
INSERT INTO kitty_provider_operations_state(singleton) VALUES(TRUE) ON CONFLICT DO NOTHING;

-- 与主站逐字一致。
CREATE OR REPLACE FUNCTION kitty_procurement_cost(rule jsonb, m jsonb, credit_rate numeric)
 RETURNS numeric
 LANGUAGE plpgsql
 IMMUTABLE
AS $function$
DECLARE amount NUMERIC; method TEXT:=rule->>'method';
 cache_write NUMERIC; cache_30m NUMERIC; cache_12h NUMERIC; cache_amount NUMERIC;
BEGIN
 IF (m->>'input')::numeric<0 OR (m->>'output')::numeric<0 OR (m->>'cache_read')::numeric<0 OR (m->>'cache_write')::numeric<0 THEN RETURN NULL; END IF;
 CASE method
  WHEN 'standard_multiplier' THEN
   IF (m->>'standard')::numeric<0 THEN RETURN NULL; END IF;
   amount:=(m->>'standard')::numeric*(rule->>'multiplier')::numeric;
  WHEN 'account_credits' THEN
   IF (m->>'account_credits')::numeric<0 THEN RETURN NULL; END IF;
   amount:=(m->>'account_credits')::numeric*credit_rate;
  WHEN 'tokens' THEN
   -- Split only complete, non-negative integral upstream measurements. Missing
   -- or inconsistent TTL details keep the historic aggregate cost unchanged.
   cache_write:=(m->>'cache_write')::numeric;
   cache_30m:=(m->>'cache_write_30m')::numeric;
   cache_12h:=(m->>'cache_write_12h')::numeric;
   cache_amount:=cache_write*(rule->>'cache_write_price')::numeric;
   IF cache_30m>=0 AND cache_12h>=0 AND cache_30m=trunc(cache_30m) AND cache_12h=trunc(cache_12h)
    AND cache_30m+cache_12h=cache_write AND (rule->>'cache_write_12h_price') IS NOT NULL THEN
    cache_amount:=cache_30m*(rule->>'cache_write_price')::numeric+cache_12h*(rule->>'cache_write_12h_price')::numeric;
   END IF;
   IF (m->>'images')::bigint>0 OR (m->>'videos')::bigint>0 THEN RETURN NULL; END IF;
   amount:=((m->>'input')::numeric*(rule->>'input_price')::numeric+
    (m->>'output')::numeric*(rule->>'output_price')::numeric+
    (m->>'cache_read')::numeric*(rule->>'cache_read_price')::numeric+
    cache_amount)/1000000;
  WHEN 'image' THEN
   IF (m->>'images')::bigint<=0 THEN RETURN NULL; END IF;
   amount:=(m->>'images')::numeric*(rule->>'unit_price')::numeric;
  WHEN 'video_second' THEN
   IF (m->>'videos')::bigint<=0 OR COALESCE((m->>'seconds')::numeric,0)<=0 THEN RETURN NULL; END IF;
   amount:=(m->>'seconds')::numeric*(m->>'videos')::numeric*(rule->>'unit_price')::numeric;
  WHEN 'video_item' THEN
   IF (m->>'videos')::bigint<=0 THEN RETURN NULL; END IF;
   amount:=(m->>'videos')::numeric*(rule->>'unit_price')::numeric;
  WHEN 'request' THEN amount:=(rule->>'unit_price')::numeric;
  ELSE RETURN NULL;
 END CASE;
 IF amount>=0 AND amount<'Infinity'::numeric THEN RETURN round(amount,12); END IF;
 RETURN NULL;
END;
$function$;

-- 与主站逐字一致：采购记录变动时生成一版不可变的加权参考单价。
CREATE OR REPLACE FUNCTION kitty_supplier_credit_rate_trigger()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
DECLARE cash_total NUMERIC;credit_total NUMERIC;
BEGIN
 PERFORM id FROM kitty_supplier_wallets WHERE id=NEW.wallet_id FOR UPDATE;
 SELECT COALESCE(sum((CASE WHEN kind='refund' THEN -1 ELSE 1 END)*cash*fx_rate),0),
  COALESCE(sum((CASE WHEN kind='refund' THEN -1 ELSE 1 END)*(credits+bonus)),0)
 INTO cash_total,credit_total FROM kitty_supplier_purchases WHERE wallet_id=NEW.wallet_id AND voided_at IS NULL;
 INSERT INTO kitty_supplier_credit_rates(wallet_id,cash_basis,credit_basis,rate)
 VALUES(NEW.wallet_id,cash_total,credit_total,CASE WHEN credit_total>0 AND cash_total>=0 THEN cash_total/credit_total END);
 RETURN NEW;
END;
$function$;

DROP TRIGGER IF EXISTS kitty_supplier_credit_rate_write ON kitty_supplier_purchases;
CREATE TRIGGER kitty_supplier_credit_rate_write AFTER INSERT OR UPDATE OF voided_at ON kitty_supplier_purchases FOR EACH ROW EXECUTE FUNCTION kitty_supplier_credit_rate_trigger();
