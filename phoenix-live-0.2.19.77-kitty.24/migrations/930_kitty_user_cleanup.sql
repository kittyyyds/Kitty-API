-- Kitty 用户清理：无余额长期未用用户的预览/执行记录、清理保护名单、登录绑定归档。
-- 表名、字段名、类型、默认值、约束名与 Kitty 主站 354_kitty_business_and_media.sql 完全一致，便于原样迁数据。
-- 清理是软删除（users.deleted_at），不删历史数据；登录绑定删除前先整行归档到 user_cleanup_identity_archives。

SET LOCAL lock_timeout = '2s';

CREATE TABLE IF NOT EXISTS public.user_cleanup_guards (
    user_id bigint NOT NULL,
    is_system boolean DEFAULT false NOT NULL,
    is_protected boolean DEFAULT false NOT NULL,
    updated_by bigint,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE IF NOT EXISTS public.user_cleanup_identity_archives (
    preview_id uuid NOT NULL,
    user_id bigint NOT NULL,
    identities jsonb DEFAULT '[]'::jsonb NOT NULL,
    channels jsonb DEFAULT '[]'::jsonb NOT NULL,
    adoption_decisions jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE IF NOT EXISTS public.user_cleanup_previews (
    id uuid NOT NULL,
    actor_user_id bigint NOT NULL,
    candidate_ids bigint[] NOT NULL,
    candidate_snapshot jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone DEFAULT (now() + '00:10:00'::interval) NOT NULL,
    completed_at timestamp with time zone,
    result jsonb
);

DO $kitty_schema$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='public.user_cleanup_guards'::regclass AND conname='user_cleanup_guards_pkey') THEN
  ALTER TABLE ONLY public.user_cleanup_guards
    ADD CONSTRAINT user_cleanup_guards_pkey PRIMARY KEY (user_id);
 END IF;
END $kitty_schema$;

DO $kitty_schema$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='public.user_cleanup_identity_archives'::regclass AND conname='user_cleanup_identity_archives_pkey') THEN
  ALTER TABLE ONLY public.user_cleanup_identity_archives
    ADD CONSTRAINT user_cleanup_identity_archives_pkey PRIMARY KEY (preview_id, user_id);
 END IF;
END $kitty_schema$;

DO $kitty_schema$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='public.user_cleanup_previews'::regclass AND conname='user_cleanup_previews_pkey') THEN
  ALTER TABLE ONLY public.user_cleanup_previews
    ADD CONSTRAINT user_cleanup_previews_pkey PRIMARY KEY (id);
 END IF;
END $kitty_schema$;

CREATE INDEX IF NOT EXISTS idx_user_cleanup_previews_actor_created ON public.user_cleanup_previews USING btree (actor_user_id, created_at DESC);

DO $kitty_schema$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='public.user_cleanup_guards'::regclass AND conname='user_cleanup_guards_user_id_fkey') THEN
  ALTER TABLE ONLY public.user_cleanup_guards
    ADD CONSTRAINT user_cleanup_guards_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;
 END IF;
END $kitty_schema$;

DO $kitty_schema$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='public.user_cleanup_identity_archives'::regclass AND conname='user_cleanup_identity_archives_preview_id_fkey') THEN
  ALTER TABLE ONLY public.user_cleanup_identity_archives
    ADD CONSTRAINT user_cleanup_identity_archives_preview_id_fkey FOREIGN KEY (preview_id) REFERENCES public.user_cleanup_previews(id);
 END IF;
END $kitty_schema$;
