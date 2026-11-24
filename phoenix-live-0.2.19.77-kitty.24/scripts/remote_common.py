"""Shared helpers for CS release steps on Phoenix. Never prints credentials or setting values."""
import base64,hashlib,hmac,json,os,secrets,subprocess,time,urllib.error,urllib.request
from pathlib import Path

ROOT = Path('/opt/kitty-cs-zuchengchen-20261120')
APP, PG, REDIS = 'kitty-cs-czc-app', 'kitty-final-migration-target', 'kitty-cs-czc-redis'
PROJECT = 'kitty-cs-czc'
PRESERVE = [PG, REDIS, 'kitty-subcustom-app', 'kitty-old-archive-app', 'kitty-prod-public-caddy']
DATA_SQL = ("SELECT json_build_object('users',(SELECT count(*) FROM users),"
            "'balances',(SELECT md5(coalesce(string_agg(id::text||':'||balance::text,',' ORDER BY id),'')) FROM users),"
            "'user_identity_status',(SELECT md5(coalesce(string_agg(id::text||':'||email||':'||password_hash||':'||role||':'||status||':'||concurrency::text||':'||coalesce(deleted_at::text,''),',' ORDER BY id),'')) FROM users),"
            "'codes',(SELECT count(*) FROM redeem_codes),'orders',(SELECT count(*) FROM payment_orders),"
            "'accounts',(SELECT count(*) FROM accounts),'keys',(SELECT count(*) FROM api_keys),"
            "'key_ownership_status',(SELECT md5(coalesce(string_agg(id::text||':'||user_id::text||':'||coalesce(group_id::text,'')||':'||status||':'||coalesce(deleted_at::text,'')||':'||quota::text||':'||quota_used::text,',' ORDER BY id),'')) FROM api_keys),"
            "'key_groups',(SELECT md5(coalesce(string_agg(api_key_id::text||':'||group_id::text,',' ORDER BY api_key_id,group_id),'')) FROM api_key_groups),"
            "'groups',(SELECT count(*) FROM groups),'usage_logs',(SELECT count(*) FROM usage_logs))")
SETTINGS_SQL = "SELECT coalesce(json_object_agg(key,md5(value)),'{}') FROM settings"


def rel_dir():
    return Path(__file__).resolve().parent


def config():
    return json.loads((rel_dir() / 'release.json').read_text())


def run(args, timeout=180, **kw):
    p = subprocess.run(args, stdout=kw.pop('stdout', subprocess.PIPE), stderr=subprocess.PIPE, timeout=timeout, **kw)
    if p.returncode:
        # Keep only the tail of stderr in a private file for diagnosis; never echo it.
        (Path(__file__).resolve().parent / 'last-command-error.private.log').write_bytes((p.stderr or b'')[-4000:])
        raise RuntimeError('command failed: ' + str(args[:3]) + ' rc=' + str(p.returncode))
    return (p.stdout or b'').decode().strip()


def inspect(name):
    return json.loads(run(['docker', 'inspect', name]))[0]


def environment(item):
    """Resolved container environment, including compose overrides; never log it."""
    return dict(x.split('=',1) for x in item['Config']['Env'] if '=' in x)


def runtime():
    app=inspect(APP); e=environment(app)
    assert e.get('DATABASE_HOST') == PG, 'unexpected production database host'
    assert e.get('DATABASE_USER') == e.get('DATABASE_DBNAME') == 'cs_czc', 'unexpected database identity'
    redis=inspect(REDIS)
    redis_aliases={REDIS}
    for net,item in redis['NetworkSettings']['Networks'].items():
        if net in app['NetworkSettings']['Networks']:
            redis_aliases.update(item.get('Aliases') or [])
    assert e.get('REDIS_HOST') in redis_aliases, 'unexpected production Redis'
    assert e.get('TOTP_ENCRYPTION_KEY'), 'missing encryption key'
    labels=app['Config']['Labels']
    assert labels.get('com.docker.compose.project') == PROJECT
    assert labels.get('com.docker.compose.service') == 'app'
    assert labels.get('com.docker.compose.project.working_dir') == str(ROOT)
    mounts=[Path(x['Source']) for x in app['Mounts'] if x.get('Destination')=='/app/data']
    assert len(mounts)==1 and mounts[0].is_dir(), 'application data mount missing'
    return {'app':app,'env':e,'pg':inspect(e['DATABASE_HOST']),'data':mounts[0]}


def assert_preserved(before):
    """Intentionally stopped old production remains stopped, not healthy."""
    for name,old in before.items():
        now=inspect(name)
        assert now['Id']==old['Id'], 'unexpected container replacement: '+name
        assert now['State']['Running']==old['State']['Running'], 'unexpected container state: '+name
        if old['State']['Running']:
            assert healthy(now), 'unhealthy preserved container: '+name


def healthy(c):
    return c['State']['Running'] and c['State'].get('Health', {}).get('Status', 'healthy') == 'healthy'


def psql(container):
    return ['docker','exec','-i','-e','PGOPTIONS=-c timezone=UTC',container,'psql','-XqAt','-v','ON_ERROR_STOP=1','-U','cs_czc','-d','cs_czc']


def query(container, sql, snapshot=None):
    if snapshot:
        assert all(c in '0123456789ABCDEF-' for c in snapshot), 'invalid PG snapshot'
        sql="BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SET TRANSACTION SNAPSHOT '"+snapshot+"'; "+sql+'; COMMIT;'
    return run(psql(container),input=sql.encode(),timeout=600)


