-- Kitty 定时测试与质量运维（/admin/tests）：组合计划、题库/能力库、组合规则、质量预算、探测、
-- 结果处置、保护规则、通过率证据、自动复核。
-- 表名、字段名、类型、默认值、约束、函数、触发器与主站最终结构一致（主站 247–360 号迁移累加的结果），
-- 只有一处差异：reconcile_active_test_model_protection 不再改 accounts.status 为 quality_paused，
-- 暂停只写进 accounts.extra 的投影键，由调度入口按 kitty_quality_protection_enabled 总开关读取。
-- 905 号已建 plans/results 新字段与 definitions、plan_definitions、protection_states、votes 四张表。
--
-- 迁完主站数据需要：把 accounts.status='quality_paused' 改回 'active'（extra 里已有投影）；
-- 把 scheduled_test_*_id_seq、quality_probe_attempts_id_seq 推到 MAX(id)。

SET LOCAL lock_timeout = '2s';

-- 底座旧计划（按单账号、272 号已全部停用）不符合主站的分组计划约束：
-- 先把账号写回旧结果行，再把旧计划改成"停用、无分组"的历史行，然后补主站的 target_mode 约束。
UPDATE scheduled_test_results r SET account_id = p.account_id
FROM scheduled_test_plans p
WHERE r.plan_id = p.id AND r.account_id IS NULL AND p.account_id IS NOT NULL AND cardinality(p.group_ids) = 0;
UPDATE scheduled_test_plans
SET enabled = false, account_id = NULL, group_id = NULL, target_mode = 'all_accounts',
    next_run_at = NULL,
    migration_note = CASE WHEN migration_note = '' THEN '底座旧单账号定时测试，已停用，仅保留历史。' ELSE migration_note END,
    updated_at = NOW()
WHERE cardinality(group_ids) = 0 AND (account_id IS NOT NULL OR group_id IS NOT NULL OR enabled OR target_mode <> 'all_accounts');
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'scheduled_test_plans'::regclass
                   AND conname = 'scheduled_test_plans_target_mode_check') THEN
        ALTER TABLE scheduled_test_plans ADD CONSTRAINT scheduled_test_plans_target_mode_check CHECK (
            target_mode = 'all_accounts' AND account_id IS NULL AND (
                (cardinality(group_ids) > 0 AND group_id IS NOT NULL AND group_id = group_ids[1])
                OR (NOT enabled AND cardinality(group_ids) = 0 AND group_id IS NULL)));
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS idx_st_protection_account ON scheduled_test_protection_states (account_id);

-- ===== 新表 =====

CREATE TABLE IF NOT EXISTS quality_operations_config (
    id integer NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    config jsonb NOT NULL,
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT quality_operations_config_pkey PRIMARY KEY (id),
    CONSTRAINT quality_operations_config_id_check CHECK ((id = 1))
);

CREATE TABLE IF NOT EXISTS quality_budget_days (
    day date NOT NULL,
    account_id bigint NOT NULL,
    requests bigint NOT NULL DEFAULT 0,
    tokens bigint NOT NULL DEFAULT 0,
    unknown_requests bigint NOT NULL DEFAULT 0,
    recovery_requests bigint NOT NULL DEFAULT 0,
    recovery_tokens bigint NOT NULL DEFAULT 0,
    CONSTRAINT quality_budget_days_pkey PRIMARY KEY (day, account_id)
);

CREATE TABLE IF NOT EXISTS quality_probe_attempts (
    id BIGSERIAL NOT NULL,
    account_id bigint NOT NULL,
    day date NOT NULL,
    supplier_id bigint,
    reserved_tokens bigint NOT NULL,
    actual_tokens bigint,
    settled boolean NOT NULL DEFAULT false,
    kind text NOT NULL DEFAULT 'unknown'::text,
    started_at timestamp with time zone NOT NULL DEFAULT now(),
    purpose text NOT NULL DEFAULT 'regular'::text,
    CONSTRAINT quality_probe_attempts_pkey PRIMARY KEY (id),
    CONSTRAINT quality_probe_attempts_purpose_check CHECK ((purpose = ANY (ARRAY['regular'::text, 'recovery'::text])))
);
CREATE INDEX IF NOT EXISTS idx_quality_probe_retention ON quality_probe_attempts USING btree (started_at);

