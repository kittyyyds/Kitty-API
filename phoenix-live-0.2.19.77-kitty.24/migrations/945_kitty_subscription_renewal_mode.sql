-- Kitty 订阅续费方式：订单记下用户选择的续费方式，履约时按它计算到期时间。
-- 字段名、类型、默认值与 Kitty 主站 354_kitty_business_and_media.sql 完全一致，便于之后原样迁移主站数据。
-- restart = 重开周期（从支付成功时间重新计算，额度补满）；extend = 延长周期（在原到期时间上累加）。
-- 已有订单一律按 extend 处理，与改动前的续费行为相同。

SET LOCAL lock_timeout = '2s';

ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS subscription_renewal_mode character varying(16) DEFAULT 'extend'::character varying NOT NULL;
