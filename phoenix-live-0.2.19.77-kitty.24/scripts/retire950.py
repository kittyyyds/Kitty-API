"""Version-specific release guard. Snapshots stay in a private server directory."""
import base64
import json
import retired_schema
from remote_common import query, run, SETTINGS_SQL

MIGRATION = '950_retire_membership_and_risk_center.sql'
KEYS = {'risk_control_enabled', 'usage_policy_config'}


def enabled(cfg):
    return MIGRATION in cfg.get('new_migrations', [])


def settings_sql(cfg):
    if not enabled(cfg) and not cfg.get('compare_iq_schedule_settings') and cfg.get('version') != '0.2.19.68-kitty.17':
        return retired_schema.filtered_settings_sql(cfg,SETTINGS_SQL)
    # An active IQ schedule may run as soon as the server starts. Its schedule,
    # revision, model and all user-configured fields must still compare exactly.
    return retired_schema.filtered_settings_sql(cfg,"""SELECT coalesce(json_object_agg(key,md5(CASE WHEN key ~ '^account_intelligence_schedule:[0-9]+$'
    THEN (value::jsonb - 'next_run_at' - 'last_attempt_at' - 'last_error')::text ELSE value END)),'{}') FROM settings""")


def check_settings(cfg, before, after):
    if enabled(cfg):
        expected = {k: v for k, v in before.items() if k not in KEYS}
        changed = {k for k in expected.keys() | after.keys() if k not in expected or k not in after or expected[k] != after[k]}
    else:
        changed = {k for k in before.keys() | after.keys() if before.get(k) != after.get(k)}
    if changed:
        raise RuntimeError('Unexpected settings difference: ' + ','.join(sorted(changed)))


SNAPSHOT = """SELECT json_build_object('users',COALESCE((SELECT json_agg(row_to_json(x)) FROM
 (SELECT id,created_at,vip,usage_policy_unban_at FROM users WHERE vip OR usage_policy_unban_at IS NOT NULL) x),'[]'),
 'settings',COALESCE((SELECT json_agg(row_to_json(x)) FROM (SELECT key,value,updated_at FROM settings
 WHERE key IN ('risk_control_enabled','usage_policy_config')) x),'[]'))"""


def snapshot(cfg, db, folder):
    retired_schema.snapshot(cfg,db,folder)
    if not enabled(cfg):
        return
    payload = query(db, SNAPSHOT)
    assert set(json.loads(payload)) == {'users', 'settings'}
    with (folder / 'retired-fields.private.json').open('x', encoding='utf8') as f:
        f.write(payload)


def recovery_sql(payload):
    # Restore retired columns only. Leave newer statuses, balances and orders intact.
    raw = base64.b64encode(payload.encode()).decode()
    return """BEGIN;
SET LOCAL lock_timeout='15s';
LOCK TABLE users,settings,schema_migrations IN SHARE ROW EXCLUSIVE MODE;
ALTER TABLE users ADD COLUMN vip boolean NOT NULL DEFAULT false, ADD COLUMN usage_policy_unban_at timestamptz;
CREATE TEMP TABLE old950_snapshot ON COMMIT DROP AS SELECT convert_from(decode('%s','base64'),'UTF8')::jsonb AS obj;
UPDATE users u SET vip=b.vip, usage_policy_unban_at=CASE WHEN u.status='disabled' AND b.usage_policy_unban_at > NOW() THEN b.usage_policy_unban_at END
FROM old950_snapshot s, jsonb_to_recordset(s.obj->'users') b(id bigint,created_at timestamptz,vip boolean,usage_policy_unban_at timestamptz)
WHERE u.id=b.id AND u.created_at=b.created_at;
INSERT INTO settings(key,value,updated_at) SELECT b.key,b.value,b.updated_at FROM old950_snapshot s,
 jsonb_to_recordset(s.obj->'settings') b(key text,value text,updated_at timestamptz)
WHERE b.key IN ('risk_control_enabled','usage_policy_config') ON CONFLICT(key) DO NOTHING;
CREATE INDEX idx_users_usage_policy_unban_at ON users(usage_policy_unban_at) WHERE deleted_at IS NULL AND usage_policy_unban_at IS NOT NULL;
CREATE INDEX idx_users_vip_pending_upgrade ON users(balance) WHERE deleted_at IS NULL AND vip=false AND balance>=100;
DELETE FROM schema_migrations WHERE filename='950_retire_membership_and_risk_center.sql';
COMMIT;
""" % raw


def recover(cfg, db, folder):
    retired_schema.recover(cfg,db,folder)
    if not enabled(cfg) or query(db, "SELECT count(*) FROM schema_migrations WHERE filename='" + MIGRATION + "'") == '0':
        return
    sql = recovery_sql((folder / 'retired-fields.private.json').read_text(encoding='utf8'))
    run(['docker','exec','-i',db,'psql','-Xq','-v','ON_ERROR_STOP=1','-U','cs_czc','-d','cs_czc'],input=sql.encode(),timeout=300)
