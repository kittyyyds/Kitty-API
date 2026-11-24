-- Kitty 单账号智商检测（账号"更多"菜单里的手动检测与单号定时检测）。
-- 表结构与主站 361_account_intelligence_tests.sql 一致，便于原样迁移主站数据。
-- 定时检测的配置与主站一样存放在 settings 表（键 account_intelligence_schedule:<账号ID>），不另建表。
CREATE TABLE IF NOT EXISTS account_intelligence_tests (
 id UUID PRIMARY KEY,
 account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 request_id UUID NOT NULL,
 status VARCHAR(20) NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 finished_at TIMESTAMPTZ,
 payload JSONB NOT NULL,
 UNIQUE(account_id, request_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS account_intelligence_one_running ON account_intelligence_tests(account_id) WHERE status = 'running';
CREATE INDEX IF NOT EXISTS account_intelligence_history ON account_intelligence_tests(account_id, created_at DESC, id DESC);
