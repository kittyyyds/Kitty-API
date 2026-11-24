-- Kitty 渠道质量检测（客户页 /test-results 的读取与投票）所依赖的表。
-- 表名、字段名、类型、默认值与 Kitty 主站最终结构一致（主站 066/247-333 号迁移累加的结果），
-- 便于之后原样迁移主站数据。
--
-- 底座已有 scheduled_test_plans / scheduled_test_results（上游 066/070 建表，272 号已停用定时测试，
-- 旧行只作历史保留），这里只补主站新增的字段；旧行的新字段取默认值，读取端按主站规则天然不会展示它们
-- （结果的 target_mode 为空且 account_id 为空）。
--
-- 只建结构：检测的生产端（定时测试、质量运维的执行与判定、触发器、数据转换）属于第 6 阶段，
-- 不在这里迁移；主站 251/265 号的 target_mode CHECK 约束会与底座旧行冲突，也留到第 6 阶段处理。
-- 迁完数据需要把 scheduled_test_plans_id_seq、scheduled_test_results_id_seq、
-- scheduled_test_definitions_id_seq 推到 MAX(id)。

SET LOCAL lock_timeout = '2s';

CREATE TABLE IF NOT EXISTS scheduled_test_definitions (
    id BIGSERIAL PRIMARY KEY,
    key VARCHAR(100) NOT NULL UNIQUE,
    name VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL,
    output_kind VARCHAR(20) NOT NULL DEFAULT 'text',
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE scheduled_test_definitions ADD COLUMN IF NOT EXISTS sort_order INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scheduled_test_definitions ADD COLUMN IF NOT EXISTS max_output_tokens INTEGER NOT NULL DEFAULT 0
    CHECK (max_output_tokens BETWEEN 0 AND 131072);
ALTER TABLE scheduled_test_definitions ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_std_sort_order ON scheduled_test_definitions(sort_order, id);
CREATE INDEX IF NOT EXISTS idx_scheduled_test_definitions_not_deleted
    ON scheduled_test_definitions (sort_order, id) WHERE deleted_at IS NULL;

-- scheduled_test_plans：主站允许按分组建计划，account_id 可为空。
ALTER TABLE scheduled_test_plans ALTER COLUMN account_id DROP NOT NULL;
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS name VARCHAR(200) NOT NULL DEFAULT '';
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS test_definition_id BIGINT;
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS test_type VARCHAR(50) NOT NULL DEFAULT 'account';
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS group_id BIGINT;
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS reasoning_effort VARCHAR(20) NOT NULL DEFAULT '';
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS target_mode VARCHAR(20) NOT NULL DEFAULT 'all_accounts';
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS sort_order INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS test_definition_ids BIGINT[] NOT NULL DEFAULT '{}';
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS protection JSONB NOT NULL DEFAULT '{}';
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS latest_run_id TEXT NOT NULL DEFAULT '';
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS group_ids BIGINT[] NOT NULL DEFAULT '{}';
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS migration_note TEXT NOT NULL DEFAULT '';
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS schedule_config JSONB;
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS excluded_account_ids BIGINT[] NOT NULL DEFAULT '{}';
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS model_ids TEXT[] NOT NULL DEFAULT '{}';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'scheduled_test_plans'::regclass
                   AND conname = 'scheduled_test_plans_test_definition_id_fkey') THEN
        ALTER TABLE scheduled_test_plans ADD CONSTRAINT scheduled_test_plans_test_definition_id_fkey
            FOREIGN KEY (test_definition_id) REFERENCES scheduled_test_definitions(id) ON DELETE RESTRICT;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'scheduled_test_plans'::regclass
                   AND conname = 'scheduled_test_plans_group_id_fkey') THEN
        ALTER TABLE scheduled_test_plans ADD CONSTRAINT scheduled_test_plans_group_id_fkey
            FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_stp_definition ON scheduled_test_plans(test_definition_id);
CREATE INDEX IF NOT EXISTS idx_stp_group_id ON scheduled_test_plans(group_id);
CREATE INDEX IF NOT EXISTS idx_stp_target_mode ON scheduled_test_plans(target_mode);
CREATE INDEX IF NOT EXISTS idx_stp_sort_order ON scheduled_test_plans(sort_order, id);
CREATE INDEX IF NOT EXISTS idx_scheduled_test_plans_group_ids ON scheduled_test_plans USING GIN(group_ids);

