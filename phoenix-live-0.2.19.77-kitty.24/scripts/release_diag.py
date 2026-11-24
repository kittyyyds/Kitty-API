"""Safe read-only state for release preconditions; no credentials or identities."""
import json
from remote_common import query,PG,rel_dir
out={'original_cs_oauth':json.loads(query(PG,"SELECT coalesce(json_agg(json_build_object('id',id,'type',type,'status',status,'deleted',deleted_at IS NOT NULL,'deleted_at',deleted_at,'updated_at',updated_at)),'[]') FROM accounts WHERE id BETWEEN 1000000034 AND 1000000037")),
'audit_tables':query(PG,"SELECT string_agg(table_name,',') FROM information_schema.tables WHERE table_schema='public' AND (table_name LIKE '%audit%' OR table_name LIKE '%operation%')"),
'audit_columns':query(PG,"SELECT string_agg(column_name,',') FROM information_schema.columns WHERE table_name='audit_logs'"),
'oauth_deletion_audit':json.loads(query(PG,"SELECT coalesce(json_agg(json_build_object('id',id,'created_at',created_at,'action',action,'path',path,'method',method,'status_code',status_code,'actor_user_id',actor_user_id) ORDER BY id),'[]') FROM audit_logs WHERE created_at BETWEEN '2026-11-23T10:30:50Z' AND '2026-11-23T10:31:40Z' AND path LIKE '%accounts%' AND method NOT IN ('GET','OPTIONS')"))}
print(json.dumps(out));(rel_dir()/'diagnostic.public.json').write_text(json.dumps(out,indent=2))
