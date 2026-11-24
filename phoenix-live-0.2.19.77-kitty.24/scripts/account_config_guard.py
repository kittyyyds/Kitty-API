"""Compare stable routing and billing configuration; quota/token samples may move."""
import hashlib,json,sys
from remote_common import query,PG,rel_dir
path=rel_dir()/'account-config-before.public.json'
columns=['id','name','platform','type','proxy_id','concurrency','priority','schedulable','rate_multiplier','cost_rate_multiplier','created_at','deleted_at']
available=set(query(PG,"SELECT column_name FROM information_schema.columns WHERE table_name='accounts'").splitlines())
selected=[x for x in columns if x in available]
selection=','.join('a.'+x for x in selected)
sql="SELECT coalesce(json_agg(row_to_json(x) ORDER BY id),'[]') FROM(SELECT "+selection+" FROM accounts a)x"
raw=query(PG,sql)
groups=query(PG,"SELECT coalesce(json_agg(row_to_json(x) ORDER BY account_id,group_id),'[]') FROM(SELECT account_id,group_id,priority FROM account_groups)x") if 'priority' in query(PG,"SELECT column_name FROM information_schema.columns WHERE table_name='account_groups'").splitlines() else query(PG,"SELECT coalesce(json_agg(row_to_json(x) ORDER BY account_id,group_id),'[]') FROM(SELECT account_id,group_id FROM account_groups)x")
values={'account_rows':len(json.loads(raw)),'stable_columns':selected,'account_config_sha256':hashlib.sha256(raw.encode()).hexdigest(),'group_assignments_sha256':hashlib.sha256(groups.encode()).hexdigest(),'group_assignments':len(json.loads(groups))}
if sys.argv[1]=='before':
    assert not path.exists(),'baseline already saved'
    path.write_text(json.dumps(values,indent=2));print(json.dumps(values))
else:
    expected=json.loads(path.read_text());changed=[x for x in values if expected.get(x)!=values[x]]
    result={'passed':not changed,'changed':changed,**values};(rel_dir()/'account-config-after.public.json').write_text(json.dumps(result,indent=2));print(json.dumps(result));assert not changed
