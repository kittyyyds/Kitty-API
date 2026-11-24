"""Compact read-only rehearsal progress and live OAuth group eligibility."""
import json,subprocess
from remote_common import PG,query,rel_dir,inspect
out={}
for f in ['backup.rc','stage.rc','rehearsal.rc','cutover.rc']:
    p=rel_dir()/f
    if p.exists():out[f]=p.read_text()
p=subprocess.run(['docker','ps','--filter','label=kitty.rehearsal=cs-main-release-rehearsal','--format','{{.Names}} {{.Status}}'],capture_output=True,text=True,timeout=30);out['rehearsal_containers']=p.stdout.splitlines()
if any('kitty-cs-main-rehearsal-pg' in x for x in out['rehearsal_containers']):
    out['restore_activity']=json.loads(query('kitty-cs-main-rehearsal-pg',"SELECT coalesce(json_agg(json_build_object('command',command,'phase',phase,'blocks_total',blocks_total,'blocks_done',blocks_done)),'[]') FROM pg_stat_progress_create_index"))
out['eligible_oauth_groups']=json.loads(query(PG,"SELECT coalesce(json_agg(json_build_object('id',a.id,'active',a.status='active','schedulable',a.schedulable,'groups',(SELECT json_agg(ag.group_id) FROM account_groups ag WHERE ag.account_id=a.id),'models',(SELECT json_agg(k) FROM jsonb_object_keys(coalesce(a.credentials->'model_mapping','{}'))k)) ORDER BY a.id),'[]') FROM accounts a WHERE a.deleted_at IS NULL AND a.platform='openai' AND a.type='oauth'"))
print(json.dumps(out))
