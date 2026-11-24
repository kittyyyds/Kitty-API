-- Kitty 渠道状态 V3（组件式状态页 + 可选空闲探测）。
-- 表名、字段名、类型、默认值、约束名、索引名、序列名与 Kitty 主站 354_kitty_business_and_media.sql 完全一致，
-- 便于原样迁移主站配置数据（迁完需把各 *_id_seq 推到 MAX(id)）。
-- V3 页面读 V2 已有的被动聚合表（channel_monitor_v2_*），本迁移不改动这些表。
-- 空闲探测默认关闭：idle_probe 默认 {"groups": [], "enabled": false}，需要管理员手动开启。

SET LOCAL lock_timeout = '2s';

CREATE SEQUENCE IF NOT EXISTS channel_monitor_v3_categories_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

CREATE TABLE IF NOT EXISTS channel_monitor_v3_categories (
    id bigint DEFAULT nextval('channel_monitor_v3_categories_id_seq'::regclass) NOT NULL,
    name character varying(64) NOT NULL,
    description character varying(500) DEFAULT ''::character varying NOT NULL,
    sort_order integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT channel_monitor_v3_categories_pkey PRIMARY KEY (id)
);

ALTER SEQUENCE channel_monitor_v3_categories_id_seq OWNED BY channel_monitor_v3_categories.id;

CREATE SEQUENCE IF NOT EXISTS channel_monitor_v3_components_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

CREATE TABLE IF NOT EXISTS channel_monitor_v3_components (
    id bigint DEFAULT nextval('channel_monitor_v3_components_id_seq'::regclass) NOT NULL,
    category_id bigint,
    name character varying(100) NOT NULL,
    description character varying(500) DEFAULT ''::character varying NOT NULL,
    group_id bigint NOT NULL,
    model character varying(100) DEFAULT ''::character varying NOT NULL,
    degraded_ttft_ms integer DEFAULT 0 NOT NULL,
    show_multiplier boolean DEFAULT true NOT NULL,
    visibility character varying(16) DEFAULT 'group'::character varying NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    sort_order integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT channel_monitor_v3_components_pkey PRIMARY KEY (id),
    CONSTRAINT channel_monitor_v3_components_degraded_ttft_ms_check CHECK (((degraded_ttft_ms = 0) OR ((degraded_ttft_ms >= 1000) AND (degraded_ttft_ms <= 120000)))),
    CONSTRAINT channel_monitor_v3_components_visibility_check CHECK (((visibility)::text = ANY (ARRAY[('group'::character varying)::text, ('public'::character varying)::text]))),
    CONSTRAINT channel_monitor_v3_components_category_id_fkey FOREIGN KEY (category_id) REFERENCES channel_monitor_v3_categories(id) ON DELETE SET NULL,
    CONSTRAINT channel_monitor_v3_components_group_id_fkey FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE
);

ALTER SEQUENCE channel_monitor_v3_components_id_seq OWNED BY channel_monitor_v3_components.id;

CREATE INDEX IF NOT EXISTS idx_channel_monitor_v3_components_order
    ON channel_monitor_v3_components USING btree (category_id, sort_order, id);

