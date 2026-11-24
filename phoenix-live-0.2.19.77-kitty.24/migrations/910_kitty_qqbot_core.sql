-- Kitty QQ 机器人（整套）核心表。
-- 按 Kitty 主站迁移 271～348 的原顺序原样重放，表名、字段名、类型、约束名与主站完全一致，
-- 便于之后原样迁移主站数据（迁完需要把 qq_bot_*_id_seq 推到 MAX(id)）。
-- 与主站的差异（只为适配 CS，均不改变表结构）：
--   * 277 的 CREATE TABLE 补了 IF NOT EXISTS；
--   * qq_bot_rewards_kind_check / qq_bot_reward_codes_source_check 只加最终版本（中间版本省略），
--     最终约束名与内容与主站一致，整份文件可重复执行；
--   * 279 只取 qq_bot_lottery_chance_grants（GPT 直充本身不移植）；
--   * 349（充值优惠券）只取 qq_bot_reward_codes 的列与约束变更，coupon_template_id
--     不建外键（CS 没有 recharge_coupon_templates），reward_type 仍允许历史值 'coupon'，
--     新的优惠券奖励由代码拒绝；
--   * 354 里的 qq_bot_balance_alerts / qq_bot_balance_subscriptions 及其主键、索引、外键。
-- qq_bot_usage_metrics 在主站是视图，见 911。

SET LOCAL lock_timeout = '2s';