def consistent_dump(container, path):
    """Dump and comparison hashes from one exported MVCC snapshot, not moving live totals."""
    hold=subprocess.Popen(psql(container),stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    try:
        hold.stdin.write('BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SELECT pg_export_snapshot();\n');hold.stdin.flush()
        snap=hold.stdout.readline().strip()
        assert snap and all(c in '0123456789ABCDEF-' for c in snap),'snapshot export failed'
        expected={'business':json.loads(query(container,DATA_SQL,snap)), 'settings':json.loads(query(container,SETTINGS_SQL,snap))}
        with path.open('xb') as f:
            run(['docker','exec',container,'pg_dump','-Fc','--no-owner','--no-acl','--snapshot='+snap,'-U','cs_czc','-d','cs_czc'],stdout=f,timeout=3600)
        return expected
    finally:
        if hold.poll() is None:
            try:hold.stdin.write('ROLLBACK;\n');hold.stdin.close();hold.wait(timeout=15)
            except (OSError,subprocess.TimeoutExpired):hold.kill();hold.wait(timeout=15)


def http_client(base):
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def req(path, body=None, token=None, method=None):
        headers = {'Content-Type': 'application/json','Host':'kittyapi.xyz'}
        if token:
            headers['Authorization'] = 'Bearer ' + token
        q = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers=headers, method=method)
        try:
            with opener.open(q, timeout=25) as r:
                return r.status, r.read()
        except urllib.error.HTTPError as e:
            return e.code, e.read()
    return req


def admin_token(req, container=PG, app=APP):
    """Server-local short-lived acceptance token from actual migrated admin identity.

    No old admin credentials file, password reset or persistent session is used.
    The JWT remains in process memory and is never included in receipts.
    """
    users=json.loads(query(container,"SELECT coalesce(json_agg(json_build_array(id,email,role,password_hash)),'[]') FROM(SELECT * FROM users WHERE deleted_at IS NULL AND status='active' AND role='admin' ORDER BY id LIMIT 1)u"))
    assert users,'no active migrated administrator'
    uid,email,role,pw=users[0]
    key=query(container,"SELECT value FROM security_secrets WHERE key='jwt_secret'")
    assert key,'runtime JWT secret not persisted'
    version=int.from_bytes(hashlib.sha256((email.strip().lower()+'\n'+pw).encode()).digest()[:8],'big')&0x7fffffffffffffff
    now=int(time.time());payload={'user_id':uid,'email':email,'role':role,'token_version':version,'iat':now,'nbf':now,'exp':now+600}
    enc=lambda v:base64.urlsafe_b64encode(json.dumps(v,separators=(',',':')).encode()).rstrip(b'=')
    body=enc({'alg':'HS256','typ':'JWT'})+b'.'+enc(payload)
    value=(body+b'.'+base64.urlsafe_b64encode(hmac.new(key.encode(),body,hashlib.sha256).digest()).rstrip(b'=')).decode()
    code,_=req('/api/v1/auth/me',token=value)
    assert code==200,'migrated admin token did not authenticate'
    return value


def guard_rehearsal_settings(container):
    """Temporarily guard consumers/retention; return exact values for restoration."""
    rows=json.loads(query(container,"SELECT coalesce(json_agg(json_build_array(key,value,updated_at)),'[]') FROM settings WHERE key IN ('kitty_qqbot_connection_enabled','kitty_account_intelligence_schedules_enabled','kitty_scheduled_tests_enabled','ops_runtime_log_config')"))
    query(container,"INSERT INTO settings(key,value,updated_at) SELECT x,'false',NOW() FROM unnest(ARRAY['kitty_qqbot_connection_enabled','kitty_account_intelligence_schedules_enabled','kitty_scheduled_tests_enabled']) x ON CONFLICT(key) DO UPDATE SET value='false'")
    # Retention is otherwise allowed to remove old logs during startup, creating
    # a false migration mismatch. Restore the exact existing JSON after checks.
    query(container,"UPDATE settings SET value=(value::jsonb || jsonb_build_object('request_retention_days',0))::text WHERE key='ops_runtime_log_config'")
    return rows


def restore_consumer_settings(container,rows):
    raw=base64.b64encode(json.dumps(rows).encode()).decode()
    query(container,"""BEGIN;
DELETE FROM settings WHERE key IN ('kitty_qqbot_connection_enabled','kitty_account_intelligence_schedules_enabled','kitty_scheduled_tests_enabled','ops_runtime_log_config');
INSERT INTO settings(key,value,updated_at) SELECT x->>0,x->>1,(x->>2)::timestamptz
FROM jsonb_array_elements(convert_from(decode('"""+raw+"""','base64'),'UTF8')::jsonb)x;
COMMIT;""")


def base_checks(req, token, version):
    """Endpoints every release must keep working. Admin APIs are skipped while the compliance lock is on."""
    checks = []

    def expect(path, code_expected, auth=None):
        code, _ = req(path, token=auth)
        checks.append({'path': path, 'authenticated': bool(auth), 'status': code, 'expected': code_expected})
        assert code == code_expected, 'check failed: ' + path + ' got ' + str(code)

    expect('/health', 200)
    code, raw = req('/api/v1/settings/public')
    assert code == 200 and json.loads(raw)['data']['version'] == version, 'version mismatch'
    for p in ['/api/v1/auth/me', '/api/v1/user/profile', '/api/v1/keys', '/api/v1/groups/available', '/api/v1/usage/stats']:
        expect(p, 200, token)
    code, raw = req('/api/v1/admin/compliance', token=token)
    locked = code == 200 and json.loads(raw)['data'].get('required') is True
    if not locked:
        for p in ['/api/v1/admin/accounts', '/api/v1/admin/groups', '/api/v1/admin/users', '/api/v1/admin/dashboard/stats', '/api/v1/admin/settings']:
            expect(p, 200, token)
    for p in ['/api/v1/admin/accounts', '/v1/models']:
        expect(p, 401)
    return checks, locked
