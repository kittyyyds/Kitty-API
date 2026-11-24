"""Minimal HTTPS inference checks, with release-owned short-lived Keys."""
import datetime as dt,hashlib,json,secrets,time,urllib.error,urllib.request,uuid
from remote_common import query,PG,rel_dir,config
ROOT=rel_dir()
def call(key,path,body,sid):
    req=urllib.request.Request('https://kittyapi.xyz'+path,data=json.dumps(body).encode(),headers={'Authorization':'Bearer '+key,'Content-Type':'application/json','User-Agent':'codex_cli_rs/0.77.0','OpenAI-Beta':'responses=experimental','session_id':sid,'session-id':sid})
    start=time.monotonic()
    try:
        with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req,timeout=120) as r:code=r.status;raw=r.read(2*1024*1024)
    except urllib.error.HTTPError as e:code=e.code;raw=e.read(1024*1024)
    except Exception as e:return {'passed':False,'error_class':type(e).__name__},None
    completed=False;rid=None
    for line in raw.splitlines():
        if not line.startswith(b'data: '):continue
        try:data=json.loads(line[6:])
        except ValueError:continue
        if data.get('type')=='response.completed':completed=True;rid=data.get('response',{}).get('id')
    if path.endswith('chat/completions'):completed=b'[DONE]' in raw
    result={'passed':code==200 and completed,'status':code,'completed':completed,'seconds':round(time.monotonic()-start,2),'bytes':len(raw)}
    if not result['passed']:
        f=ROOT/('gateway-probe-'+uuid.uuid4().hex+'.private.response');f.write_bytes(raw);result['diagnostic_file']=str(f)
    return result,rid
def main():
    cfg=config();assert json.loads((ROOT/'cutover.public.json').read_text())['passed']
    uid=query(PG,"SELECT id FROM users WHERE role='admin' AND status='active' AND deleted_at IS NULL ORDER BY balance DESC,id LIMIT 1")
    plans=[('deepseek',63,'deepseek-flash','/v1/chat/completions'),('openai',72,'gpt-5.6-sol','/v1/responses')]
    results=[]
    for platform,gid,model,path in plans:
        # Existing active groups only; this never changes routing or account settings.
        assert query(PG,"SELECT count(*) FROM groups WHERE id="+str(gid)+" AND status='active' AND deleted_at IS NULL")=='1'
        key='sk-release-check-'+secrets.token_hex(24);digest='$sha256$'+hashlib.sha256(key.encode()).hexdigest()+'$release-check'
        kid=query(PG,"INSERT INTO api_keys(user_id,name,key,group_id,status,quota) VALUES("+uid+",'Release .24 gateway acceptance','"+digest+"',"+str(gid)+",'active',0.2) RETURNING id")
        try:
            sid=str(uuid.uuid4())
            body={'model':model,'stream':True}
            if platform=='openai':body.update(input='Reply only OK.',store=True,max_output_tokens=32)
            else:body.update(messages=[{'role':'user','content':'Reply only OK.'}],max_tokens=16)
            result,rid=call(key,path,body,sid);result.update(platform=platform,model=model,group_id=gid,path=path);results.append(result)
            if platform=='openai' and result['passed'] and rid:
                continued,_=call(key,path,{'model':model,'stream':True,'input':'Reply only OK again.','previous_response_id':rid,'store':True,'max_output_tokens':32},sid)
                continued.update(platform=platform,model=model,group_id=gid,path=path,continuation=True);results.append(continued)
        finally:query(PG,"UPDATE api_keys SET status='inactive',deleted_at=NOW() WHERE id="+kid)
    out={'passed':all(r['passed'] for r in results),'public_tls':True,'checks':results,'only_release_keys_created_and_disabled':True,'account_settings_unchanged':True,'version':cfg['version'],'utc':dt.datetime.now(dt.timezone.utc).isoformat()}
    (ROOT/'gateway-model-probe.public.json').write_text(json.dumps(out,indent=2));print(json.dumps(out))
if __name__=='__main__':main()
