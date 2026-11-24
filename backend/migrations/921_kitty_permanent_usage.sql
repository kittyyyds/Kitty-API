-- Kitty 第 6 阶段：永久累计用量 + 采购收据触发器（建在 usage_logs 上）。
-- 表名、字段名、约束名与主站 354_kitty_business_and_media.sql 一致。
--
-- 与主站的口径差异（只因 CS 的 usage_logs 少列，不给 usage_logs 加列）：
--   * CS 没有 cache_creation_30m_tokens / cache_creation_12h_tokens（OpenAI 缓存档位）。
--     kitty_permanent_usage 的这两列照建（迁主站数据用），CS 新写入的行一律记 0；
--     缓存写入总量仍在 cache_creation_tokens 里，不丢。
--   * 收据 measurements 里 cache_write_30m / cache_write_12h 记 0，Claude 的 5m/1h 另记在
--     cache_write_5m / cache_write_1h（只作展示，不参与成本公式）。成本公式与主站一致，
--     30m+12h 不等于总写入时按普通缓存写入价整体计价，所以不会把 Claude 1h 当成 12h 计价。
--   * 触发器监听的列里，用 cache_creation_5m_tokens / cache_creation_1h_tokens 代替主站的
--     30m / 12h 两列。
--
-- 迁移主站历史数据时（第 7 阶段）：导入 usage_logs 之前先停用本文件的三个触发器
-- （ALTER TABLE usage_logs DISABLE TRIGGER kitty_permanent_usage_write,
--  DISABLE TRIGGER kitty_permanent_usage_delete, DISABLE TRIGGER kitty_procurement_write;
--  或在导入会话里 SET session_replication_role = replica），
-- 再原样导入主站的 kitty_permanent_usage、kitty_permanent_usage_state（baseline_complete=true）、
-- kitty_procurement_receipts / adjustments，最后 ENABLE TRIGGER。否则触发器会按 CS 当前规则
-- 重算收据、把主站的 30m/12h 改成 0，回填任务也会再扫一遍历史行。

SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS kitty_permanent_usage (
    source_log_id bigint NOT NULL,
    api_key_id bigint NOT NULL,
    request_key text NOT NULL,
    user_id bigint NOT NULL,
    input_tokens bigint NOT NULL,
    output_tokens bigint NOT NULL,
    cache_creation_tokens bigint NOT NULL,
    cache_read_tokens bigint NOT NULL,
    actual_cost numeric,
    total_cost numeric,
    invalid_metrics boolean DEFAULT false NOT NULL,
    billing_type smallint NOT NULL,
    observed_at timestamp with time zone NOT NULL,
    captured_at timestamp with time zone DEFAULT now() NOT NULL,
    cache_creation_30m_tokens bigint DEFAULT 0 NOT NULL,
    cache_creation_12h_tokens bigint DEFAULT 0 NOT NULL,
    CONSTRAINT kitty_permanent_usage_pkey PRIMARY KEY (api_key_id, request_key)
);
CREATE INDEX IF NOT EXISTS kitty_permanent_usage_user ON kitty_permanent_usage USING btree (user_id);

CREATE TABLE IF NOT EXISTS kitty_permanent_usage_state (
    singleton boolean DEFAULT true NOT NULL,
    started_at timestamp with time zone DEFAULT now() NOT NULL,
    baseline_highwater bigint NOT NULL,
    baseline_cursor bigint DEFAULT 0 NOT NULL,
    baseline_complete boolean DEFAULT false NOT NULL,
    baseline_completed_at timestamp with time zone,
    CONSTRAINT kitty_permanent_usage_state_singleton_check CHECK (singleton),
    CONSTRAINT kitty_permanent_usage_state_pkey PRIMARY KEY (singleton)
);

CREATE OR REPLACE FUNCTION kitty_capture_permanent_usage(u usage_logs, overwrite_metrics boolean)
 RETURNS void
 LANGUAGE plpgsql
