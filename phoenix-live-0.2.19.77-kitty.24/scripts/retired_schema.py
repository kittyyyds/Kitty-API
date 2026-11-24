"""Rollback only explicitly retired module tables/settings, never customer business tables.

The release manifest must enumerate the migration, tables and setting keys/prefixes.
Snapshots are private and live only on the server. Old app rollback is rehearsed.
"""
import base64,json,re
from pathlib import Path
from remote_common import query,run,psql,SETTINGS_SQL

TABLE_PREFIXES=('content_moderation_','prompt_audit_','security_penalty_','security_policy_','cyber_policy_','unified_risk_')
TABLE_EXACT={'usage_policy_violations'}
KEY_PREFIXES=('content_moderation_','prompt_audit_','security_penalty_','security_policy_','cyber_','risk_control_')
KEY_PATTERNS=('content_moderation_%','prompt_audit_%','security_penalty_%','risk_control_%','cyber_session_%')

def spec(cfg):
    value=cfg.get('retired_schema',{})
    if not value:return {}
    assert re.fullmatch(r'9[0-9]{2}_kitty_[A-Za-z0-9_]+\.sql',value['migration'])
    assert value['migration'] in cfg.get('new_migrations',[])
    assert isinstance(value.get('tables'),list), 'enumerate actual retired tables, possibly empty'
    for t in value['tables']:
        assert re.fullmatch(r'[a-z][a-z0-9_]+',t) and (t.startswith(TABLE_PREFIXES) or t in TABLE_EXACT), 'unsafe retirement table'
    for key in value.get('setting_keys',[]):
        assert key.startswith(KEY_PREFIXES), 'unsafe retirement setting key'
    for prefix in value.get('setting_prefixes',[]):
        assert prefix in KEY_PREFIXES or prefix in ('cyber_session_',), 'unsafe retirement setting prefix'
    for pattern in value.get('setting_patterns',[]):
        assert pattern in KEY_PATTERNS, 'unsafe retirement setting pattern'
    assert not value.get('group_policy_flags') or value['migration']=='952_kitty_retire_local_policy_runtime.sql'
    return value

def enabled(cfg):return bool(spec(cfg))

def setting_condition(cfg):
    s=spec(cfg)
    clauses=["key='"+k+"'" for k in s.get('setting_keys',[])]
    clauses += ["starts_with(key,'"+k+"')" for k in s.get('setting_prefixes',[])]
    clauses += ["key LIKE '"+k+"'" for k in s.get('setting_patterns',[])]
    return '('+' OR '.join(clauses)+')' if clauses else 'false'

def filtered_settings_sql(cfg,base=SETTINGS_SQL):
    if not enabled(cfg):return base
    assert base.rstrip().endswith('FROM settings')
    return base.rstrip()+' WHERE NOT '+setting_condition(cfg)

def snapshot(cfg,db,folder):
    s=spec(cfg)
    if not s:return
    folder=Path(folder);folder.mkdir(mode=0o700,exist_ok=True)
    assert query(db,"SELECT count(*) FROM schema_migrations WHERE filename='"+s['migration']+"'")=='0','retirement already applied'
    known=json.loads(query(db,"SELECT coalesce(json_agg(tablename),'[]') FROM pg_tables WHERE schemaname='public'"))
    tables=[t for t in s['tables'] if t in known]
    if tables:
        references=json.loads(query(db,"SELECT coalesce(json_agg(DISTINCT cl.relname),'[]') FROM pg_constraint c JOIN pg_class cl ON cl.oid=c.conrelid JOIN pg_class ref ON ref.oid=c.confrelid WHERE c.contype='f' AND ref.relname IN("+','.join("'"+t+"'" for t in tables)+")"))
        assert set(references)<=set(tables),'retired schema has foreign keys from nonretired business tables'
    selected=['public.'+t for t in tables]
    sequences=json.loads(query(db,"SELECT coalesce(json_agg(DISTINCT seq.relname),'[]') FROM pg_depend d JOIN pg_class seq ON seq.oid=d.objid JOIN pg_class tab ON tab.oid=d.refobjid JOIN pg_namespace n ON n.oid=tab.relnamespace WHERE seq.relkind='S' AND n.nspname='public' AND tab.relname IN("+','.join("'"+t+"'" for t in tables)+")")) if tables else []
    selected += ['public.'+x for x in sequences]
    if selected:
        args=['docker','exec',db,'pg_dump','-Fc','--no-owner','--no-acl','-U','cs_czc','-d','cs_czc']
        for t in selected:args += ['-t',t]
        with (folder/'retired-module.private.pgcustom').open('xb') as f:run(args,stdout=f,timeout=1200)
    settings=query(db,"SELECT coalesce(json_agg(json_build_object('key',key,'value',value,'updated_at',updated_at)),'[]') FROM settings WHERE "+setting_condition(cfg))
    (folder/'retired-module-settings.private.json').write_text(settings)
    if s.get('group_policy_flags'):
        flags=query(db,"SELECT coalesce(json_agg(json_build_object('id',id,'created_at',created_at,'security_policy_enabled',security_policy_enabled,'security_policy_email_enabled',security_policy_email_enabled)),'[]') FROM groups WHERE security_policy_enabled OR security_policy_email_enabled")
        (folder/'retired-group-flags.private.json').write_text(flags)
    (folder/'retired-module.private.json').write_text(json.dumps({'migration':s['migration'],'tables':tables,'sequences':sequences}))

