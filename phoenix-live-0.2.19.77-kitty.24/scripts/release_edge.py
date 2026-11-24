"""Maintenance only the current main domain; preserve archived domain and Caddy inode."""
import hashlib,json,os,re,time
from pathlib import Path
from remote_common import run,runtime,environment,inspect,REDIS

LIVE=Path('/opt/codex-migration/20261019/public-caddy-production/Caddyfile')
CADDY='kitty-prod-public-caddy'
SITE='https://kittyapi.xyz'

def site_span(text):
    matches=list(re.finditer(r'^'+re.escape(SITE)+r'\s*\{\s*$',text,re.M))
    assert len(matches)==1,'main site ambiguous'
    start=matches[0].start();opening=text.index('{',start);depth=0;quote=None;comment=False;escape=False
    for i in range(opening,len(text)):
        ch=text[i]
        if comment:
            if ch=='\n':comment=False
            continue
        if quote:
            if escape:escape=False;continue
            if ch=='\\' and quote!='`':escape=True;continue
            if ch==quote:quote=None
            continue
        if ch=='#':comment=True;continue
        if ch in ('"',"'",'`'):quote=ch;continue
        if ch=='{':depth+=1
        elif ch=='}':
            depth-=1
            if depth==0:return start,i+1
    raise ValueError('unbalanced main site')

def candidate(raw):
    text=raw.decode();a,b=site_span(text)
    return (text[:a]+SITE+''' {
    header Cache-Control "no-store"
    header Retry-After "30"
    header Content-Type "application/json; charset=utf-8"
    respond `{"error":{"type":"site_maintenance","message":"KittyAPI 正在更新，请稍后重试。"}}` 503
}'''+text[b:]).encode()

def apply(raw):
    run(['docker','exec','-i',CADDY,'caddy','validate','--config','-','--adapter','caddyfile'],input=raw,timeout=60)
    old=LIVE.read_bytes()
    def write(blob):
        with LIVE.open('r+b') as f:f.seek(0);f.write(blob);f.truncate();f.flush();os.fsync(f.fileno())
    try:
        write(raw);run(['docker','exec','-i',CADDY,'caddy','reload','--config','-','--adapter','caddyfile'],input=raw,timeout=60)
    except BaseException:
        write(old);run(['docker','exec','-i',CADDY,'caddy','reload','--config','-','--adapter','caddyfile'],input=old,timeout=60);raise

def enter(folder):
    folder=Path(folder);raw=LIVE.read_bytes();new=candidate(raw)
    (folder/'Caddyfile.before.private').write_bytes(raw)
    state={'original':hashlib.sha256(raw).hexdigest(),'maintenance':hashlib.sha256(new).hexdigest()}
    apply(new);(folder/'edge-state.public.json').write_text(json.dumps(state));return state

def leave(folder):
    folder=Path(folder);state=json.loads((folder/'edge-state.public.json').read_text())
    assert hashlib.sha256(LIVE.read_bytes()).hexdigest()==state['maintenance'],'Caddy changed during release; inspect before restoring'
    original=(folder/'Caddyfile.before.private').read_bytes();assert hashlib.sha256(original).hexdigest()==state['original']
    apply(original)

DRAIN_LUA="""local now=tonumber(redis.call('TIME')[1]);local out={0,0,0};
for i,pat in ipairs({'concurrency:account:*','concurrency:user:*','concurrency:live:account:*','concurrency:live:user:*','concurrency:wait:*','wait:account:*'}) do
 local cursor='0';repeat local r=redis.call('SCAN',cursor,'MATCH',pat,'COUNT',1000);cursor=r[1];for _,k in ipairs(r[2]) do
 if string.match(k,':%d+$') then
  if i<=4 then local cutoff=now-900;if i>=3 then cutoff=now-60 end;local n=redis.call('ZCOUNT',k,'('..cutoff,'+inf');out[1]=out[1]+n
  else out[2]=out[2]+tonumber(redis.call('GET',k) or '0') end
 end end until cursor=='0'
end;return out"""

def inflight():
    e=runtime()['env'];cmd=['docker','exec','-e','REDISCLI_AUTH',REDIS,'redis-cli','--raw','-n',e.get('REDIS_DB','0'),'EVAL',DRAIN_LUA,'0']
    raw=run(cmd,env={**os.environ,'REDISCLI_AUTH':e['REDIS_PASSWORD']},timeout=30)
    values=raw.splitlines();assert len(values)==3 and all(re.fullmatch(r'-?\d+',x) for x in values),'cannot verify in-flight metrics'
    return {'active_slots':int(values[0]),'waiting':int(values[1])}

def drain(timeout=600):
    start=time.monotonic();seen=[];zeros=0
    while time.monotonic()-start<timeout:
        row=inflight();seen.append({'elapsed':round(time.monotonic()-start),**row})
        zeros=zeros+1 if row['active_slots']==row['waiting']==0 else 0
        if zeros>=3:return {'passed':True,'samples':seen,'idle_ingress_ws_may_reconnect':True}
        time.sleep(5)
    raise RuntimeError('active requests did not drain; keep old application and retry later')