CREATE TABLE IF NOT EXISTS quality_healthy_frequency (
    plan_id bigint NOT NULL,
    account_id bigint NOT NULL,
    policy_key text NOT NULL,
    config_version bigint NOT NULL,
    streak integer NOT NULL DEFAULT 0,
    next_at timestamp with time zone NOT NULL,
    observed_at timestamp with time zone NOT NULL,
    CONSTRAINT quality_healthy_frequency_pkey PRIMARY KEY (plan_id, account_id),
    CONSTRAINT quality_healthy_frequency_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT quality_healthy_frequency_plan_id_fkey FOREIGN KEY (plan_id) REFERENCES scheduled_test_plans(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS quality_management_config_archive (
    kind text NOT NULL,
    source_id bigint NOT NULL,
    config jsonb NOT NULL,
    archived_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT quality_management_config_archive_pkey PRIMARY KEY (kind, source_id)
);

CREATE TABLE IF NOT EXISTS scheduled_test_managed_accounts (
    plan_id bigint NOT NULL,
    account_id bigint NOT NULL,
    source_group_id bigint NOT NULL,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT scheduled_test_managed_accounts_pkey PRIMARY KEY (plan_id, account_id),
    CONSTRAINT scheduled_test_managed_accounts_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT scheduled_test_managed_accounts_plan_id_fkey FOREIGN KEY (plan_id) REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
    CONSTRAINT scheduled_test_managed_accounts_source_group_id_fkey FOREIGN KEY (source_group_id) REFERENCES groups(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS scheduled_test_combination_states (
    plan_id bigint NOT NULL,
    account_id bigint NOT NULL,
    blocked boolean NOT NULL DEFAULT false,
    reason text NOT NULL DEFAULT ''::text,
    rule_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    trigger_event_id bigint,
    model_key text NOT NULL DEFAULT ''::text,
    account_wide boolean NOT NULL DEFAULT false,
    CONSTRAINT scheduled_test_combination_states_pkey PRIMARY KEY (plan_id, account_id, model_key),
    CONSTRAINT scheduled_test_combination_states_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT scheduled_test_combination_states_plan_id_fkey FOREIGN KEY (plan_id) REFERENCES scheduled_test_plans(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_st_combination_account_blocked ON scheduled_test_combination_states USING btree (account_id) WHERE blocked;

CREATE TABLE IF NOT EXISTS scheduled_test_account_backoffs (
    plan_id bigint NOT NULL,
    account_id bigint NOT NULL,
    policy_key text NOT NULL,
    consecutive_failures integer NOT NULL DEFAULT 0,
    next_check_at timestamp with time zone NOT NULL,
    observed_at timestamp with time zone NOT NULL,
    CONSTRAINT scheduled_test_account_backoffs_pkey PRIMARY KEY (plan_id, account_id),
    CONSTRAINT scheduled_test_account_backoffs_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT scheduled_test_account_backoffs_plan_id_fkey FOREIGN KEY (plan_id) REFERENCES scheduled_test_plans(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS scheduled_test_pass_rate_evidence (
    id BIGSERIAL NOT NULL,
    result_id bigint NOT NULL,
    plan_id bigint NOT NULL,
    account_id bigint NOT NULL,
    test_definition_id bigint NOT NULL,
    model_key text NOT NULL,
    rule_version text NOT NULL,
    run_key text NOT NULL,
    verdict text NOT NULL,
    started_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT scheduled_test_pass_rate_evidence_pkey PRIMARY KEY (id),
    CONSTRAINT scheduled_test_pass_rate_evid_plan_id_account_id_test_defin_key UNIQUE (plan_id, account_id, test_definition_id, model_key, rule_version, run_key),
    CONSTRAINT scheduled_test_pass_rate_evidence_result_id_key UNIQUE (result_id),
    CONSTRAINT scheduled_test_pass_rate_evidence_verdict_check CHECK ((verdict = ANY (ARRAY['pass'::text, 'fail'::text]))),
    CONSTRAINT scheduled_test_pass_rate_evidence_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT scheduled_test_pass_rate_evidence_plan_id_fkey FOREIGN KEY (plan_id) REFERENCES scheduled_test_plans(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS scheduled_test_pass_rate_window ON scheduled_test_pass_rate_evidence USING btree (plan_id, account_id, test_definition_id, model_key, rule_version, started_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS scheduled_test_pass_rate_states (
    plan_id bigint NOT NULL,
    account_id bigint NOT NULL,
    test_definition_id bigint NOT NULL,
    model_key text NOT NULL,
    rule_version text NOT NULL,
    blocked boolean NOT NULL DEFAULT false,
    summary jsonb NOT NULL,
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT scheduled_test_pass_rate_states_pkey PRIMARY KEY (plan_id, account_id, test_definition_id, model_key),
    CONSTRAINT scheduled_test_pass_rate_states_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT scheduled_test_pass_rate_states_plan_id_fkey FOREIGN KEY (plan_id) REFERENCES scheduled_test_plans(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS scheduled_test_pass_rate_holds ON scheduled_test_pass_rate_states USING btree (account_id, model_key) WHERE blocked;

CREATE TABLE IF NOT EXISTS scheduled_test_all_pass_rounds (
    id BIGSERIAL NOT NULL,
    plan_id bigint NOT NULL,
    account_id bigint NOT NULL,
    model_key text NOT NULL,
    run_id text NOT NULL,
    started_at timestamp with time zone NOT NULL,
    rule_version text NOT NULL,
    failed_definition_ids bigint[] NOT NULL DEFAULT '{}'::bigint[],
    verdict text NOT NULL DEFAULT 'pending'::text,
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT scheduled_test_all_pass_rounds_pkey PRIMARY KEY (id),
    CONSTRAINT scheduled_test_all_pass_round_plan_id_account_id_model_key__key UNIQUE (plan_id, account_id, model_key, run_id),
    CONSTRAINT scheduled_test_all_pass_rounds_run_id_check CHECK ((run_id <> ''::text)),
    CONSTRAINT scheduled_test_all_pass_rounds_verdict_check CHECK ((verdict = ANY (ARRAY['pending'::text, 'pass'::text, 'fail'::text]))),
    CONSTRAINT scheduled_test_all_pass_rounds_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT scheduled_test_all_pass_rounds_plan_id_fkey FOREIGN KEY (plan_id) REFERENCES scheduled_test_plans(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_st_all_pass_round_history ON scheduled_test_all_pass_rounds USING btree (plan_id, account_id, model_key, started_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS scheduled_test_execution_leases (
    plan_id bigint NOT NULL,
    scope text NOT NULL,
    owner text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    CONSTRAINT scheduled_test_execution_leases_pkey PRIMARY KEY (plan_id, scope),
    CONSTRAINT scheduled_test_execution_leases_plan_id_fkey FOREIGN KEY (plan_id) REFERENCES scheduled_test_plans(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_scheduled_test_leases_expiry ON scheduled_test_execution_leases USING btree (expires_at);

CREATE TABLE IF NOT EXISTS scheduled_test_auto_review_days (
    day date NOT NULL,
    requests bigint NOT NULL DEFAULT 0,
    CONSTRAINT scheduled_test_auto_review_days_pkey PRIMARY KEY (day),
    CONSTRAINT scheduled_test_auto_review_days_requests_check CHECK ((requests >= 0))
);

CREATE TABLE IF NOT EXISTS scheduled_test_protection_events (
    id BIGSERIAL NOT NULL,
    plan_id bigint NOT NULL,
    account_id bigint NOT NULL,
    test_definition_id bigint NOT NULL DEFAULT 0,
    result_id bigint NOT NULL,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    snapshot jsonb NOT NULL,
    CONSTRAINT scheduled_test_protection_events_pkey PRIMARY KEY (id)
);
CREATE INDEX IF NOT EXISTS idx_scheduled_test_protection_events_account ON scheduled_test_protection_events USING btree (account_id, created_at DESC, id DESC);

-- ===== 函数 =====

CREATE OR REPLACE FUNCTION reconcile_active_test_model_protection(affected_account bigint)
 RETURNS void
 LANGUAGE plpgsql
AS $function$
DECLARE
 projected_holds JSONB;
 projected_states JSONB;
 all_models_bad BOOLEAN;
 account_wide_held BOOLEAN;
 protection_reason TEXT;
 desired_extra JSONB;
 paused BOOLEAN;
 changed_count BIGINT;
BEGIN
 PERFORM id FROM accounts WHERE id=affected_account FOR NO KEY UPDATE;
 IF NOT FOUND THEN RETURN; END IF;
WITH all_holds AS (
 SELECT s.model_key,s.reason,s.updated_at,
        (s.model_key='') AS account_wide
 FROM scheduled_test_protection_states s
 JOIN scheduled_test_definitions d ON d.id=s.test_definition_id
 WHERE s.account_id=affected_account AND s.blocked AND d.output_kind<>'statistics'
 UNION ALL
 SELECT model_key,reason,updated_at,(account_wide OR model_key='')
 FROM scheduled_test_combination_states WHERE account_id=affected_account AND blocked
 UNION ALL
 SELECT model_key,COALESCE(summary->>'reason','智力检测最近通过率低于要求'),updated_at,(model_key='')
 FROM scheduled_test_pass_rate_states WHERE account_id=affected_account AND blocked
), model_holds AS (
 SELECT DISTINCT ON(model_key) model_key,reason FROM all_holds
 WHERE NOT account_wide ORDER BY model_key,updated_at DESC
), monitored_plan_models AS (
 SELECT p.id AS plan_id,p.latest_run_id,p.protection,selected.model_key
 FROM scheduled_test_plans p
 CROSS JOIN LATERAL unnest(CASE WHEN cardinality(p.model_ids)>0 THEN p.model_ids ELSE ARRAY[p.model_id] END) selected(model_key)
 WHERE p.protection->>'enabled'='true'
 AND (p.enabled OR EXISTS(SELECT 1 FROM scheduled_test_pass_rate_states kept WHERE kept.plan_id=p.id AND kept.account_id=affected_account AND kept.blocked))
 AND NOT (affected_account=ANY(COALESCE(p.excluded_account_ids,'{}'::bigint[])))
 AND EXISTS(SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p.protection->'rules')='array' THEN p.protection->'rules' ELSE '[]'::jsonb END) rule
   JOIN scheduled_test_definitions d ON d.id=(rule->>'test_definition_id')::bigint
   WHERE d.output_kind<>'statistics')
 AND (EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=affected_account AND ag.group_id=ANY(p.group_ids))
   OR EXISTS(SELECT 1 FROM scheduled_test_protection_states s WHERE s.plan_id=p.id AND s.account_id=affected_account)
   OR EXISTS(SELECT 1 FROM scheduled_test_combination_states s WHERE s.plan_id=p.id AND s.account_id=affected_account)
   OR EXISTS(SELECT 1 FROM scheduled_test_pass_rate_states s WHERE s.plan_id=p.id AND s.account_id=affected_account))
), monitored_models AS (
 SELECT DISTINCT model_key FROM monitored_plan_models
), model_states AS (
 SELECT m.model_key,jsonb_strip_nulls(jsonb_build_object('status',
 CASE WHEN EXISTS(SELECT 1 FROM all_holds h WHERE h.account_wide OR h.model_key=m.model_key) THEN 'blocked'
 WHEN NOT EXISTS(
  SELECT 1 FROM monitored_plan_models p
  WHERE p.model_key=m.model_key AND CASE WHEN p.protection->>'mode'='all_pass' THEN NOT EXISTS(
   SELECT 1 FROM scheduled_test_all_pass_rounds h WHERE h.plan_id=p.plan_id AND h.account_id=affected_account
   AND h.model_key=m.model_key AND h.run_id=p.latest_run_id AND h.verdict='pass'
  ) ELSE EXISTS(
   SELECT 1 FROM jsonb_array_elements(p.protection->'rules') rule
   JOIN scheduled_test_definitions d ON d.id=(rule->>'test_definition_id')::bigint AND d.output_kind<>'statistics'
   WHERE NOT EXISTS(
    SELECT 1 FROM scheduled_test_protection_states s JOIN scheduled_test_results r ON r.id=s.result_id
    WHERE s.plan_id=p.plan_id AND s.account_id=affected_account AND s.model_key=m.model_key AND s.test_definition_id=d.id
    AND s.completed AND s.verdict='pass' AND s.result_started_at>=s.round_started_at
    AND r.started_at=s.result_started_at AND r.run_id<>'' AND r.run_id=p.latest_run_id AND NOT r.protection_revoked
   )
  ) END
 ) THEN 'normal' ELSE 'pending' END,
 'reason',(SELECT h.reason FROM all_holds h WHERE h.account_wide OR h.model_key=m.model_key ORDER BY h.updated_at DESC LIMIT 1))) AS state
 FROM (SELECT model_key FROM monitored_models UNION SELECT model_key FROM model_holds) m
)
SELECT COALESCE((SELECT jsonb_object_agg(model_key,reason) FROM model_holds),'{}'::jsonb),
 EXISTS(SELECT 1 FROM monitored_models) AND NOT EXISTS(
   SELECT 1 FROM monitored_models m WHERE NOT EXISTS(SELECT 1 FROM model_holds h WHERE h.model_key=m.model_key OR h.model_key='')),
 EXISTS(SELECT 1 FROM all_holds WHERE account_wide),
 COALESCE((SELECT reason FROM all_holds WHERE account_wide ORDER BY updated_at DESC LIMIT 1),
          (SELECT reason FROM model_holds ORDER BY model_key LIMIT 1),'渠道质量检测保护'),
 COALESCE((SELECT jsonb_object_agg(model_key,state) FROM model_states),'{}'::jsonb)
 INTO projected_holds,all_models_bad,account_wide_held,protection_reason,projected_states;
 paused := all_models_bad OR account_wide_held;
 SELECT COALESCE(extra,'{}'::jsonb)-'active_test_model_states'-'active_test_model_holds'-'active_test_all_models_paused'-'quality_protection_reason'-'quality_protection_trial'
 INTO desired_extra FROM accounts WHERE id=affected_account;
 IF projected_holds<>'{}'::jsonb THEN
  desired_extra := jsonb_set(desired_extra,'{active_test_model_holds}',projected_holds);
 END IF;
 IF projected_states<>'{}'::jsonb THEN
  desired_extra := jsonb_set(desired_extra,'{active_test_model_states}',projected_states);
 END IF;
 IF all_models_bad THEN
  desired_extra := jsonb_set(desired_extra,'{active_test_all_models_paused}','true'::jsonb);
 END IF;
 IF paused THEN
  desired_extra := jsonb_set(desired_extra,'{quality_protection_reason}',to_jsonb(protection_reason));
 END IF;
 -- CS：不改 accounts.status，整号暂停只投影到 extra（quality_protection_reason /
 -- active_test_all_models_paused），由调度入口读取；旧数据里的 quality_paused 在这里顺手恢复为 active。
 UPDATE accounts SET
 status=CASE WHEN status='quality_paused' THEN 'active' ELSE status END,
 extra=desired_extra,updated_at=clock_timestamp()
 WHERE id=affected_account AND deleted_at IS NULL
 AND (extra IS DISTINCT FROM desired_extra OR status='quality_paused');
 GET DIAGNOSTICS changed_count = ROW_COUNT;
 IF changed_count>0 THEN
  INSERT INTO scheduler_outbox(event_type,account_id,dedup_key)
  VALUES('account_changed',affected_account,'scheduler_outbox:' || encode(sha256(convert_to('account_changed','UTF8') || decode('00','hex') || convert_to(affected_account::text,'UTF8') || decode('0000','hex')),'hex'))
  ON CONFLICT(dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING;
 END IF;
END;
$function$
;

CREATE OR REPLACE FUNCTION reconcile_deleted_scheduled_test_model_holds()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
DECLARE affected_account BIGINT;
BEGIN
 FOR affected_account IN SELECT DISTINCT account_id FROM deleted_model_states ORDER BY account_id LOOP
  PERFORM reconcile_active_test_model_protection(affected_account);
 END LOOP;
 RETURN NULL;
END;
$function$
;

CREATE OR REPLACE FUNCTION reconcile_deleted_scheduled_test_pass_rate_holds()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
DECLARE
 affected_account BIGINT;
 remaining_holds JSONB;
 changed_count BIGINT;
BEGIN
 FOR affected_account IN SELECT DISTINCT account_id FROM deleted_pass_rate_states ORDER BY account_id LOOP
  PERFORM id FROM accounts WHERE id=affected_account FOR NO KEY UPDATE;
  IF NOT FOUND THEN
   CONTINUE;
  END IF;
  SELECT COALESCE(jsonb_object_agg(model_key,reason),'{}'::jsonb) INTO remaining_holds FROM (
   SELECT DISTINCT ON(model_key) model_key,COALESCE(summary->>'reason','recovery_pending') AS reason
   FROM scheduled_test_pass_rate_states WHERE account_id=affected_account AND blocked
   ORDER BY model_key,updated_at DESC
  ) holds;
  UPDATE accounts SET extra=CASE WHEN remaining_holds='{}'::jsonb
    THEN COALESCE(extra,'{}'::jsonb)-'active_pass_rate_holds'
    ELSE jsonb_set(COALESCE(extra,'{}'::jsonb),'{active_pass_rate_holds}',remaining_holds) END,
   updated_at=clock_timestamp()
  WHERE id=affected_account AND COALESCE(extra->'active_pass_rate_holds','{}'::jsonb) IS DISTINCT FROM remaining_holds;
  GET DIAGNOSTICS changed_count = ROW_COUNT;
  IF changed_count > 0 THEN
   INSERT INTO scheduler_outbox(event_type,account_id,dedup_key)
  VALUES('account_changed',affected_account,'scheduler_outbox:' || encode(sha256(convert_to('account_changed','UTF8') || decode('00','hex') || convert_to(affected_account::text,'UTF8') || decode('0000','hex')),'hex'))
  ON CONFLICT(dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING;
  END IF;
 END LOOP;
 RETURN NULL;
END;
$function$
;

CREATE OR REPLACE FUNCTION reject_archived_scheduled_test_definitions()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
DECLARE
    selected_id BIGINT;
BEGIN
    FOR selected_id IN
        SELECT DISTINCT id FROM unnest(
            COALESCE(NEW.test_definition_ids, '{}'::bigint[]) ||
            CASE WHEN NEW.test_definition_id IS NULL THEN '{}'::bigint[]
                 ELSE ARRAY[NEW.test_definition_id] END
        ) AS selected(id)
        ORDER BY id
    LOOP
        PERFORM 1 FROM scheduled_test_definitions
        WHERE id = selected_id AND deleted_at IS NULL FOR KEY SHARE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'test definition % has been deleted', selected_id
                USING ERRCODE = '23503';
        END IF;
    END LOOP;
    RETURN NEW;
END;
$function$
;

CREATE OR REPLACE FUNCTION reject_quality_evidence_update()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
BEGIN
 RAISE EXCEPTION 'quality protection evidence is immutable';
END;
$function$
;

CREATE OR REPLACE FUNCTION sync_scheduled_test_plan_definitions()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
BEGIN
    DELETE FROM scheduled_test_plan_definitions
    WHERE plan_id = NEW.id AND NOT (test_definition_id = ANY(NEW.test_definition_ids));
    INSERT INTO scheduled_test_plan_definitions (plan_id, test_definition_id)
    SELECT NEW.id, definition_id
    FROM unnest(NEW.test_definition_ids) AS selected(definition_id)
    ORDER BY definition_id
    ON CONFLICT DO NOTHING;
    RETURN NEW;
END;
$function$
;

-- ===== 触发器 =====

DROP TRIGGER IF EXISTS scheduled_test_combination_deleted_model_projection ON scheduled_test_combination_states;
CREATE TRIGGER scheduled_test_combination_deleted_model_projection AFTER DELETE ON scheduled_test_combination_states REFERENCING OLD TABLE AS deleted_model_states FOR EACH STATEMENT EXECUTE FUNCTION reconcile_deleted_scheduled_test_model_holds();
DROP TRIGGER IF EXISTS scheduled_test_pass_rate_deleted_model_projection ON scheduled_test_pass_rate_states;
CREATE TRIGGER scheduled_test_pass_rate_deleted_model_projection AFTER DELETE ON scheduled_test_pass_rate_states REFERENCING OLD TABLE AS deleted_model_states FOR EACH STATEMENT EXECUTE FUNCTION reconcile_deleted_scheduled_test_model_holds();
DROP TRIGGER IF EXISTS scheduled_test_pass_rate_deleted_projection ON scheduled_test_pass_rate_states;
CREATE TRIGGER scheduled_test_pass_rate_deleted_projection AFTER DELETE ON scheduled_test_pass_rate_states REFERENCING OLD TABLE AS deleted_pass_rate_states FOR EACH STATEMENT EXECUTE FUNCTION reconcile_deleted_scheduled_test_pass_rate_holds();
DROP TRIGGER IF EXISTS trg_stp_reject_archived_definitions ON scheduled_test_plans;
CREATE TRIGGER trg_stp_reject_archived_definitions BEFORE INSERT OR UPDATE OF test_definition_id, test_definition_ids ON scheduled_test_plans FOR EACH ROW EXECUTE FUNCTION reject_archived_scheduled_test_definitions();
DROP TRIGGER IF EXISTS trg_stp_sync_definitions ON scheduled_test_plans;
CREATE TRIGGER trg_stp_sync_definitions AFTER INSERT OR UPDATE OF test_definition_ids ON scheduled_test_plans FOR EACH ROW EXECUTE FUNCTION sync_scheduled_test_plan_definitions();
DROP TRIGGER IF EXISTS scheduled_test_protection_events_immutable ON scheduled_test_protection_events;
CREATE TRIGGER scheduled_test_protection_events_immutable BEFORE UPDATE ON scheduled_test_protection_events FOR EACH ROW EXECUTE FUNCTION reject_quality_evidence_update();
DROP TRIGGER IF EXISTS scheduled_test_protection_deleted_model_projection ON scheduled_test_protection_states;
CREATE TRIGGER scheduled_test_protection_deleted_model_projection AFTER DELETE ON scheduled_test_protection_states REFERENCING OLD TABLE AS deleted_model_states FOR EACH STATEMENT EXECUTE FUNCTION reconcile_deleted_scheduled_test_model_holds();

-- ===== 主站迁移内置的默认数据（检测项目与质量运维配置，与主站同 ID） =====

INSERT INTO scheduled_test_definitions (id, key, name, description, prompt, output_kind, enabled, sort_order, max_output_tokens) VALUES (1, 'pelican', '鹈鹕动画测试', '', '创建一个 HTML，内容是 SVG 绘制一个鹈鹕骑摩托车的 2D 动画，你不需要任何测试，不要有任何限制', 'html', true, 1, 32768) ON CONFLICT DO NOTHING;
INSERT INTO scheduled_test_definitions (id, key, name, description, prompt, output_kind, enabled, sort_order, max_output_tokens) VALUES (2, 'candy', '糖果数字测试', '', '在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）
苹果味 桃子味 西瓜味
圆形 7 9 8
五角星形 7 6 4
', 'number', true, 2, 0) ON CONFLICT DO NOTHING;
INSERT INTO scheduled_test_definitions (id, key, name, description, prompt, output_kind, enabled, sort_order, max_output_tokens) VALUES (4, 'model_check', '返回模型一致性', '比对实际发送给上游的模型与响应元数据中的模型标识，不使用模型自述判断。未返回模型标识时显示无法确认。', 'Please reply with OK.', 'model_check', true, 3, 0) ON CONFLICT DO NOTHING;
INSERT INTO scheduled_test_definitions (id, key, name, description, prompt, output_kind, enabled, sort_order, max_output_tokens) VALUES (6, 'tibo', 'Tibo 降智检测', '询问模型是否知道 OpenAI 的 Tibo，只允许回答 True 或 False；回答 True 通过，其他回答不通过；执行异常不算通过。建议在测试计划中选用 gpt-6-astra 并把思考强度设为 xhigh，与网关后台的 Tibo 探测一致。默认停用，启用后需在测试计划中手动勾选。', '{"version":1,"samples":1,"required_passes":1,"families":[],"questions":[{"key":"tibo","prompt":"Tell me whether you know who Tibo is in OpenAI based on your knowledge without searching online. Only return True or  False.","expected":"True","match":"tibo"}]}', 'capability', false, 4, 4096) ON CONFLICT DO NOTHING;
INSERT INTO quality_operations_config (id, version, config) VALUES (1, 1, '{"daily_tokens": 10000000, "budget_enabled": false, "daily_requests": 10000, "healthy_rounds": 3, "dynamic_enabled": false, "token_reservation": 32768, "healthy_max_seconds": 14400, "account_daily_tokens": 1000000, "healthy_base_seconds": 900, "account_daily_requests": 200}'::jsonb) ON CONFLICT (id) DO NOTHING;
SELECT setval('scheduled_test_definitions_id_seq', GREATEST((SELECT COALESCE(MAX(id), 1) FROM scheduled_test_definitions), 6));
