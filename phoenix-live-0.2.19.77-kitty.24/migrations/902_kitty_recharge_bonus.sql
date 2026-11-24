-- Kitty 充值赠送（可过期赠额）。
-- 表名、字段名、类型、约束名与 Kitty 主站 354_kitty_business_and_media.sql 完全一致，
-- 便于之后原样迁移主站数据（迁完需要把各 *_id_seq 推到 MAX(id)）。
-- 赠额会同时计入 users.balance；recharge_bonus_grants.remaining_amount 记录余额里还属于赠额的部分。

SET LOCAL lock_timeout = '2s';

CREATE TABLE IF NOT EXISTS recharge_bonus_rules (
    id bigserial NOT NULL,
    recharge_amount numeric(20,8) NOT NULL,
    bonus_amount numeric(20,8) NOT NULL,
    validity_days integer NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    starts_at timestamp with time zone,
    ends_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT recharge_bonus_rules_pkey PRIMARY KEY (id),
    CONSTRAINT recharge_bonus_rules_recharge_amount_key UNIQUE (recharge_amount),
    CONSTRAINT recharge_bonus_rules_amount_positive CHECK ((recharge_amount > (0)::numeric)),
    CONSTRAINT recharge_bonus_rules_bonus_positive CHECK ((bonus_amount > (0)::numeric)),
    CONSTRAINT recharge_bonus_rules_validity_nonnegative CHECK ((validity_days >= 0)),
    CONSTRAINT recharge_bonus_rules_window_valid CHECK (((ends_at IS NULL) OR (starts_at IS NULL) OR (ends_at > starts_at)))
);

CREATE INDEX IF NOT EXISTS idx_recharge_bonus_rules_active
    ON recharge_bonus_rules USING btree (enabled, recharge_amount, starts_at, ends_at);

CREATE TABLE IF NOT EXISTS recharge_bonus_order_snapshots (
    payment_order_id bigint NOT NULL,
    rule_id bigint,
    recharge_amount numeric(20,8) NOT NULL,
    bonus_amount numeric(20,8) NOT NULL,
    validity_days integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT recharge_bonus_order_snapshots_pkey PRIMARY KEY (payment_order_id),
    CONSTRAINT recharge_bonus_order_snapshots_payment_order_id_fkey FOREIGN KEY (payment_order_id) REFERENCES payment_orders(id) ON DELETE CASCADE,
    CONSTRAINT recharge_bonus_order_snapshots_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES recharge_bonus_rules(id) ON DELETE SET NULL,
    CONSTRAINT recharge_bonus_order_snapshot_amount_positive CHECK ((recharge_amount > (0)::numeric)),
    CONSTRAINT recharge_bonus_order_snapshot_bonus_positive CHECK ((bonus_amount > (0)::numeric)),
    CONSTRAINT recharge_bonus_order_snapshot_validity_nonnegative CHECK ((validity_days >= 0))
);

CREATE TABLE IF NOT EXISTS recharge_bonus_grants (
    id bigserial NOT NULL,
    user_id bigint NOT NULL,
    payment_order_id bigint NOT NULL,
    rule_id bigint,
    granted_amount numeric(20,8) NOT NULL,
    remaining_amount numeric(20,8) NOT NULL,
    revoked_amount numeric(20,8) DEFAULT 0 NOT NULL,
    expires_at timestamp with time zone,
    status character varying(20) DEFAULT 'active'::character varying NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT recharge_bonus_grants_pkey PRIMARY KEY (id),
    CONSTRAINT recharge_bonus_grants_payment_order_id_key UNIQUE (payment_order_id),
    CONSTRAINT recharge_bonus_grants_payment_order_id_fkey FOREIGN KEY (payment_order_id) REFERENCES payment_orders(id) ON DELETE CASCADE,
    CONSTRAINT recharge_bonus_grants_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES recharge_bonus_rules(id) ON DELETE SET NULL,
    CONSTRAINT recharge_bonus_grants_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT recharge_bonus_grants_amounts_bounded CHECK (((remaining_amount + revoked_amount) <= granted_amount)),
    CONSTRAINT recharge_bonus_grants_granted_positive CHECK ((granted_amount > (0)::numeric)),
    CONSTRAINT recharge_bonus_grants_remaining_nonnegative CHECK ((remaining_amount >= (0)::numeric)),
    CONSTRAINT recharge_bonus_grants_revoked_nonnegative CHECK ((revoked_amount >= (0)::numeric)),
    CONSTRAINT recharge_bonus_grants_status_valid CHECK (((status)::text = ANY (ARRAY[('active'::character varying)::text, ('exhausted'::character varying)::text, ('expired'::character varying)::text, ('revoked'::character varying)::text])))
);

