"""Final read-only health and release temporary-Key cleanup check."""
import datetime as dt,json
from remote_common import APP,PG,inspect,query,rel_dir,http_client,admin_token,base_checks,config
app=inspect(APP);cfg=config();ip=next(x['IPAddress'] for x in app['NetworkSettings']['Networks'].values() if x['IPAddress']);req=http_client('http://'+ip+':8080')
checks,locked=base_checks(req,admin_token(req),cfg['version'])
temp=int(query(PG,"SELECT count(*) FROM api_keys WHERE name IN ('Release .24 gateway acceptance','Release .24 WS continuation acceptance') AND (deleted_at IS NULL OR status='active')"))
out={'passed':temp==0 and not locked and app['State']['Health']['Status']=='healthy' and app['RestartCount']==0,'version':cfg['version'],'checks':checks,'unretired_release_keys':temp,'healthy':app['State']['Health']['Status']=='healthy','restart_count':app['RestartCount'],'utc':dt.datetime.now(dt.timezone.utc).isoformat()}
(rel_dir()/'final-release.public.json').write_text(json.dumps(out,indent=2));print(json.dumps(out));assert out['passed']