AS $function$
DECLARE k TEXT; invalid BOOLEAN; ac NUMERIC; tc NUMERIC;
BEGIN
 k:=CASE WHEN COALESCE(u.request_id,'')<>'' THEN 'request:'||u.request_id ELSE 'legacy-row:'||u.id::text END;
 ac:=CASE WHEN u.actual_cost>'-Infinity'::numeric AND u.actual_cost<'Infinity'::numeric THEN u.actual_cost END;
 tc:=CASE WHEN u.total_cost>'-Infinity'::numeric AND u.total_cost<'Infinity'::numeric THEN u.total_cost END;
 -- CS: no 30m/12h columns on usage_logs; they are stored as 0.
 invalid:=(ac IS NULL OR tc IS NULL OR u.input_tokens<0 OR u.output_tokens<0 OR u.cache_creation_tokens<0 OR u.cache_read_tokens<0);
 INSERT INTO kitty_permanent_usage(source_log_id,api_key_id,request_key,user_id,input_tokens,output_tokens,cache_creation_tokens,cache_read_tokens,cache_creation_30m_tokens,cache_creation_12h_tokens,actual_cost,total_cost,invalid_metrics,billing_type,observed_at)
 VALUES(u.id,u.api_key_id,k,u.user_id,GREATEST(u.input_tokens,0),GREATEST(u.output_tokens,0),GREATEST(u.cache_creation_tokens,0),GREATEST(u.cache_read_tokens,0),0,0,ac,tc,invalid,u.billing_type,u.created_at)
 ON CONFLICT DO NOTHING;
 -- A replay with a new log ID cannot overwrite a previously corrected receipt.
 -- Baseline backfill never replaces a live, newer trigger snapshot.
 IF overwrite_metrics THEN
  UPDATE kitty_permanent_usage SET input_tokens=GREATEST(u.input_tokens,0),output_tokens=GREATEST(u.output_tokens,0),
   cache_creation_tokens=GREATEST(u.cache_creation_tokens,0),cache_read_tokens=GREATEST(u.cache_read_tokens,0),
   actual_cost=ac,total_cost=tc,invalid_metrics=invalid,billing_type=u.billing_type,observed_at=u.created_at
  WHERE source_log_id=u.id AND api_key_id=u.api_key_id AND request_key=k AND user_id=u.user_id
  AND (input_tokens,output_tokens,cache_creation_tokens,cache_read_tokens,actual_cost,total_cost,invalid_metrics,billing_type,observed_at)
   IS DISTINCT FROM (GREATEST(u.input_tokens,0)::bigint,GREATEST(u.output_tokens,0)::bigint,GREATEST(u.cache_creation_tokens,0)::bigint,GREATEST(u.cache_read_tokens,0)::bigint,ac,tc,invalid,u.billing_type::smallint,u.created_at);
 END IF;
END;
$function$;

CREATE OR REPLACE FUNCTION kitty_backfill_permanent_usage(batch_size integer)
 RETURNS bigint
 LANGUAGE plpgsql
AS $function$
DECLARE s kitty_permanent_usage_state%ROWTYPE; u usage_logs%ROWTYPE; processed BIGINT:=0; last_id BIGINT;
BEGIN
 IF batch_size<1 OR batch_size>1000 THEN RAISE EXCEPTION 'invalid permanent usage batch size'; END IF;
 SELECT * INTO s FROM kitty_permanent_usage_state WHERE singleton FOR UPDATE;
 IF NOT FOUND OR s.baseline_complete THEN RETURN 0; END IF;
 last_id:=s.baseline_cursor;
 FOR u IN SELECT * FROM usage_logs WHERE id>s.baseline_cursor AND id<=s.baseline_highwater ORDER BY id LIMIT batch_size LOOP
  PERFORM kitty_capture_permanent_usage(u,FALSE);
  last_id:=u.id;processed:=processed+1;
 END LOOP;
 UPDATE kitty_permanent_usage_state SET baseline_cursor=CASE WHEN processed<batch_size THEN s.baseline_highwater ELSE last_id END,
  baseline_complete=(processed<batch_size OR last_id>=s.baseline_highwater),
  baseline_completed_at=CASE WHEN processed<batch_size OR last_id>=s.baseline_highwater THEN NOW() END WHERE singleton;
 RETURN processed;
