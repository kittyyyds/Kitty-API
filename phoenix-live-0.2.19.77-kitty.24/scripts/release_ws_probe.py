"""Execute one WS -> HTTP live continuation without exposing its temporary Key."""
import hashlib,json,os,secrets,subprocess,uuid,sys
from remote_common import query,PG,rel_dir
def main():
    assert json.loads((rel_dir()/'cutover.public.json').read_text())['passed']
    uid=query(PG,"SELECT id FROM users WHERE role='admin' AND status='active' AND deleted_at IS NULL ORDER BY balance DESC,id LIMIT 1")
    gid=int(sys.argv[1]) if len(sys.argv)>1 else 72;assert gid in (4,72)
    key='sk-release-ws-check-'+secrets.token_hex(24);digest='$sha256$'+hashlib.sha256(key.encode()).hexdigest()+'$release-ws-check'
    kid=query(PG,"INSERT INTO api_keys(user_id,name,key,group_id,status,quota) VALUES("+uid+",'Release .24 WS continuation acceptance','"+digest+"',"+str(gid)+",'active',0.2) RETURNING id")
    try:
        executable=rel_dir()/'ws-probe';executable.chmod(0o700)
        result=subprocess.run([str(executable)],env={**os.environ,'KITTY_RELEASE_PROBE_KEY':key,'KITTY_RELEASE_PROBE_SESSION':str(uuid.uuid4()),'KITTY_RELEASE_PROBE_GROUP':str(gid)},capture_output=True,text=True,timeout=130)
        assert result.returncode==0,'WS probe process failed'
        out=json.loads(result.stdout);out['only_release_key_created_and_disabled']=True
        (rel_dir()/('ws-continuation-group'+str(gid)+'.public.json')).write_text(json.dumps(out,indent=2));print(json.dumps(out))
    finally:query(PG,"UPDATE api_keys SET status='inactive',deleted_at=NOW() WHERE id="+kid)
if __name__=='__main__':main()
