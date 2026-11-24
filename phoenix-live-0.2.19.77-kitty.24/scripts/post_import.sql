-- Run only on the isolated/imported CS database, with the CS app stopped.
-- All historical tables must already be loaded with capture triggers bypassed.
\set ON_ERROR_STOP on
BEGIN;
SET LOCAL timezone='UTC';
SET LOCAL lock_timeout='10s';
SET LOCAL session_replication_role=replica;

-- Account lifetime reporting uses surviving procurement receipts and reachable
-- logs. kitty_permanent_usage has no account_id and cannot restore that link.
TRUNCATE kitty_account_cost_legacy,kitty_account_lifetime_costs;
INSERT INTO kitty_account_cost_legacy
SELECT u.api_key_id,k.request_key,u.account_id,
 COALESCE(u.account_stats_cost,u.total_cost,0)*COALESCE(u.account_rate_multiplier,1),COALESCE(u.actual_cost,0)
FROM usage_logs u
CROSS JOIN LATERAL(SELECT CASE WHEN COALESCE(u.request_id,'')<>'' THEN 'request:'||u.request_id ELSE 'legacy-row:'||u.id::text END AS request_key) k
WHERE NOT EXISTS(SELECT FROM kitty_procurement_receipts r WHERE (r.api_key_id,r.request_key)=(u.api_key_id,k.request_key))
ON CONFLICT(api_key_id,request_key) DO NOTHING;
INSERT INTO kitty_account_lifetime_costs
SELECT account_id,SUM(account_cost),SUM(user_cost) FROM(
 SELECT account_id,COALESCE((measurements->>'account_credits')::numeric,0) AS account_cost,COALESCE(request_valuation,0) AS user_cost FROM kitty_procurement_receipts
 UNION ALL SELECT account_id,account_cost,user_cost FROM kitty_account_cost_legacy
) history GROUP BY account_id;

-- These are derived transport/cache queues owned by the CS runtime.
TRUNCATE scheduler_outbox,auth_cache_invalidation_outbox,
 scheduler_dirty_work,scheduler_dirty_account_sources,
 scheduler_dirty_group_sources,scheduler_dirty_membership_sources,billing_attempt_outbox;
INSERT INTO scheduler_ownership_epoch(singleton,epoch) VALUES(true,2)
 ON CONFLICT(singleton) DO UPDATE SET epoch=scheduler_ownership_epoch.epoch+1;
INSERT INTO scheduler_dirty_work(kind,entity_id,generation,rebuild_buckets) VALUES(3,0,1,true);
INSERT INTO scheduler_dirty_account_sources(account_id,bucket_dirty)
 SELECT id,true FROM accounts WHERE deleted_at IS NULL ON CONFLICT(account_id) DO NOTHING;
INSERT INTO scheduler_dirty_group_sources(group_id)
 SELECT id FROM groups WHERE deleted_at IS NULL ON CONFLICT(group_id) DO NOTHING;
SET LOCAL session_replication_role=origin;
COMMIT;

-- Caller must additionally run import_common.validate_fk_sql(), reset all owned
-- serial sequences, ANALYZE, and compare financial/entity digests to source.
