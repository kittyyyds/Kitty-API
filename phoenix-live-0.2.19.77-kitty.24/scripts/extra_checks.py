"""Release checks: customer payment contract, migrated data and retired APIs."""
import base64,hashlib,hmac,json,time
from remote_common import config

def scoped_token(query,uid):
    user=json.loads(query("SELECT json_build_array(id,email,role,password_hash) FROM users WHERE id="+str(uid)+" AND status='active' AND deleted_at IS NULL"))
    assert user
    ident,email,role,password=user
    key=query("SELECT value FROM security_secrets WHERE key='jwt_secret'")
    now=int(time.time());version=int.from_bytes(hashlib.sha256((email.strip().lower()+'\n'+password).encode()).digest()[:8],'big')&0x7fffffffffffffff
    encode=lambda value:base64.urlsafe_b64encode(json.dumps(value,separators=(',',':')).encode()).rstrip(b'=')
    body=encode({'alg':'HS256','typ':'JWT'})+b'.'+encode({'user_id':ident,'email':email,'role':role,'token_version':version,'iat':now,'nbf':now,'exp':now+600})
    return (body+b'.'+base64.urlsafe_b64encode(hmac.new(key.encode(),body,hashlib.sha256).digest()).rstrip(b'=')).decode()

def check(req,admin,query,locked,live=False):
    assert not locked,'existing compliance acceptance must remain valid'
    checks=[]
    for path in ['/api/v1/admin/security-policy/config','/api/v1/admin/security-policy/logs']:
        status,_=req(path,token=admin)
        assert status==404,'retired policy route still registered'
        checks.append({'path':path,'status':status})
    user=scoped_token(query,17)
    for path in ['/api/v1/auth/me','/api/v1/keys','/api/v1/billing/statement','/api/v1/payment/orders/my','/api/v1/payment/invoices/config','/api/v1/user/qqbot/binding','/api/v1/usage/stats']:
        status,raw=req(path,token=user);assert status==200 and json.loads(raw).get('code')==0,path
        checks.append({'path':path,'scope':'customer','status':status})
    status,raw=req('/api/v1/payment/checkout-info',token=user);assert status==200
    data=json.loads(raw)['data'];assert not data.get('preview_only') and not data.get('balance_disabled')
    methods=data['methods'];assert methods and all(m in methods for m in ['alipay','wxpay','dujiaopay'])
    amounts=[5,10,30,50,100,500,1000,5000]
    for amount in amounts:
        assert amount>=data.get('global_min',0)
        assert any(m.get('available') is not False and (m.get('single_min',0)<=amount) and (not m.get('single_max',0) or m['single_max']>=amount) for m in methods.values())
    checks.append({'path':'/api/v1/payment/checkout-info','scope':'customer','status':status,'methods':sorted(methods),'fixed_packages_eligible':amounts,'no_order_created':True})
    original_oauth=json.loads(query("SELECT coalesce(json_agg(json_build_object('id',id,'type',type,'deleted',deleted_at IS NOT NULL) ORDER BY id),'[]') FROM accounts WHERE id BETWEEN 1000000034 AND 1000000037"))
    # The administrator deleted two recovered identities before this release.
    # Preserve those deletions, while proving the upgrade retains all four rows.
    assert original_oauth==[{'id':i,'type':'oauth','deleted':i in (1000000034,1000000036)} for i in range(1000000034,1000000038)],'original CS OAuth row/deletion state changed'
    oauth=sum(not row['deleted'] for row in original_oauth)
    ids=json.loads(query("SELECT coalesce(json_agg(id),'[]') FROM(SELECT id FROM accounts WHERE deleted_at IS NULL AND type='oauth' AND platform='openai' ORDER BY id LIMIT 5)x"))
    for ident in ids:
        path='/api/v1/admin/accounts/'+str(ident)+'/usage?source=passive'
        status,raw=req(path,token=admin);assert status==200 and json.loads(raw).get('code')==0,path
        checks.append({'path':path,'status':status,'local_snapshot_only':True})
    if ids:
        status,raw=req('/api/v1/admin/accounts/usage/batch',body={'account_ids':ids,'source':'passive'},token=admin)
        assert status==200 and json.loads(raw).get('code')==0,'passive batch usage'
        checks.append({'path':'/api/v1/admin/accounts/usage/batch','status':status,'local_snapshot_only':True,'account_count':len(ids)})
    removed=int(query("SELECT count(*) FROM settings WHERE key LIKE 'content_moderation_%' OR key LIKE 'prompt_audit_%' OR key LIKE 'security_penalty_%' OR key LIKE 'risk_control_%' OR key LIKE 'cyber_session_%' OR key='cyber_policy_user_allowlist'"))
    assert removed==0,'retired runtime settings remain'
    return {'passed':True,'checks':checks,'original_cs_oauth_rows_retained':len(original_oauth),'original_cs_oauth_active_identities':oauth,'original_cs_oauth_prior_deletions_preserved':True,'retired_setting_rows':removed,'actual_payment_not_attempted':True}
