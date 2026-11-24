-- Kitty 渠道监控 V2：糖果检测结果与 V2 卡片增强所需字段。
-- 表名、字段名、类型、默认值与主站最终结构一致（主站 263、316、354 号迁移累加的结果）。
-- candy_probes 默认空数组：未配置糖果检测时不会发出任何探测请求。

SET LOCAL lock_timeout = '2s';

ALTER TABLE channel_monitor_v2_config ADD COLUMN IF NOT EXISTS candy_probes JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE channel_monitor_v2_config ADD COLUMN IF NOT EXISTS candy_history_limit INTEGER NOT NULL DEFAULT 100;
ALTER TABLE channel_monitor_v2_config ADD COLUMN IF NOT EXISTS card_timeline_mode TEXT NOT NULL DEFAULT 'windows';
ALTER TABLE channel_monitor_v2_config ADD COLUMN IF NOT EXISTS group_order BIGINT[] NOT NULL DEFAULT '{}'::bigint[];

CREATE TABLE IF NOT EXISTS channel_monitor_v2_candy_results (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    config_key VARCHAR(64) NOT NULL,
    model VARCHAR(100) NOT NULL,
    reasoning_effort VARCHAR(16) NOT NULL,
    slot TIMESTAMPTZ NOT NULL,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    verdict VARCHAR(16) NOT NULL DEFAULT 'running'
        CHECK (verdict IN ('running', 'correct', 'incorrect', 'error')),
    latency_ms BIGINT NOT NULL DEFAULT 0,
    answer_preview VARCHAR(512) NOT NULL DEFAULT '',
    reason VARCHAR(32) NOT NULL DEFAULT '',
    UNIQUE (group_id, config_key, slot)
);
CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_candy_history ON channel_monitor_v2_candy_results (group_id, checked_at DESC);
CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_candy_history_key ON channel_monitor_v2_candy_results (group_id, config_key, checked_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_monitor_v2_candy_running ON channel_monitor_v2_candy_results (group_id, config_key) WHERE verdict = 'running';

-- 主站 263 / 316：缓存可计入口径与输出速度（TPS）的分子分母，聚合表四张都加。
ALTER TABLE channel_monitor_v2_metrics_1m
    ADD COLUMN IF NOT EXISTS cache_eligible_input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_eligible_creation_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_eligible_read_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_output_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_generation_ms BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_samples BIGINT NOT NULL DEFAULT 0;
ALTER TABLE channel_monitor_v2_user_metrics_1m
    ADD COLUMN IF NOT EXISTS cache_eligible_input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_eligible_creation_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_eligible_read_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_output_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_generation_ms BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_samples BIGINT NOT NULL DEFAULT 0;
ALTER TABLE channel_monitor_v2_metrics_rollup
    ADD COLUMN IF NOT EXISTS cache_eligible_input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_eligible_creation_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_eligible_read_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_output_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_generation_ms BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_samples BIGINT NOT NULL DEFAULT 0;
ALTER TABLE channel_monitor_v2_user_metrics_rollup
    ADD COLUMN IF NOT EXISTS cache_eligible_input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_eligible_creation_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_eligible_read_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_output_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_generation_ms BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_samples BIGINT NOT NULL DEFAULT 0;
