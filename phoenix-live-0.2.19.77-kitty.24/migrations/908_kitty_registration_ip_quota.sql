-- Kitty 注册 IP 限额：同一 IP 终身最多注册 2 个账号（开关 registration_ip_limit_enabled，未设置即开启）。
-- 表名、字段名、类型、约束名与 Kitty 主站 354_kitty_business_and_media.sql 完全一致，便于原样迁数据。
-- ip_hash = sha256(规范化 IP)：IPv4（含映射地址）取地址本身，原生 IPv6 聚合到 /64。
-- 计数在建用户的同一事务里原子占用，建号失败会随事务回滚。

SET LOCAL lock_timeout = '2s';

CREATE TABLE IF NOT EXISTS registration_ip_quotas (
    ip_hash character varying(64) NOT NULL,
    account_count smallint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT registration_ip_quotas_pkey PRIMARY KEY (ip_hash),
    CONSTRAINT registration_ip_quotas_account_count_check CHECK (((account_count >= 1) AND (account_count <= 2))),
    CONSTRAINT registration_ip_quotas_ip_hash_check CHECK ((length((ip_hash)::text) = 64))
);