END;
$function$;

CREATE OR REPLACE FUNCTION kitty_capture_procurement(u usage_logs)
 RETURNS void
 LANGUAGE plpgsql
AS $function$
DECLARE k TEXT; existing kitty_procurement_receipts%ROWTYPE; r kitty_supplier_cost_rules%ROWTYPE;
 w kitty_supplier_wallets%ROWTYPE; s kitty_suppliers%ROWTYPE;
 binding_id BIGINT; credit_rate_id BIGINT; rule_json JSONB:='{}'::jsonb; m JSONB; cr NUMERIC; ec NUMERIC; val NUMERIC;
BEGIN
 k:=CASE WHEN COALESCE(u.request_id,'')<>'' THEN 'request:'||u.request_id ELSE 'legacy-row:'||u.id::text END;
 -- CS mapping: OpenAI 30m/12h slots are 0 (no such columns); Claude 5m/1h are recorded
 -- under their own keys for display only and never enter the 12h price slot.
 m:=jsonb_build_object('input',u.input_tokens,'output',u.output_tokens,'cache_read',u.cache_read_tokens,'cache_write',u.cache_creation_tokens,
  'cache_write_30m',0,'cache_write_12h',0,
  'cache_write_5m',u.cache_creation_5m_tokens,'cache_write_1h',u.cache_creation_1h_tokens,
  'images',u.image_count,'videos',u.video_count,'seconds',u.video_duration_seconds,'standard',u.total_cost,
  'account_credits',COALESCE(u.account_stats_cost,u.total_cost)*COALESCE(u.account_rate_multiplier,1));
 val:=CASE WHEN u.actual_cost>=0 AND u.actual_cost<'Infinity'::numeric THEN u.actual_cost END;
 SELECT * INTO existing FROM kitty_procurement_receipts WHERE api_key_id=u.api_key_id AND request_key=k;
 IF FOUND THEN
  IF existing.source_log_id<>u.id OR existing.account_id<>u.account_id OR existing.user_id<>u.user_id THEN RETURN; END IF;
  -- Receipt corrections reuse the frozen procurement rule, never today's config.
  ec:=kitty_procurement_cost(existing.rule_snapshot,m,existing.credit_cost);
  UPDATE kitty_procurement_receipts SET measurements=m,request_valuation=val,estimated_cost=ec,
   cost_status=CASE WHEN ec IS NULL THEN 'unknown' ELSE 'estimated' END,
   upstream_request_id=COALESCE(u.upstream_request_id,upstream_request_id)
  WHERE api_key_id=u.api_key_id AND request_key=k;
  RETURN;
 END IF;
 SELECT wallet_id INTO binding_id FROM kitty_supplier_bindings WHERE account_id=u.account_id
  AND started_at<=u.created_at AND (ended_at IS NULL OR ended_at>u.created_at) ORDER BY started_at DESC,id DESC LIMIT 1;
 IF FOUND THEN
  SELECT * INTO w FROM kitty_supplier_wallets WHERE id=binding_id;
  SELECT * INTO s FROM kitty_suppliers WHERE id=w.supplier_id;
  SELECT * INTO r FROM kitty_supplier_cost_rules WHERE wallet_id=w.id AND model IN ('*',COALESCE(NULLIF(u.upstream_model,''),NULLIF(u.requested_model,''),u.model))
   AND started_at<=u.created_at AND (ended_at IS NULL OR ended_at>u.created_at)
   ORDER BY (model<>'*') DESC,started_at DESC,id DESC LIMIT 1;
  IF FOUND THEN
   rule_json:=to_jsonb(r)-'ended_at'-'note';
   IF r.method='account_credits' THEN
    SELECT id,rate INTO credit_rate_id,cr FROM kitty_supplier_credit_rates
     WHERE wallet_id=w.id AND recorded_at<=u.created_at ORDER BY recorded_at DESC,id DESC LIMIT 1;
    rule_json:=rule_json||jsonb_build_object('credit_rate_version',credit_rate_id);
   END IF;
   ec:=kitty_procurement_cost(rule_json,m,cr);
  END IF;
 END IF;
 INSERT INTO kitty_procurement_receipts(api_key_id,request_key,source_log_id,account_id,user_id,group_id,model,upstream_model,upstream_request_id,
  supplier_id,supplier_name,wallet_id,wallet_name,rule_id,rule_snapshot,credit_cost,estimated_cost,cost_currency,request_valuation,measurements,cost_status,observed_at)
 VALUES(u.api_key_id,k,u.id,u.account_id,u.user_id,u.group_id,COALESCE(NULLIF(u.requested_model,''),u.model),COALESCE(NULLIF(u.upstream_model,''),NULLIF(u.requested_model,''),u.model),u.upstream_request_id,
  s.id,s.name,w.id,w.name,r.id,rule_json,cr,ec,r.currency,val,m,CASE WHEN ec IS NULL THEN 'unknown' ELSE 'estimated' END,u.created_at)
 ON CONFLICT DO NOTHING;
