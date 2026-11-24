-- Kitty billing rules ported from the Kitty main site (migration 354 there).
-- Table/column names match the main site so its rows can be copied verbatim.

-- 1. Global long-context pricing rules: when an enabled rule matches the exact
--    billing model and input + cache_read + cache_write strictly exceeds
--    threshold_tokens, the whole request's four token items are multiplied.
CREATE TABLE IF NOT EXISTS global_context_pricing_rules (
    id BIGSERIAL PRIMARY KEY,
    model TEXT NOT NULL,
    platform TEXT NOT NULL,
    threshold_tokens BIGINT NOT NULL,
    input_multiplier DOUBLE PRECISION NOT NULL,
    output_multiplier DOUBLE PRECISION NOT NULL,
    cache_read_multiplier DOUBLE PRECISION NOT NULL,
    cache_write_multiplier DOUBLE PRECISION NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    revision BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT global_context_pricing_rules_model_key UNIQUE (model),
    CONSTRAINT global_context_pricing_rules_threshold_tokens_check CHECK (threshold_tokens > 0 AND threshold_tokens <= 100000000),
    CONSTRAINT global_context_pricing_rules_input_multiplier_check CHECK (input_multiplier >= 0.01 AND input_multiplier <= 100),
    CONSTRAINT global_context_pricing_rules_output_multiplier_check CHECK (output_multiplier >= 0.01 AND output_multiplier <= 100),
    CONSTRAINT global_context_pricing_rules_cache_read_multiplier_check CHECK (cache_read_multiplier >= 0.01 AND cache_read_multiplier <= 100),
    CONSTRAINT global_context_pricing_rules_cache_write_multiplier_check CHECK (cache_write_multiplier >= 0.01 AND cache_write_multiplier <= 100)
);

COMMENT ON TABLE global_context_pricing_rules IS 'Kitty: 全局长上下文计费规则（按精确计费模型ID匹配，命中时关闭模型自带阶梯）';

-- 2. Per-group image prices by model and resolution (USD per image, final
--    customer price; wins over image_price_1k/2k/4k).
ALTER TABLE groups ADD COLUMN IF NOT EXISTS image_model_prices JSONB;

COMMENT ON COLUMN groups.image_model_prices IS 'Kitty: 按模型和分辨率覆盖图片单张最终价格（USD），形如 {"gpt-image-2":{"1K":0.05}}';
