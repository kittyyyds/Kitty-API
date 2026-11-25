-- Migration: 953_restore_gemini_antigravity_platforms
-- Kitty 从 Wei-Shaw v0.2.24 恢复 Gemini 与 Antigravity 两个平台
-- （网关、OAuth、Vertex、显式缓存、渠道监控 provider 约束）。
--
-- 原则：
--   - 只放宽约束 / 新建缺失表，不删除任何已有数据；
--   - 233 停用的存量 gemini / antigravity 账号与分组保持原状，由管理员按需重新启用；
--   - 全部语句幂等可重入（IF EXISTS / IF NOT EXISTS）。

-- ---------- 1. 复合路由端点：重新允许 gemini（233 收紧时移除） ----------
-- target_platform 的 CHECK 已在 242 删除，改由应用层平台清单校验。
ALTER TABLE composite_model_routes
    DROP CONSTRAINT IF EXISTS composite_model_routes_endpoint_check;
ALTER TABLE composite_model_routes
    ADD CONSTRAINT composite_model_routes_endpoint_check
    CHECK (endpoint IN ('any', 'messages', 'count_tokens', 'responses', 'chat_completions', 'embeddings', 'images', 'gemini'));

-- ---------- 2. 渠道监控 provider CHECK：重新允许 gemini 与 antigravity ----------
ALTER TABLE channel_monitors
    DROP CONSTRAINT IF EXISTS channel_monitors_provider_check;
ALTER TABLE channel_monitors
    ADD CONSTRAINT channel_monitors_provider_check
    CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok', 'antigravity', 'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go'));

ALTER TABLE channel_monitor_request_templates
    DROP CONSTRAINT IF EXISTS channel_monitor_request_templates_provider_check;
ALTER TABLE channel_monitor_request_templates
    ADD CONSTRAINT channel_monitor_request_templates_provider_check
    CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok', 'antigravity', 'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go'));

-- ---------- 3. Gemini 显式缓存（cachedContents）资源绑定（上游 243 / 244 / 245） ----------
CREATE TABLE IF NOT EXISTS gemini_cached_contents (
    id                BIGSERIAL PRIMARY KEY,
    public_id         VARCHAR(64)  NOT NULL,
    user_id           BIGINT       NOT NULL,
    api_key_id        BIGINT       NOT NULL,
    group_id          BIGINT       NOT NULL,
    account_id        BIGINT       NOT NULL,
    upstream_name     VARCHAR(512) NOT NULL,
    model             VARCHAR(255) NOT NULL,
    upstream_model    VARCHAR(512) NOT NULL,
    display_name      VARCHAR(512) NOT NULL DEFAULT '',
    total_token_count BIGINT       NOT NULL DEFAULT 0,
    expire_time       TIMESTAMPTZ  NOT NULL,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at        TIMESTAMPTZ  NULL
);

ALTER TABLE gemini_cached_contents
    ADD COLUMN IF NOT EXISTS channel_usage JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE gemini_cached_contents
    ADD COLUMN IF NOT EXISTS mutation_token       VARCHAR(64) NULL,
    ADD COLUMN IF NOT EXISTS mutation_lease_until TIMESTAMPTZ NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_gemini_cached_contents_public_id
    ON gemini_cached_contents (public_id);

CREATE INDEX IF NOT EXISTS idx_gemini_cached_contents_api_key_active
    ON gemini_cached_contents (api_key_id, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_gemini_cached_contents_account
    ON gemini_cached_contents (account_id);

CREATE INDEX IF NOT EXISTS idx_gemini_cached_contents_pending_mutation
    ON gemini_cached_contents (mutation_lease_until)
    WHERE mutation_token IS NOT NULL AND deleted_at IS NULL;