def recover(cfg,db,folder):
    s=spec(cfg)
    if not s:return
    if query(db,"SELECT count(*) FROM schema_migrations WHERE filename='"+s['migration']+"'")=='0':return
    folder=Path(folder);m=json.loads((folder/'retired-module.private.json').read_text())
    assert m['migration']==s['migration'] and set(m['tables'])<=set(s['tables'])
    current=json.loads(query(db,"SELECT coalesce(json_agg(tablename),'[]') FROM pg_tables WHERE schemaname='public'"))
    assert not set(m['tables'])&set(current), 'retired tables partially present; inspect before recovery'
    if m['tables']:
        with (folder/'retired-module.private.pgcustom').open('rb') as f:
            run(['docker','exec','-i',db,'pg_restore','--no-owner','--no-acl','--exit-on-error','--single-transaction','-U','cs_czc','-d','cs_czc'],stdin=f,timeout=1200)
    payload=base64.b64encode((folder/'retired-module-settings.private.json').read_bytes()).decode()
    group_restore=''
    if s.get('group_policy_flags'):
        groups=base64.b64encode((folder/'retired-group-flags.private.json').read_bytes()).decode()
        group_restore="""UPDATE groups g SET security_policy_enabled=x.security_policy_enabled,security_policy_email_enabled=x.security_policy_email_enabled
FROM jsonb_to_recordset(convert_from(decode('%s','base64'),'UTF8')::jsonb) x(id bigint,created_at timestamptz,security_policy_enabled boolean,security_policy_email_enabled boolean)
WHERE g.id=x.id AND g.created_at=x.created_at AND NOT g.security_policy_enabled AND NOT g.security_policy_email_enabled;"""%groups
    statement="""BEGIN;
INSERT INTO settings(key,value,updated_at)
SELECT x.key,x.value,x.updated_at FROM jsonb_to_recordset(convert_from(decode('%s','base64'),'UTF8')::jsonb) x(key text,value text,updated_at timestamptz)
ON CONFLICT(key) DO NOTHING;
%s
DELETE FROM schema_migrations WHERE filename='%s';
COMMIT;"""%(payload,group_restore,s['migration'])
    run(psql(db),input=statement.encode(),timeout=120)

def assert_applied(cfg,db):
    s=spec(cfg)
    if not s:return
    assert query(db,"SELECT count(*) FROM schema_migrations WHERE filename='"+s['migration']+"'")=='1'
    for t in s['tables']:
        assert query(db,"SELECT to_regclass('public."+t+"') IS NULL")=='t','retired table still present: '+t
    assert query(db,'SELECT count(*) FROM settings WHERE '+setting_condition(cfg))=='0','retired settings remain'
    if s.get('group_policy_flags'):
        assert query(db,'SELECT count(*) FROM groups WHERE security_policy_enabled OR security_policy_email_enabled')=='0','group policy flags remain'

