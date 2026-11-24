-- Kitty 网站工单（用户提交、管理员回复）。
-- 表名、字段名、类型、默认值、约束名、索引名与 Kitty 主站 354_kitty_business_and_media.sql 完全一致，
-- 便于之后原样迁移主站数据（迁完需要把 support_center_tickets_id_seq、
-- support_center_messages_id_seq 推到 MAX(id)）。
-- 开关与表单配置沿用主站的 settings 键：support_ticket_enabled、support_ticket_config（不在这里写入）。

SET LOCAL lock_timeout = '2s';

CREATE TABLE IF NOT EXISTS support_center_tickets (
    id bigserial NOT NULL,
    user_id bigint NOT NULL,
    category character varying(32) DEFAULT ''::character varying NOT NULL,
    title character varying(100) NOT NULL,
    status character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    message_count integer DEFAULT 0 NOT NULL,
    last_message_at timestamp with time zone DEFAULT now() NOT NULL,
    last_message_role character varying(8) DEFAULT 'user'::character varying NOT NULL,
    user_unread boolean DEFAULT false NOT NULL,
    admin_unread boolean DEFAULT true NOT NULL,
    closed_at timestamp with time zone,
    closed_by_role character varying(8) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT support_center_tickets_pkey PRIMARY KEY (id),
    CONSTRAINT support_center_tickets_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT support_center_tickets_closed_by_role_check CHECK (((closed_by_role)::text = ANY (ARRAY[(''::character varying)::text, ('user'::character varying)::text, ('admin'::character varying)::text]))),
    CONSTRAINT support_center_tickets_last_message_role_check CHECK (((last_message_role)::text = ANY (ARRAY[('user'::character varying)::text, ('admin'::character varying)::text]))),
    CONSTRAINT support_center_tickets_status_check CHECK (((status)::text = ANY (ARRAY[('pending'::character varying)::text, ('processing'::character varying)::text, ('replied'::character varying)::text, ('closed'::character varying)::text])))
);

CREATE TABLE IF NOT EXISTS support_center_messages (
    id bigserial NOT NULL,
    ticket_id bigint NOT NULL,
    author_role character varying(8) NOT NULL,
    author_user_id bigint,
    body text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT support_center_messages_pkey PRIMARY KEY (id),
    CONSTRAINT support_center_messages_ticket_id_fkey FOREIGN KEY (ticket_id) REFERENCES support_center_tickets(id) ON DELETE CASCADE,
    CONSTRAINT support_center_messages_author_user_id_fkey FOREIGN KEY (author_user_id) REFERENCES users(id) ON DELETE SET NULL,
    CONSTRAINT support_center_messages_author_role_check CHECK (((author_role)::text = ANY (ARRAY[('user'::character varying)::text, ('admin'::character varying)::text]))),
    CONSTRAINT support_center_messages_body_check CHECK (((char_length(body) >= 1) AND (char_length(body) <= 5000)))
);

CREATE INDEX IF NOT EXISTS idx_support_center_messages_author_time ON support_center_messages USING btree (author_user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_support_center_messages_ticket ON support_center_messages USING btree (ticket_id, id);
CREATE INDEX IF NOT EXISTS idx_support_center_tickets_status_activity ON support_center_tickets USING btree (status, last_message_at DESC);
CREATE INDEX IF NOT EXISTS idx_support_center_tickets_user_activity ON support_center_tickets USING btree (user_id, last_message_at DESC);
