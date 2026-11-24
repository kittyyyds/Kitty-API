-- QQ 机器人用量视图改回与主站一致：建在永久累计用量 kitty_permanent_usage 上（主站 354）。
-- 911 先临时建在 usage_logs 上；列名、类型、顺序与 911 相同，QQ 相关查询不用改。
-- 本迁移之前已存在的 CS 用量由后台回填任务（kitty-permanent-usage-backfill）补进永久表，
-- 回填完成前 QQ 排行、成长任务只看得到迁移之后的新用量。

SET LOCAL lock_timeout = '5s';

CREATE OR REPLACE VIEW qq_bot_usage_metrics AS
 SELECT user_id,
    input_tokens,
    output_tokens,
    cache_creation_tokens,
    cache_read_tokens,
    actual_cost,
    billing_type,
    observed_at AS created_at
   FROM kitty_permanent_usage
  WHERE (NOT invalid_metrics);
