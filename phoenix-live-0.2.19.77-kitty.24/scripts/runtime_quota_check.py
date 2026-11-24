"""Read-only confirmation of live background quota sampling after upgrade."""
import datetime as dt,json,subprocess
from remote_common import PG,APP,query,inspect,rel_dir
app=inspect(APP);started=app['State']['StartedAt']
sql="""SELECT json_build_object(
'active_openai_oauth',(SELECT count(*) FROM accounts WHERE platform='openai' AND type='oauth' AND deleted_at IS NULL AND status='active'),
'sampled_since_start',(SELECT count(*) FROM accounts WHERE platform='openai' AND type='oauth' AND deleted_at IS NULL AND (extra->'codex_credits_snapshot'->>'fetched_at')::bigint>=EXTRACT(EPOCH FROM TIMESTAMPTZ '"""+started+"""')),
'recent_samples',(SELECT coalesce(json_agg(json_build_object('id',id,'sampled',extra ? 'codex_credits_snapshot','reset_sampled',extra ? 'codex_reset_credit_snapshot','sample_at',extra->'codex_credits_snapshot'->'fetched_at') ORDER BY id),'[]') FROM accounts WHERE platform='openai' AND type='oauth' AND deleted_at IS NULL AND status='active'))"""
out=json.loads(query(PG,sql))
logs=subprocess.run(['docker','logs','--since',started,APP],capture_output=True,text=True,timeout=45)
assert logs.returncode==0
lines=(logs.stdout+logs.stderr).splitlines()
out.update(worker_scan_failures=sum('openai_quota_refresh_scan_failed' in x for x in lines),worker_account_warnings=sum('openai_quota_refresh_account_failed' in x for x in lines),startup_panic=any('panic:' in x.lower() for x in lines),started_at=started,utc=dt.datetime.now(dt.timezone.utc).isoformat())
out['passed']=not out['startup_panic'] and out['worker_scan_failures']==0 and (out['active_openai_oauth']==0 or out['sampled_since_start']>0)
(rel_dir()/'runtime-quota.public.json').write_text(json.dumps(out,indent=2));print(json.dumps(out))