END;
$function$;

CREATE OR REPLACE FUNCTION kitty_permanent_usage_capture_trigger()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
BEGIN
 IF TG_OP='DELETE' THEN
  -- Preserve rows deleted during the bounded historical backfill. This also
  -- covers FK cascades; no subtraction for retention or key/user deletion.
  PERFORM kitty_capture_permanent_usage(OLD,FALSE);
  RETURN OLD;
 END IF;
 PERFORM kitty_capture_permanent_usage(NEW,TRUE);
 RETURN NEW;
END;
$function$;

CREATE OR REPLACE FUNCTION kitty_procurement_capture_trigger()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
BEGIN
 PERFORM kitty_capture_procurement(NEW);
 RETURN NEW;
END;
$function$;

-- Existing CS rows (id <= highwater) are captured by the backfill worker. Creating the
-- triggers waits for in-flight usage_logs inserts, so no row falls between the two.
INSERT INTO kitty_permanent_usage_state(singleton,baseline_highwater) SELECT TRUE,COALESCE(max(id),0) FROM usage_logs ON CONFLICT DO NOTHING;

DROP TRIGGER IF EXISTS kitty_permanent_usage_delete ON usage_logs;
CREATE TRIGGER kitty_permanent_usage_delete BEFORE DELETE ON usage_logs FOR EACH ROW EXECUTE FUNCTION kitty_permanent_usage_capture_trigger();

DROP TRIGGER IF EXISTS kitty_permanent_usage_write ON usage_logs;
CREATE TRIGGER kitty_permanent_usage_write AFTER INSERT OR UPDATE OF input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, cache_creation_5m_tokens, cache_creation_1h_tokens, actual_cost, total_cost, billing_type, created_at ON usage_logs FOR EACH ROW EXECUTE FUNCTION kitty_permanent_usage_capture_trigger();

DROP TRIGGER IF EXISTS kitty_procurement_write ON usage_logs;
CREATE TRIGGER kitty_procurement_write AFTER INSERT OR UPDATE OF input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, cache_creation_5m_tokens, cache_creation_1h_tokens, total_cost, actual_cost, account_stats_cost, account_rate_multiplier, image_count, video_count, video_duration_seconds, upstream_request_id ON usage_logs FOR EACH ROW EXECUTE FUNCTION kitty_procurement_capture_trigger();