-- ===== 主站 271_qq_bot.sql =====
-- QQ group assistant. Rewards and usage are deliberately separate: model calls
-- use the operator's API key; member balances are only credited by rewards.
CREATE TABLE IF NOT EXISTS qq_bot_groups (
    app_id TEXT NOT NULL, group_id TEXT NOT NULL,
    last_event TEXT NOT NULL DEFAULT '', last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (app_id, group_id)
);
CREATE TABLE IF NOT EXISTS qq_bot_bindings (
    app_id TEXT NOT NULL, member_id TEXT NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id),
    nickname TEXT NOT NULL DEFAULT '', group_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (app_id, member_id), UNIQUE (app_id, user_id)
);
CREATE TABLE IF NOT EXISTS qq_bot_bind_requests (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    app_id TEXT NOT NULL, code_hash TEXT NOT NULL UNIQUE,
    member_id TEXT NOT NULL DEFAULT '', nickname TEXT NOT NULL DEFAULT '', group_id TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS qq_bot_members (
    app_id TEXT NOT NULL, group_id TEXT NOT NULL, member_id TEXT NOT NULL,
    inviter_id TEXT NOT NULL DEFAULT '', invited_at TIMESTAMPTZ,
    joined_at TIMESTAMPTZ, left_at TIMESTAMPTZ, last_membership_at TIMESTAMPTZ,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (app_id, group_id, member_id)
);
CREATE TABLE IF NOT EXISTS qq_bot_rewards (
    id BIGSERIAL PRIMARY KEY, reward_key TEXT NOT NULL UNIQUE,
    app_id TEXT NOT NULL, group_id TEXT NOT NULL, member_id TEXT NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id),
    kind TEXT NOT NULL CHECK (kind IN ('checkin','invite')),
    source_user_id BIGINT REFERENCES users(id), source_member_id TEXT NOT NULL DEFAULT '',
    amount NUMERIC(20,8) NOT NULL CHECK (amount > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS qq_bot_rewards_user_time ON qq_bot_rewards(user_id, created_at DESC);
CREATE TABLE IF NOT EXISTS qq_bot_jobs (
    id TEXT PRIMARY KEY, app_id TEXT NOT NULL, event_key TEXT NOT NULL,
    event_type TEXT NOT NULL, group_id TEXT NOT NULL, member_id TEXT NOT NULL DEFAULT '',
    message_id TEXT NOT NULL DEFAULT '', payload JSONB NOT NULL,
    kind TEXT NOT NULL DEFAULT 'event', status TEXT NOT NULL DEFAULT 'queued',
    result_text TEXT NOT NULL DEFAULT '', result_image BYTEA, image_type TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '', input_tokens BIGINT NOT NULL DEFAULT 0, output_tokens BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), started_at TIMESTAMPTZ, finished_at TIMESTAMPTZ,
    UNIQUE (app_id, event_key)
);
CREATE INDEX IF NOT EXISTS qq_bot_jobs_queue ON qq_bot_jobs(created_at) WHERE status = 'queued';
CREATE INDEX IF NOT EXISTS qq_bot_jobs_member ON qq_bot_jobs(app_id, group_id, member_id, created_at DESC);
CREATE TABLE IF NOT EXISTS qq_bot_daily_usage (
    app_id TEXT NOT NULL, scope TEXT NOT NULL, day DATE NOT NULL, kind TEXT NOT NULL,
    count INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (app_id, scope, day, kind)
);
CREATE TABLE IF NOT EXISTS qq_bot_conversations (
    app_id TEXT NOT NULL, group_id TEXT NOT NULL, member_id TEXT NOT NULL,
    messages JSONB NOT NULL DEFAULT '[]', updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (app_id, group_id, member_id)
);


-- ===== 主站 272_qq_bot_email_binding.sql =====
-- Email links carry a one-use bearer token. Only its hash is stored.
CREATE TABLE IF NOT EXISTS qq_bot_email_bind_requests (
    token_hash TEXT PRIMARY KEY,
    app_id TEXT NOT NULL, member_id TEXT NOT NULL, group_id TEXT NOT NULL,
    nickname TEXT NOT NULL DEFAULT '', user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email TEXT NOT NULL, expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (app_id, member_id)
);
CREATE INDEX IF NOT EXISTS qq_bot_email_bind_requests_user ON qq_bot_email_bind_requests(user_id);
CREATE TABLE IF NOT EXISTS qq_bot_email_limits (
    app_id TEXT NOT NULL, scope TEXT NOT NULL, day DATE NOT NULL,
    count INTEGER NOT NULL DEFAULT 0, last_sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (app_id, scope, day)
);


-- ===== 主站 273_qq_bot_activities.sql =====
-- Zero-value check-ins and losing lottery entries still consume their entitlement.
-- (CS) 中间版本的 qq_bot_rewards_kind_check 省略，只保留最终版本，便于重复执行
ALTER TABLE qq_bot_rewards DROP CONSTRAINT IF EXISTS qq_bot_rewards_amount_check;
ALTER TABLE qq_bot_rewards ADD CONSTRAINT qq_bot_rewards_amount_check CHECK (amount >= 0);

CREATE TABLE IF NOT EXISTS qq_bot_lottery_draws (
    id BIGSERIAL PRIMARY KEY,
    request_key TEXT NOT NULL UNIQUE,
    app_id TEXT NOT NULL, member_id TEXT NOT NULL, group_id TEXT NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id),
    prize_name TEXT NOT NULL,
    amount NUMERIC(20,8) NOT NULL CHECK (amount >= 0),
    config_snapshot JSONB NOT NULL,
    recharge_total NUMERIC(20,8) NOT NULL CHECK (recharge_total >= 0),
    earned BIGINT NOT NULL CHECK (earned >= 0),
    remaining BIGINT NOT NULL CHECK (remaining >= 0),
    result_text TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS qq_bot_lottery_draws_user_time ON qq_bot_lottery_draws(user_id, created_at DESC);


-- ===== 主站 274_qq_bot_activity_record_indexes.sql =====
-- Support paginated participation lists and invitation reward lookup without
-- scanning all activity history for each relationship on a page.
CREATE INDEX IF NOT EXISTS qq_bot_rewards_app_kind_time
    ON qq_bot_rewards(app_id, kind, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS qq_bot_rewards_invite_source_member
    ON qq_bot_rewards(app_id, source_member_id, id) WHERE kind='invite';
CREATE INDEX IF NOT EXISTS qq_bot_rewards_invite_source_user
    ON qq_bot_rewards(source_user_id, id) WHERE kind='invite';
CREATE INDEX IF NOT EXISTS qq_bot_lottery_draws_app_time
    ON qq_bot_lottery_draws(app_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS qq_bot_members_invite_time
    ON qq_bot_members(app_id, (COALESCE(invited_at,first_seen_at)) DESC, group_id, member_id)
    WHERE inviter_id<>'';


-- ===== 主站 275_qq_bot_message_references.sql =====
-- Only retain messages actually delivered by this bot, scoped to their group.
-- No callback auth_token, attachment URL, image bytes or private history here.
CREATE TABLE IF NOT EXISTS qq_bot_message_references (
    app_id TEXT NOT NULL,
    group_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    ref_idx TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    is_image BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (app_id, group_id, message_id)
);
CREATE INDEX IF NOT EXISTS qq_bot_message_references_ref
    ON qq_bot_message_references(app_id, group_id, ref_idx) WHERE ref_idx <> '';
CREATE INDEX IF NOT EXISTS qq_bot_message_references_expiry
    ON qq_bot_message_references(created_at);


-- ===== 主站 277_qq_bot_transfer_requests.sql =====
CREATE TABLE IF NOT EXISTS qq_bot_transfer_requests (
    token_hash     text             NOT NULL PRIMARY KEY,
    app_id         text             NOT NULL,
    from_member_id text             NOT NULL,
    from_user_id   bigint           NOT NULL,
    from_email     text             NOT NULL,
    to_user_id     bigint           NOT NULL,
    to_email       text             NOT NULL,
    amount         numeric(18,8)    NOT NULL,
    fee            numeric(18,8)    NOT NULL,
    net_amount     numeric(18,8)    NOT NULL,
    expires_at     timestamptz      NOT NULL,
    created_at     timestamptz      NOT NULL DEFAULT NOW(),
    UNIQUE (app_id, from_member_id)
);


-- ===== 主站 278_qq_bot_group_history.sql =====
-- Shared history starts empty: never expose existing per-member conversations.
CREATE TABLE IF NOT EXISTS qq_bot_group_messages (
    id BIGSERIAL PRIMARY KEY,
    app_id TEXT NOT NULL,
    group_id TEXT NOT NULL,
    message_key TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    content TEXT NOT NULL,
    context_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (app_id, group_id, message_key)
);
CREATE INDEX IF NOT EXISTS idx_qq_bot_group_messages_recent
    ON qq_bot_group_messages (app_id, group_id, context_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_qq_bot_group_messages_expiry
    ON qq_bot_group_messages (created_at);

-- Older references and command replies remain private to the explicit quote.
ALTER TABLE qq_bot_message_references
    ADD COLUMN IF NOT EXISTS shareable BOOLEAN NOT NULL DEFAULT FALSE;


-- ===== 主站 279_gpt_direct_lottery_rewards.sql =====
-- A CDK grants chances once across requests, users, refreshes and worker instances.
CREATE TABLE IF NOT EXISTS qq_bot_lottery_chance_grants (
    source_key TEXT PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    chances INTEGER NOT NULL CHECK (chances BETWEEN 1 AND 1000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS qq_bot_lottery_chance_grants_user ON qq_bot_lottery_chance_grants(user_id);


-- ===== 主站 280_qq_bot_reward_redemption.sql =====
CREATE TABLE IF NOT EXISTS qq_bot_reward_codes (
    id BIGSERIAL PRIMARY KEY,
    app_id TEXT NOT NULL,
    code_hash TEXT NOT NULL UNIQUE,
    code_hint TEXT NOT NULL,
    batch_name TEXT NOT NULL,
    reward_type TEXT NOT NULL CHECK (reward_type IN ('balance','chances','physical','other')),
    amount NUMERIC(20,8) NOT NULL DEFAULT 0,
    quantity INTEGER NOT NULL DEFAULT 0,
    prize_name TEXT NOT NULL DEFAULT '',
    claim_instructions TEXT NOT NULL DEFAULT '',
    target_user_id BIGINT REFERENCES users(id),
    expires_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'unused' CHECK (status IN ('unused','disabled','pending','fulfilled')),
    user_id BIGINT REFERENCES users(id),
    member_id TEXT NOT NULL DEFAULT '',
    group_id TEXT NOT NULL DEFAULT '',
    redeemed_at TIMESTAMPTZ,
    fulfilled_at TIMESTAMPTZ,
    fulfillment_note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((reward_type='balance' AND amount>0 AND amount<=1000 AND quantity=0)
        OR (reward_type<>'balance' AND amount=0 AND quantity BETWEEN 1 AND 1000)),
    CHECK ((status IN ('unused','disabled') AND user_id IS NULL AND redeemed_at IS NULL)
        OR (status IN ('pending','fulfilled') AND user_id IS NOT NULL AND redeemed_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS qq_bot_reward_codes_user ON qq_bot_reward_codes(user_id, id DESC);

-- Persistent, shared across processes and groups. User scopes survive QQ rebinding.
CREATE TABLE IF NOT EXISTS qq_bot_reward_limits (
    scope TEXT PRIMARY KEY,
    last_attempt_at TIMESTAMPTZ NOT NULL DEFAULT 'epoch',
    blocked_until TIMESTAMPTZ NOT NULL DEFAULT 'epoch'
);
CREATE TABLE IF NOT EXISTS qq_bot_reward_attempts (
    request_key TEXT PRIMARY KEY,
    app_id TEXT NOT NULL,
    member_id TEXT NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id),
    code_id BIGINT REFERENCES qq_bot_reward_codes(id),
    failed BOOLEAN NOT NULL,
    result_text TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS qq_bot_reward_attempts_member ON qq_bot_reward_attempts(app_id,member_id,created_at DESC) WHERE failed;
CREATE INDEX IF NOT EXISTS qq_bot_reward_attempts_user ON qq_bot_reward_attempts(user_id,created_at DESC) WHERE failed;
ALTER TABLE qq_bot_jobs ADD COLUMN IF NOT EXISTS reward_code_hash TEXT NOT NULL DEFAULT '';
-- (CS) 中间版本的 qq_bot_rewards_kind_check 省略，只保留最终版本，便于重复执行


-- ===== 主站 283_qq_bot_price_notifications.sql =====
-- Durable baseline and transactional outbox (qq_bot_jobs) for price notices.
CREATE TABLE IF NOT EXISTS qq_bot_price_snapshots (
    app_id TEXT PRIMARY KEY,
    snapshot JSONB NOT NULL,
    next_check_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS qq_bot_jobs_price_dispatch
    ON qq_bot_jobs(app_id, started_at DESC)
    WHERE event_type = 'PRICE_CHANGE_NOTIFICATION';


-- ===== 主站 285_qq_bot_autonomous.sql =====
-- Bounded autonomous chat, isolated by bot and group. No message bodies here.
ALTER TABLE qq_bot_jobs ADD COLUMN IF NOT EXISTS trigger_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE qq_bot_jobs ADD COLUMN IF NOT EXISTS decision_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE qq_bot_jobs ADD COLUMN IF NOT EXISTS model_reserved_at TIMESTAMPTZ;
ALTER TABLE qq_bot_jobs ADD COLUMN IF NOT EXISTS reply_reserved_at TIMESTAMPTZ;
ALTER TABLE qq_bot_jobs ADD COLUMN IF NOT EXISTS reply_sent_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS qq_bot_jobs_autonomous_limits ON qq_bot_jobs(app_id,group_id,reply_reserved_at DESC) WHERE kind='ambient_chat';
CREATE TABLE IF NOT EXISTS qq_bot_autonomous_state (
 app_id TEXT NOT NULL, group_id TEXT NOT NULL, messages BIGINT NOT NULL DEFAULT 0,
 last_message_id TEXT NOT NULL DEFAULT '', last_message_at TIMESTAMPTZ,
 last_text_hash TEXT NOT NULL DEFAULT '', last_candidate_at TIMESTAMPTZ,
 last_reply_at TIMESTAMPTZ, last_reply_member TEXT NOT NULL DEFAULT '',
 last_reply_kind TEXT NOT NULL DEFAULT '', last_reply_trigger TEXT NOT NULL DEFAULT '',
 last_reply_counter BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY(app_id,group_id)
);
CREATE TABLE IF NOT EXISTS qq_bot_autonomous_seen (
 app_id TEXT NOT NULL, group_id TEXT NOT NULL, message_id TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), PRIMARY KEY(app_id,group_id,message_id)
);
CREATE INDEX IF NOT EXISTS qq_bot_autonomous_seen_expiry ON qq_bot_autonomous_seen(created_at);
CREATE TABLE IF NOT EXISTS qq_bot_autonomous_decisions (
 app_id TEXT NOT NULL, group_id TEXT NOT NULL, day DATE NOT NULL,
 reason TEXT NOT NULL, count BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY(app_id,group_id,day,reason)
);


-- ===== 主站 286_qq_bot_health_notifications.sql =====
CREATE TABLE IF NOT EXISTS qq_bot_health_snapshots (
    app_id TEXT PRIMARY KEY,
    snapshot JSONB NOT NULL,
    next_check_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS qq_bot_health_notification_queue ON qq_bot_jobs(app_id,created_at)
    WHERE event_type='HEALTH_CHANGE_NOTIFICATION' AND status IN ('queued','running');


-- ===== 主站 287_qq_bot_growth_rewards.sql =====
-- Automatic rewards share the existing fulfillment workflow, without issuing
-- redeemable codes. The account-scoped source key prevents duplicate grants.
ALTER TABLE qq_bot_reward_codes ALTER COLUMN code_hash DROP NOT NULL;
ALTER TABLE qq_bot_reward_codes ADD COLUMN IF NOT EXISTS source_kind TEXT NOT NULL DEFAULT 'code';
ALTER TABLE qq_bot_reward_codes ADD COLUMN IF NOT EXISTS source_key TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS qq_bot_reward_codes_source_key ON qq_bot_reward_codes(source_key);
-- (CS) 中间版本的 qq_bot_reward_codes_source_check 省略，只保留最终版本，便于重复执行
-- (CS) 中间版本的 qq_bot_rewards_kind_check 省略，只保留最终版本，便于重复执行


-- ===== 主站 288_qq_bot_daily_rankings.sql =====
-- Version reward rules by Beijing calendar day. Editing today's configuration
-- cannot change an earlier day's winners or replay an already settled day.
CREATE TABLE IF NOT EXISTS qq_bot_ranking_config_versions (
    app_id TEXT NOT NULL,
    effective_day DATE NOT NULL,
    config JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(app_id, effective_day)
);
CREATE TABLE IF NOT EXISTS qq_bot_ranking_settlements (
    app_id TEXT NOT NULL,
    ranking_day DATE NOT NULL,
    rules JSONB NOT NULL,
    reward_count INTEGER NOT NULL DEFAULT 0,
    settled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(app_id, ranking_day)
);
-- (CS) 中间版本的 qq_bot_reward_codes_source_check 省略，只保留最终版本，便于重复执行
ALTER TABLE qq_bot_rewards DROP CONSTRAINT IF EXISTS qq_bot_rewards_kind_check;
ALTER TABLE qq_bot_rewards ADD CONSTRAINT qq_bot_rewards_kind_check CHECK (kind IN ('checkin','invite','lottery','redemption','growth','streak','ranking'));


-- ===== 主站 289_qq_bot_ranking_claims.sql =====
-- Daily settlement freezes eligibility; only an explicit welfare claim grants
-- the reward. Existing automatic grants and settled dates remain untouched.
CREATE TABLE IF NOT EXISTS qq_bot_ranking_claims (
    id BIGSERIAL PRIMARY KEY,
    app_id TEXT NOT NULL,
    ranking_day DATE NOT NULL,
    ranking_kind TEXT NOT NULL CHECK (ranking_kind IN ('tokens','amount')),
    rank INTEGER NOT NULL CHECK (rank BETWEEN 1 AND 10),
    user_id BIGINT NOT NULL REFERENCES users(id),
    source_key TEXT NOT NULL UNIQUE,
    reward JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_at TIMESTAMPTZ,
    UNIQUE(app_id,ranking_day,ranking_kind,user_id)
);
CREATE INDEX IF NOT EXISTS idx_qq_bot_ranking_claims_user
    ON qq_bot_ranking_claims(app_id,user_id,ranking_day DESC,id DESC);


-- ===== 主站 290_qq_bot_periodic_growth.sql =====
-- Store the exact progress already exchanged for recurring rewards. Changing
-- an interval applies only to the remainder; switching metrics keeps each
-- metric's ledger, including when switching back. Account keys survive rebinding.
CREATE TABLE IF NOT EXISTS qq_bot_periodic_growth_progress (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    task_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('usage_count','usage_tokens','usage_amount','recharge_total','checkin_total')),
    consumed NUMERIC NOT NULL DEFAULT 0 CHECK (consumed >= 0),
    cycles BIGINT NOT NULL DEFAULT 0 CHECK (cycles >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id, task_id, kind)
);


-- ===== 主站 292_qq_bot_image_references.sql =====
-- Reuse generated bytes already retained in qq_bot_jobs for 24 hours.
ALTER TABLE qq_bot_message_references
    ADD COLUMN IF NOT EXISTS image_job_id TEXT NOT NULL DEFAULT '';

-- Short-lived attachment locators for exact user-message image references.
-- No image bytes, callback auth_token or message body is copied here.
CREATE TABLE IF NOT EXISTS qq_bot_incoming_images (
    app_id TEXT NOT NULL,
    group_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    ref_idx TEXT NOT NULL DEFAULT '',
    attachments JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (app_id, group_id, message_id)
);
CREATE INDEX IF NOT EXISTS qq_bot_incoming_images_ref
    ON qq_bot_incoming_images(app_id, group_id, ref_idx) WHERE ref_idx <> '';
CREATE INDEX IF NOT EXISTS qq_bot_incoming_images_expiry
    ON qq_bot_incoming_images(created_at);


-- ===== 主站 297_qq_bot_moderation.sql =====
-- Persist only message identities and keyed content hashes, never message bodies.
CREATE TABLE IF NOT EXISTS qq_bot_moderation_events (
 app_id TEXT NOT NULL, sandbox BOOLEAN NOT NULL, group_id TEXT NOT NULL,
 message_id TEXT NOT NULL, member_id TEXT NOT NULL, policy_hash TEXT NOT NULL,
 content_hash TEXT NOT NULL DEFAULT '', sent_at TIMESTAMPTZ NOT NULL,
 blocked BOOLEAN NOT NULL DEFAULT FALSE, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(app_id,sandbox,group_id,message_id)
);
CREATE INDEX IF NOT EXISTS qq_bot_moderation_events_window ON qq_bot_moderation_events(app_id,sandbox,group_id,member_id,sent_at DESC);
CREATE INDEX IF NOT EXISTS qq_bot_moderation_events_expiry ON qq_bot_moderation_events(created_at);
CREATE TABLE IF NOT EXISTS qq_bot_moderation_members (
 app_id TEXT NOT NULL, sandbox BOOLEAN NOT NULL, group_id TEXT NOT NULL, member_id TEXT NOT NULL,
 cooldown_until TIMESTAMPTZ NOT NULL DEFAULT 'epoch', last_muted_at TIMESTAMPTZ NOT NULL DEFAULT 'epoch',
 active_incident_id TEXT NOT NULL DEFAULT '',
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), PRIMARY KEY(app_id,sandbox,group_id,member_id)
);
CREATE TABLE IF NOT EXISTS qq_bot_moderation_incidents (
 id TEXT PRIMARY KEY, app_id TEXT NOT NULL, sandbox BOOLEAN NOT NULL, group_id TEXT NOT NULL,
 member_id TEXT NOT NULL, policy_hash TEXT NOT NULL, reason TEXT NOT NULL,
 mute_seconds INTEGER NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS qq_bot_moderation_incidents_recent ON qq_bot_moderation_incidents(app_id,sandbox,created_at DESC);
CREATE TABLE IF NOT EXISTS qq_bot_moderation_operations (
 id BIGSERIAL PRIMARY KEY, incident_id TEXT NOT NULL REFERENCES qq_bot_moderation_incidents(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK (kind IN ('mute','recall')), message_id TEXT NOT NULL DEFAULT '',
 expires_at TIMESTAMPTZ NOT NULL, status TEXT NOT NULL DEFAULT 'queued', error TEXT NOT NULL DEFAULT '',
 started_at TIMESTAMPTZ, finished_at TIMESTAMPTZ,
 UNIQUE(incident_id,kind,message_id)
);
CREATE INDEX IF NOT EXISTS qq_bot_moderation_operations_pending ON qq_bot_moderation_operations(kind,id) WHERE status='queued';
CREATE TABLE IF NOT EXISTS qq_bot_moderation_rates (
 app_id TEXT NOT NULL, sandbox BOOLEAN NOT NULL, kind TEXT NOT NULL,
 next_at TIMESTAMPTZ NOT NULL DEFAULT 'epoch', PRIMARY KEY(app_id,sandbox,kind)
);


-- ===== 主站 298_qq_bot_member_hints.sql =====
-- Short-lived display hints let operators identify recent speakers even when
-- QQ omits their nickname. These fields never participate in authorization.
ALTER TABLE qq_bot_jobs ADD COLUMN IF NOT EXISTS member_nickname TEXT NOT NULL DEFAULT '';
ALTER TABLE qq_bot_jobs ADD COLUMN IF NOT EXISTS message_preview TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS qq_bot_jobs_recent_speakers
    ON qq_bot_jobs(app_id, created_at DESC, id DESC)
    WHERE event_type IN ('GROUP_AT_MESSAGE_CREATE','GROUP_MESSAGE_CREATE') AND member_id<>'';


-- ===== 主站 299_qq_bot_transfer_history.sql =====
-- Durable, administrator-only receipts for completed transfers. Confirmation
-- tokens are deliberately excluded; old short-lived requests are not fabricated.
CREATE TABLE IF NOT EXISTS qq_bot_transfer_history (
 id BIGSERIAL PRIMARY KEY,
 app_id TEXT NOT NULL,
 from_member_id TEXT NOT NULL,
 from_user_id BIGINT NOT NULL,
 to_user_id BIGINT NOT NULL,
 from_email TEXT NOT NULL,
 to_email TEXT NOT NULL,
 amount NUMERIC(18,8) NOT NULL CHECK(amount > 0),
 fee NUMERIC(18,8) NOT NULL CHECK(fee >= 0),
 net_amount NUMERIC(18,8) NOT NULL CHECK(net_amount > 0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 CHECK(amount = fee + net_amount)
);
CREATE INDEX IF NOT EXISTS qq_bot_transfer_history_app_time ON qq_bot_transfer_history(app_id,created_at DESC,id DESC);


-- ===== 主站 302_qq_bot_image_intent.sql =====
-- Receipts contain static decisions and hashes, never prompts or credentials.
ALTER TABLE qq_bot_jobs ADD COLUMN IF NOT EXISTS image_decision JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE qq_bot_message_references ADD COLUMN IF NOT EXISTS source_job_id TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS qq_bot_pending_image_plans (
 app_id TEXT NOT NULL, group_id TEXT NOT NULL, member_id TEXT NOT NULL,
 job_id TEXT NOT NULL REFERENCES qq_bot_jobs(id) ON DELETE CASCADE,
 source_message_id TEXT NOT NULL,
 action TEXT NOT NULL CHECK(action IN ('generate_image','edit_image')),
 prompt TEXT NOT NULL CHECK(char_length(prompt) BETWEEN 1 AND 6000),
 request_quote TEXT NOT NULL CHECK(char_length(request_quote) BETWEEN 1 AND 300),
 expires_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(app_id,group_id,member_id)
);
CREATE INDEX IF NOT EXISTS qq_bot_pending_image_plans_expiry ON qq_bot_pending_image_plans(expires_at);


-- ===== 主站 304_qq_bot_member_context.sql =====
-- Existing shared replies may already combine several members' conversations.
-- Do not backfill ownership: only new, explicitly scoped history is used by
-- member-directed replies. Existing rows remain available for ambient context.
ALTER TABLE qq_bot_group_messages ADD COLUMN IF NOT EXISTS member_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_qq_bot_group_messages_member
 ON qq_bot_group_messages (app_id,group_id,member_id,context_at DESC,id DESC);


-- ===== 主站 306_qq_bot_spam_reports.sql =====
-- Only enabled groups retain a bounded, short-lived encrypted text snapshot.
-- Original QQ message IDs, not quote text or reporter-supplied authors, bind evidence.
CREATE TABLE IF NOT EXISTS qq_bot_spam_report_sources (
 app_id TEXT NOT NULL, sandbox BOOLEAN NOT NULL, group_id TEXT NOT NULL,
 message_id TEXT NOT NULL, ref_idx TEXT NOT NULL DEFAULT '', member_id TEXT NOT NULL,
 member_role TEXT NOT NULL DEFAULT '', is_bot BOOLEAN NOT NULL DEFAULT FALSE,
 eligible BOOLEAN NOT NULL DEFAULT FALSE, content_encrypted TEXT NOT NULL DEFAULT '', content_hash TEXT NOT NULL DEFAULT '',
 policy_hash TEXT NOT NULL, sent_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(app_id,sandbox,group_id,message_id)
);
CREATE INDEX IF NOT EXISTS qq_bot_spam_report_sources_reference ON qq_bot_spam_report_sources(app_id,sandbox,group_id,ref_idx) WHERE ref_idx<>'';
CREATE INDEX IF NOT EXISTS qq_bot_spam_report_sources_expiry ON qq_bot_spam_report_sources(sent_at);
CREATE INDEX IF NOT EXISTS qq_bot_spam_report_sources_member ON qq_bot_spam_report_sources(app_id,sandbox,group_id,member_id,sent_at DESC);

-- Deduplicate the target message across reporters, workers, and restarts.
-- No raw source text, URL, model response, or evidence is retained in audit rows.
CREATE TABLE IF NOT EXISTS qq_bot_spam_reports (
 id TEXT PRIMARY KEY, app_id TEXT NOT NULL, sandbox BOOLEAN NOT NULL, group_id TEXT NOT NULL,
 reporter_id TEXT NOT NULL, message_id TEXT NOT NULL, member_id TEXT NOT NULL,
 policy_hash TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'evaluating',
 category TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '', incident_id TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), finished_at TIMESTAMPTZ,
 UNIQUE(app_id,sandbox,group_id,message_id)
);
CREATE INDEX IF NOT EXISTS qq_bot_spam_reports_reporter ON qq_bot_spam_reports(app_id,sandbox,reporter_id,created_at DESC);
CREATE INDEX IF NOT EXISTS qq_bot_spam_reports_group ON qq_bot_spam_reports(app_id,sandbox,group_id,created_at DESC);
CREATE INDEX IF NOT EXISTS qq_bot_spam_reports_incident ON qq_bot_spam_reports(incident_id) WHERE incident_id<>'';
CREATE INDEX IF NOT EXISTS qq_bot_spam_reports_expiry ON qq_bot_spam_reports(created_at);


-- ===== 主站 327_qq_bot_site_queries.sql =====
-- Keep query outcome separate from model execution and QQ delivery. No raw
-- provider errors, account IDs, result bodies or credentials are stored here.
ALTER TABLE qq_bot_jobs ADD COLUMN IF NOT EXISTS site_query JSONB NOT NULL DEFAULT '{}'::jsonb;
CREATE INDEX IF NOT EXISTS idx_qq_bot_jobs_site_query_member
 ON qq_bot_jobs(app_id,group_id,member_id,created_at DESC,id DESC)
 WHERE site_query->>'kind' IS NOT NULL;


-- ===== 主站 328_qq_bot_chat_execution.sql =====
-- Bounded explicit-chat retry diagnostics only. Missing usage is unknown, not zero.
-- No raw provider errors, prompt content or credentials are stored.
ALTER TABLE qq_bot_jobs ADD COLUMN IF NOT EXISTS chat_execution JSONB;


-- ===== 主站 343_qq_bot_autonomous_followups.sql =====
-- Persist bounded continuation counts across workers and restarts.
ALTER TABLE qq_bot_autonomous_state
    ADD COLUMN IF NOT EXISTS followup_count INTEGER NOT NULL DEFAULT 0;


-- ===== 主站 348_qq_bot_autonomous_delivery.sql =====
-- Additive queue and observability metadata; existing policies remain opt-in.
ALTER TABLE qq_bot_jobs
    ADD COLUMN IF NOT EXISTS autonomous_ready_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS autonomous_data JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE qq_bot_autonomous_seen
    ADD COLUMN IF NOT EXISTS member_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS job_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS ends_conversation BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX IF NOT EXISTS qq_bot_autonomous_member_endings
    ON qq_bot_autonomous_seen(app_id,group_id,member_id,created_at DESC)
    WHERE ends_conversation;
CREATE INDEX IF NOT EXISTS qq_bot_autonomous_daily_metrics
    ON qq_bot_jobs(app_id,created_at) WHERE kind='ambient_chat';


-- ===== 主站 349_recharge_coupons.sql（只取 qq_bot_reward_codes 部分，去掉优惠券模板外键）=====
ALTER TABLE qq_bot_reward_codes ADD COLUMN IF NOT EXISTS coupon_template_id BIGINT;
ALTER TABLE qq_bot_reward_codes DROP CONSTRAINT IF EXISTS qq_bot_reward_codes_reward_type_check;
ALTER TABLE qq_bot_reward_codes ADD CONSTRAINT qq_bot_reward_codes_reward_type_check CHECK (reward_type IN ('balance','chances','physical','other','coupon'));
ALTER TABLE qq_bot_reward_codes DROP CONSTRAINT IF EXISTS qq_bot_reward_codes_source_check;
ALTER TABLE qq_bot_reward_codes ADD CONSTRAINT qq_bot_reward_codes_source_check CHECK (
 (source_kind='code' AND source_key IS NULL AND code_hash IS NOT NULL)
 OR (source_kind IN ('growth','streak','ranking','checkin','invite','lottery') AND source_key IS NOT NULL AND code_hash IS NULL AND status IN ('pending','fulfilled'))
);


-- ===== 主站 354_kitty_business_and_media.sql（只取 QQ 私聊余额提醒两张表）=====
CREATE TABLE IF NOT EXISTS qq_bot_balance_alerts (
    id text NOT NULL,
    app_id text NOT NULL,
    member_id text NOT NULL,
    user_id bigint NOT NULL,
    subscription_revision text NOT NULL,
    binding_created_at timestamp with time zone NOT NULL,
    balance numeric(20,8) NOT NULL,
    threshold numeric(20,8) NOT NULL,
    site_name text DEFAULT ''::text NOT NULL,
    recharge_url text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    lease_token text DEFAULT ''::text NOT NULL,
    lease_until timestamp with time zone,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    last_error text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    finished_at timestamp with time zone,
    CONSTRAINT qq_bot_balance_alerts_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'leased'::text, 'sent'::text, 'cancelled'::text, 'expired'::text]))),
    CONSTRAINT qq_bot_balance_alerts_threshold_check CHECK ((threshold > (0)::numeric))
);

CREATE TABLE IF NOT EXISTS qq_bot_balance_subscriptions (
    app_id text NOT NULL,
    member_id text NOT NULL,
    user_id bigint NOT NULL,
    binding_created_at timestamp with time zone NOT NULL,
    revision text NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    verified_private_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

DO $kitty_schema$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='qq_bot_balance_alerts'::regclass AND conname='qq_bot_balance_alerts_pkey') THEN
  ALTER TABLE ONLY qq_bot_balance_alerts
    ADD CONSTRAINT qq_bot_balance_alerts_pkey PRIMARY KEY (id);
 END IF;
END $kitty_schema$;

DO $kitty_schema$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='qq_bot_balance_subscriptions'::regclass AND conname='qq_bot_balance_subscriptions_pkey') THEN
  ALTER TABLE ONLY qq_bot_balance_subscriptions
    ADD CONSTRAINT qq_bot_balance_subscriptions_pkey PRIMARY KEY (app_id, member_id);
 END IF;
END $kitty_schema$;

DO $kitty_schema$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='qq_bot_balance_alerts'::regclass AND conname='qq_bot_balance_alerts_app_id_member_id_fkey') THEN
  ALTER TABLE ONLY qq_bot_balance_alerts
    ADD CONSTRAINT qq_bot_balance_alerts_app_id_member_id_fkey FOREIGN KEY (app_id, member_id) REFERENCES qq_bot_balance_subscriptions(app_id, member_id) ON DELETE CASCADE;
 END IF;
END $kitty_schema$;

DO $kitty_schema$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='qq_bot_balance_alerts'::regclass AND conname='qq_bot_balance_alerts_user_id_fkey') THEN
  ALTER TABLE ONLY qq_bot_balance_alerts
    ADD CONSTRAINT qq_bot_balance_alerts_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
 END IF;
END $kitty_schema$;

DO $kitty_schema$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='qq_bot_balance_subscriptions'::regclass AND conname='qq_bot_balance_subscriptions_app_id_member_id_fkey') THEN
  ALTER TABLE ONLY qq_bot_balance_subscriptions
    ADD CONSTRAINT qq_bot_balance_subscriptions_app_id_member_id_fkey FOREIGN KEY (app_id, member_id) REFERENCES qq_bot_bindings(app_id, member_id) ON DELETE CASCADE;
 END IF;
END $kitty_schema$;

DO $kitty_schema$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='qq_bot_balance_subscriptions'::regclass AND conname='qq_bot_balance_subscriptions_user_id_fkey') THEN
  ALTER TABLE ONLY qq_bot_balance_subscriptions
    ADD CONSTRAINT qq_bot_balance_subscriptions_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
 END IF;
END $kitty_schema$;

CREATE UNIQUE INDEX IF NOT EXISTS qq_bot_balance_alerts_pending_member ON qq_bot_balance_alerts USING btree (app_id, member_id) WHERE (status = ANY (ARRAY['pending'::text, 'leased'::text]));

CREATE INDEX IF NOT EXISTS qq_bot_balance_alerts_queue ON qq_bot_balance_alerts USING btree (next_attempt_at, created_at) WHERE (status = ANY (ARRAY['pending'::text, 'leased'::text]));