CREATE TABLE IF NOT EXISTS scheduled_test_plan_definitions (
    plan_id BIGINT NOT NULL REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
    test_definition_id BIGINT NOT NULL REFERENCES scheduled_test_definitions(id) ON DELETE RESTRICT,
    PRIMARY KEY (plan_id, test_definition_id)
);
CREATE INDEX IF NOT EXISTS idx_stpd_definition ON scheduled_test_plan_definitions(test_definition_id);

-- scheduled_test_results
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS output_kind VARCHAR(20) NOT NULL DEFAULT 'text';
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS output_html TEXT NOT NULL DEFAULT '';
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS output_numeric DOUBLE PRECISION;
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS account_id BIGINT;
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS model_id VARCHAR(100) NOT NULL DEFAULT '';
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS group_id BIGINT;
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS reasoning_effort VARCHAR(20) NOT NULL DEFAULT '';
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS test_definition_id BIGINT;
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS target_mode VARCHAR(20) NOT NULL DEFAULT '';
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS run_id TEXT NOT NULL DEFAULT '';
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS protection_decision JSONB;
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS execution_info JSONB;
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS protection_revoked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS pass_rate_rule_version TEXT NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'scheduled_test_results'::regclass
                   AND conname = 'scheduled_test_results_test_definition_id_fkey') THEN
        ALTER TABLE scheduled_test_results ADD CONSTRAINT scheduled_test_results_test_definition_id_fkey
            FOREIGN KEY (test_definition_id) REFERENCES scheduled_test_definitions(id) ON DELETE RESTRICT;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_str_created ON scheduled_test_results(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_str_target_definition_history
    ON scheduled_test_results (group_id, account_id, test_definition_id, started_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_scheduled_test_results_run
    ON scheduled_test_results (plan_id, run_id, started_at DESC, id DESC);

-- 投票所依赖的检测轮次状态（由第 6 阶段的质量运维写入）。
CREATE TABLE IF NOT EXISTS scheduled_test_protection_states (
    plan_id BIGINT NOT NULL REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    test_definition_id BIGINT NOT NULL REFERENCES scheduled_test_definitions(id) ON DELETE RESTRICT,
    result_id BIGINT REFERENCES scheduled_test_results(id) ON DELETE SET NULL,
    result_started_at TIMESTAMPTZ NOT NULL,
    generation BIGINT NOT NULL DEFAULT 1,
    automated_verdict TEXT NOT NULL DEFAULT 'pending' CHECK (automated_verdict IN ('pending', 'pass', 'fail')),
    verdict TEXT NOT NULL DEFAULT 'pending' CHECK (verdict IN ('pending', 'pass', 'fail')),
    reason TEXT NOT NULL DEFAULT '',
    blocked BOOLEAN NOT NULL DEFAULT FALSE,
    completed BOOLEAN NOT NULL DEFAULT FALSE,
    rule_config JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    routing_verdict TEXT NOT NULL DEFAULT 'pending' CHECK (routing_verdict IN ('pending', 'pass', 'fail')),
    round_started_at TIMESTAMPTZ NOT NULL DEFAULT 'epoch',
    admin_verdict TEXT NOT NULL DEFAULT '' CHECK (admin_verdict IN ('', 'pass', 'fail')),
    admin_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    admin_decided_at TIMESTAMPTZ,
    recovery_phase TEXT NOT NULL DEFAULT '' CHECK (recovery_phase IN ('', 'cooldown', 'trial')),
    recovery_cooldown_until TIMESTAMPTZ,
    recovery_trial_started_at TIMESTAMPTZ,
    recovery_trial_ends_at TIMESTAMPTZ,
    recovery_trial_requests BIGINT NOT NULL DEFAULT 0 CHECK (recovery_trial_requests >= 0),
    trigger_event_id BIGINT,
    model_key TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (plan_id, account_id, test_definition_id, model_key)
);
CREATE INDEX IF NOT EXISTS idx_st_protection_result ON scheduled_test_protection_states(result_id);
CREATE INDEX IF NOT EXISTS idx_st_protection_account_blocked ON scheduled_test_protection_states(account_id) WHERE blocked;
CREATE INDEX IF NOT EXISTS idx_st_protection_recovery_due
    ON scheduled_test_protection_states (updated_at, plan_id, account_id, test_definition_id)
    WHERE blocked AND recovery_phase <> '';

-- 客户投票：每人每个结果一票，可改票（ON CONFLICT 更新）。
CREATE TABLE IF NOT EXISTS scheduled_test_votes (
    result_id BIGINT NOT NULL REFERENCES scheduled_test_results(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    generation BIGINT NOT NULL,
    vote TEXT NOT NULL CHECK (vote IN ('pass', 'fail')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (result_id, user_id)
);
