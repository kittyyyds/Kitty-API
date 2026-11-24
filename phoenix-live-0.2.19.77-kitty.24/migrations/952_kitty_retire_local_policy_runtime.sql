-- Local moderation and automatic account/key penalties are retired. Historical
-- evidence remains available in private release backups. No users,
-- keys, balances, orders or usage records are changed by this migration.
DELETE FROM settings
WHERE key LIKE 'content_moderation_%'
   OR key LIKE 'prompt_audit_%'
   OR key LIKE 'security_penalty_%'
   OR key LIKE 'risk_control_%'
   OR key LIKE 'cyber_session_%'
   OR key = 'cyber_policy_user_allowlist';

-- Persisted group switches must not re-enable a retired runtime on re-import.
UPDATE groups
SET security_policy_enabled = false,
    security_policy_email_enabled = false
WHERE security_policy_enabled OR security_policy_email_enabled;

-- Remove only the retired local policy module, without CASCADE. A foreign key
-- from any unrelated business table must stop this migration rather than drop it.
DROP TABLE IF EXISTS
    content_moderation_archive_access_audits,
    content_moderation_log_chunks,
    content_moderation_retry_operations,
    content_moderation_logs,
    prompt_audit_events,
    prompt_audit_jobs,
    security_policy_keywords,
    unified_risk_migration_audits,
    unified_risk_migration_stage,
    unified_risk_migration_state,
    usage_policy_violations;