CREATE TABLE IF NOT EXISTS channel_monitor_v3_config (
    id smallint DEFAULT 1 NOT NULL,
    version integer DEFAULT 1 NOT NULL,
    interval_minutes integer DEFAULT 5 NOT NULL,
    cells integer DEFAULT 90 NOT NULL,
    availability_range character varying(8) DEFAULT '7d'::character varying NOT NULL,
    down_error_rate numeric(5,4) DEFAULT 0.2 NOT NULL,
    degraded_error_rate numeric(5,4) DEFAULT 0.05 NOT NULL,
    degraded_ttft_ms integer DEFAULT 10000 NOT NULL,
    min_requests integer DEFAULT 1 NOT NULL,
    ignored_error_categories text[] DEFAULT ARRAY['authentication'::text, 'client_cancelled'::text, 'content_policy'::text, 'context_limit'::text, 'group_access'::text, 'model_unsupported'::text, 'not_found'::text, 'quota_or_balance'::text] NOT NULL,
    featured_component_id bigint,
    footer_note character varying(500) DEFAULT ''::character varying NOT NULL,
    updated_by bigint,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    idle_probe jsonb DEFAULT '{"groups": [], "enabled": false}'::jsonb NOT NULL,
    CONSTRAINT channel_monitor_v3_config_pkey PRIMARY KEY (id),
    CONSTRAINT channel_monitor_v3_config_availability_range_check CHECK (((availability_range)::text = ANY (ARRAY[('24h'::character varying)::text, ('7d'::character varying)::text, ('30d'::character varying)::text]))),
    CONSTRAINT channel_monitor_v3_config_cells_check CHECK (((cells >= 30) AND (cells <= 120))),
    CONSTRAINT channel_monitor_v3_config_degraded_error_rate_check CHECK (((degraded_error_rate > (0)::numeric) AND (degraded_error_rate <= (1)::numeric))),
    CONSTRAINT channel_monitor_v3_config_degraded_ttft_ms_check CHECK (((degraded_ttft_ms >= 1000) AND (degraded_ttft_ms <= 120000))),
    CONSTRAINT channel_monitor_v3_config_down_error_rate_check CHECK (((down_error_rate > (0)::numeric) AND (down_error_rate <= (1)::numeric))),
    CONSTRAINT channel_monitor_v3_config_id_check CHECK ((id = 1)),
    CONSTRAINT channel_monitor_v3_config_interval_minutes_check CHECK ((interval_minutes = ANY (ARRAY[1, 2, 3, 5, 10, 15, 30, 60]))),
    CONSTRAINT channel_monitor_v3_config_min_requests_check CHECK (((min_requests >= 1) AND (min_requests <= 1000))),
    CONSTRAINT channel_monitor_v3_config_featured_component_id_fkey FOREIGN KEY (featured_component_id) REFERENCES channel_monitor_v3_components(id) ON DELETE SET NULL
);

CREATE SEQUENCE IF NOT EXISTS channel_monitor_v3_probe_results_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

CREATE TABLE IF NOT EXISTS channel_monitor_v3_probe_results (
    id bigint DEFAULT nextval('channel_monitor_v3_probe_results_id_seq'::regclass) NOT NULL,
    group_id bigint NOT NULL,
    model character varying(100) NOT NULL,
    slot_start timestamp with time zone NOT NULL,
    slot_minutes integer NOT NULL,
    outcome character varying(16) DEFAULT 'running'::character varying NOT NULL,
    latency_ms integer,
    error_category character varying(32) DEFAULT ''::character varying NOT NULL,
    error_message character varying(500) DEFAULT ''::character varying NOT NULL,
    account_id bigint,
    cost_usd numeric(20,10),
    started_at timestamp with time zone DEFAULT now() NOT NULL,
    finished_at timestamp with time zone,
    CONSTRAINT channel_monitor_v3_probe_results_pkey PRIMARY KEY (id),
    CONSTRAINT channel_monitor_v3_probe_results_outcome_check CHECK (((outcome)::text = ANY (ARRAY[('running'::character varying)::text, ('success'::character varying)::text, ('failure'::character varying)::text]))),
    CONSTRAINT channel_monitor_v3_probe_results_slot_minutes_check CHECK (((slot_minutes >= 1) AND (slot_minutes <= 60))),
    CONSTRAINT uq_channel_monitor_v3_probe_slot UNIQUE (group_id, model, slot_start),
    CONSTRAINT channel_monitor_v3_probe_results_group_id_fkey FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE
);

ALTER SEQUENCE channel_monitor_v3_probe_results_id_seq OWNED BY channel_monitor_v3_probe_results.id;

CREATE INDEX IF NOT EXISTS idx_channel_monitor_v3_probe_results_slot
    ON channel_monitor_v3_probe_results USING btree (slot_start);

INSERT INTO channel_monitor_v3_config(id) VALUES(1) ON CONFLICT DO NOTHING;