CREATE INDEX IF NOT EXISTS idx_recharge_bonus_grants_user_active_expiry
    ON recharge_bonus_grants USING btree (user_id, expires_at, id)
    WHERE ((remaining_amount > (0)::numeric) AND ((status)::text = 'active'::text));

CREATE TABLE IF NOT EXISTS recharge_bonus_ledger (
    id bigserial NOT NULL,
    grant_id bigint NOT NULL,
    user_id bigint NOT NULL,
    request_id character varying(255) NOT NULL,
    entry_type character varying(20) NOT NULL,
    amount numeric(20,8) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT recharge_bonus_ledger_pkey PRIMARY KEY (id),
    CONSTRAINT recharge_bonus_ledger_idempotent UNIQUE (grant_id, request_id, entry_type),
    CONSTRAINT recharge_bonus_ledger_grant_id_fkey FOREIGN KEY (grant_id) REFERENCES recharge_bonus_grants(id) ON DELETE CASCADE,
    CONSTRAINT recharge_bonus_ledger_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT recharge_bonus_ledger_amount_positive CHECK ((amount > (0)::numeric)),
    CONSTRAINT recharge_bonus_ledger_type_valid CHECK (((entry_type)::text = ANY (ARRAY[('grant'::character varying)::text, ('consume'::character varying)::text, ('expire'::character varying)::text, ('revoke'::character varying)::text])))
);

CREATE INDEX IF NOT EXISTS idx_recharge_bonus_ledger_user_created
    ON recharge_bonus_ledger USING btree (user_id, created_at DESC, id DESC);

-- 主站批量生图预扣用；CS 底座没有数据库级预扣路径，这里只建表保留结构，便于原样迁移历史数据。
CREATE TABLE IF NOT EXISTS recharge_bonus_batch_holds (
    id bigserial NOT NULL,
    batch_id character varying(255) NOT NULL,
    user_id bigint NOT NULL,
    grant_id bigint NOT NULL,
    held_amount numeric(20,8) NOT NULL,
    consumed_amount numeric(20,8) DEFAULT 0 NOT NULL,
    restored_amount numeric(20,8) DEFAULT 0 NOT NULL,
    expired_amount numeric(20,8) DEFAULT 0 NOT NULL,
    status character varying(20) DEFAULT 'held'::character varying NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    settled_at timestamp with time zone,
    CONSTRAINT recharge_bonus_batch_holds_pkey PRIMARY KEY (id),
    CONSTRAINT recharge_bonus_batch_holds_unique UNIQUE (batch_id, grant_id),
    CONSTRAINT recharge_bonus_batch_holds_grant_id_fkey FOREIGN KEY (grant_id) REFERENCES recharge_bonus_grants(id) ON DELETE CASCADE,
    CONSTRAINT recharge_bonus_batch_holds_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT recharge_bonus_batch_holds_amount_positive CHECK ((held_amount > (0)::numeric)),
    CONSTRAINT recharge_bonus_batch_holds_parts_bounded CHECK ((((consumed_amount + restored_amount) + expired_amount) <= held_amount)),
    CONSTRAINT recharge_bonus_batch_holds_parts_nonnegative CHECK (((consumed_amount >= (0)::numeric) AND (restored_amount >= (0)::numeric) AND (expired_amount >= (0)::numeric))),
    CONSTRAINT recharge_bonus_batch_holds_status_valid CHECK (((status)::text = ANY (ARRAY[('held'::character varying)::text, ('settled'::character varying)::text])))
);

CREATE INDEX IF NOT EXISTS idx_recharge_bonus_batch_holds_batch
    ON recharge_bonus_batch_holds USING btree (batch_id, status, id);
