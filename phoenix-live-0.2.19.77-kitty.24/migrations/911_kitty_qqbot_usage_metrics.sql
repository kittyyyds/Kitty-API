-- QQ 机器人成长任务、入群审核、成员画像读取的用量视图。
-- 主站的 qq_bot_usage_metrics 是建在 kitty_permanent_usage（永久累计用量）上的视图，
-- 列名与列顺序与主站一致。CS 的永久累计用量放在第 6 阶段移植；在那之前先建在
-- usage_logs 上。第 6 阶段移植 kitty_permanent_usage 后，用新迁移
-- CREATE OR REPLACE VIEW 改回与主站相同的来源（列名、类型已按主站对齐，代码不用改）。
-- 视图本身没有数据，迁数据时不需要也不能导入。

SET LOCAL lock_timeout = '2s';

CREATE OR REPLACE VIEW qq_bot_usage_metrics AS
 SELECT user_id,
    input_tokens::bigint AS input_tokens,
    output_tokens::bigint AS output_tokens,
    cache_creation_tokens::bigint AS cache_creation_tokens,
    cache_read_tokens::bigint AS cache_read_tokens,
    actual_cost::numeric AS actual_cost,
    billing_type::smallint AS billing_type,
    created_at
   FROM usage_logs;
