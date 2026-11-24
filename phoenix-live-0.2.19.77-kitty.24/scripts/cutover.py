"""Switch only the CS app to the rehearsed image, with a fresh snapshot and automatic rollback."""
import datetime as dt, hashlib, importlib.util, json, os, secrets, shutil, traceback
from decimal import Decimal
import retire950,retired_schema,release_edge
from remote_common import ROOT, APP, PG, PRESERVE, DATA_SQL, SETTINGS_SQL, config, run, inspect, healthy, query, http_client, admin_token, base_checks, rel_dir,runtime,assert_preserved,guard_rehearsal_settings,restore_consumer_settings

REPORT = {}

def settlement_state():
    return json.loads(query(PG,"""SELECT json_build_object(
      'balances',(SELECT json_object_agg(id,balance::text) FROM users),
      'applied',(SELECT coalesce(json_agg(id),'[]') FROM billing_attempt_outbox WHERE (apply_result->>'applied')::boolean IS TRUE))"""))

def verify_settlements(before,after):
    ids=sorted(set(after['applied'])-set(before['applied']))
    assert set(before['applied'])<=set(after['applied']), 'billing evidence removed'
    assert set(before['balances'])==set(after['balances']), 'user identities changed'
    rows=json.loads(query(PG,"SELECT coalesce(json_agg(json_build_object('user_id',command->'billing'->>'UserID','cost',command->'billing'->>'BalanceCost','expired',coalesce(apply_result->>'kitty_expired_bonus_amount','0'))),'[]') FROM billing_attempt_outbox WHERE id IN("+','.join(map(str,ids))+')')) if ids else []
    deltas={}
    for row in rows:
        uid=str(row['user_id']);deltas[uid]=deltas.get(uid,Decimal(0))+Decimal(row['cost'])+Decimal(row['expired'])
    changed=[]
    for uid,value in before['balances'].items():
        actual=Decimal(value)-Decimal(after['balances'][uid])
        assert abs(actual-deltas.get(uid,Decimal(0)))<=Decimal('0.00000001'), 'balance change lacks exact billing evidence for user '+uid
        if actual:changed.append(int(uid))
    return {'passed':True,'settled_billing_rows':len(rows),'users_with_settled_debits':changed,'tolerance':'0.00000001'}


def compose_up():
    return run(['docker', 'compose', '--project-name', 'kitty-cs-czc', '--project-directory', str(ROOT), '-f', str(ROOT / 'compose.json'),
                'up', '-d', '--no-deps', '--wait', '--wait-timeout', '180', 'app'], timeout=220, cwd=ROOT)


def main():
    os.umask(0o077)
    cfg, rel = config(), rel_dir()
    stage = json.loads((rel / 'stage.public.json').read_text())
    rehearsal = json.loads((rel / 'rehearsal.public.json').read_text())
    assert rehearsal['passed'] and (rel / 'rehearsal.rc').read_text() == '0'
    if retire950.enabled(cfg) or retired_schema.enabled(cfg):
        assert rehearsal.get('old_version_rollback_verified') is True, 'old application rollback not verified'
    current=runtime();app = current['app']
    assert app['Config']['Image'] == cfg['old_image'] and healthy(app)
    original = (ROOT / 'compose.json').read_bytes(); compose = json.loads(original)
    assert compose['services']['app']['image'] == cfg['old_image']
    assert inspect(stage['image'])['Id'] == stage['image_id']
    preserved = {n: inspect(n) for n in PRESERVE}
    assert not preserved['kitty-subcustom-app']['State']['Running'], 'old original production must stay stopped'
    for n,x in preserved.items():
        if x['State']['Running']:assert healthy(x), 'unhealthy prerequisite '+n
    stamp = dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ') + '-' + secrets.token_hex(3)
    back = ROOT / 'backups' / ('cutover-' + cfg['version'] + '-' + stamp); back.mkdir(mode=0o700, parents=True)
    for f in ['compose.json', '.env']:
        shutil.copy2(ROOT / f, back / f.lstrip('.'))
    REPORT.update(started_utc=stamp, backup=str(back), old_image=cfg['old_image'], new_image=stage['image'], version=cfg['version'], tag=cfg['tag'], commit=cfg['commit'])
    stopped = mutated = edge_active = False
    guards=None
    try:
        # Existing WebSocket handlers can be closed by a Caddy reload. Reach an
        # idle request boundary first, then block new ingress and verify again.
        force=cfg.get('force_disconnect_authorized') is True
        if force:
            REPORT['pre_maintenance_drain']={'skipped_by_explicit_user_authorization':True,**release_edge.inflight()}
        else:
            REPORT['pre_maintenance_drain']=release_edge.drain(cfg.get('drain_timeout_seconds',600))
        release_edge.enter(back);edge_active=True
        if force:
            REPORT['drain']={'forced_disconnect_authorized':True,**release_edge.inflight()}
        else:
            REPORT['drain']=release_edge.drain(cfg.get('drain_timeout_seconds',600))
        stopped=True
        run(['docker', 'stop', '--time', '40', APP], timeout=60)
        settings_sql = retire950.settings_sql(cfg)
        before, settings = json.loads(query(PG, DATA_SQL)), json.loads(query(PG, settings_sql))
        finance_before=settlement_state()
        (back/'business-before.public.json').write_text(json.dumps(before,indent=2))
        retire950.snapshot(cfg, PG, back)
        # Online full dump was already independently restored before rehearsal.
        # During maintenance save only the module tables being removed, not the
        # 25GB customer database again; rollback never overwrites customer data.
        verified=json.loads((rel/'latest-backup.public.json').read_text());assert verified['db_restored_and_compared']
        REPORT['verified_full_backup']=verified['backup'];REPORT['db_sha256']=verified['db_sha256']
        run(['tar', '--numeric-owner', '-czf', str(back / 'app-data.tgz'), '-C', str(current['data'].parent), current['data'].name], timeout=1200)
        guards=guard_rehearsal_settings(PG)
        (back/'consumer-settings.private.json').write_text(json.dumps(guards))
        guarded_settings=json.loads(query(PG,settings_sql))
        compose['services']['app']['image'] = stage['image']
        tmp = ROOT / 'compose.next.tmp'; tmp.write_text(json.dumps(compose, ensure_ascii=False, indent=2) + '\n')
        os.replace(tmp, ROOT / 'compose.json'); mutated = True
        compose_up()
        cur = inspect(APP); assert healthy(cur) and cur['Image'] == stage['image_id']
        after=json.loads(query(PG, DATA_SQL))
        (back/'business-after.public.json').write_text(json.dumps(after,indent=2))
        changed=[k for k in before.keys()|after.keys() if before.get(k)!=after.get(k)]
        REPORT['business_changed_fields']=changed
        if changed==['balances']:
            REPORT['pending_billing_reconciled']=verify_settlements(finance_before,settlement_state())
        else:
            assert not changed, 'business data changed: '+','.join(changed)
        retire950.check_settings(cfg, guarded_settings, json.loads(query(PG, settings_sql)))
        retired_schema.assert_applied(cfg,PG)
        ip = next(x['IPAddress'] for x in cur['NetworkSettings']['Networks'].values() if x['IPAddress'])
        req = http_client('http://' + ip + ':8080')
        token = admin_token(req)
        checks, locked = base_checks(req, token, cfg['version'])
        REPORT.update(checks=checks, compliance_locked=locked)
        extra = rel / 'extra_checks.py'
        if extra.exists():
            spec = importlib.util.spec_from_file_location('extra_checks', extra); mod = importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)
            REPORT['extra'] = mod.check(req, token, lambda sql: query(PG, sql), locked, live=True)
        assert_preserved(preserved)
        restore_consumer_settings(PG,guards);guards=None
        retire950.check_settings(cfg,settings,json.loads(query(PG,settings_sql)))
        release_edge.leave(back);edge_active=False
        REPORT.update(passed=True, business_counts=before, settings_preserved=True, preserved_containers=list(preserved), ended_utc=dt.datetime.now(dt.timezone.utc).isoformat())
    except BaseException:
        (rel / 'cutover-error.private.log').write_text(traceback.format_exc())
        if stopped:
            (back / 'new-app-error.private.log').write_text(run(['docker', 'logs', '--tail', '500', APP]) if mutated else '')
            if mutated:
                run(['docker', 'stop', '--time', '30', APP], timeout=45)
                retire950.recover(cfg, PG, back)
                if guards is not None:restore_consumer_settings(PG,guards);guards=None
                (ROOT / 'compose.json').write_bytes(original); compose_up()
                old=runtime()['app'];assert healthy(old) and old['Config']['Image']==cfg['old_image']
                rollback_state=json.loads(query(PG,DATA_SQL))
                REPORT['rollback_business_changed_fields']=[k for k in before.keys()|rollback_state.keys() if before.get(k)!=rollback_state.get(k)]
            else:
                if guards is not None:restore_consumer_settings(PG,guards);guards=None
                run(['docker', 'start', APP])
            REPORT.update(passed=False, rolled_back_app=True, database_not_overwritten=True)
        if edge_active:
            release_edge.leave(back);edge_active=False
        raise
    finally:
        (rel / 'cutover.public.json').write_text(json.dumps(REPORT, indent=2))


if __name__ == '__main__':
    lock = rel_dir() / 'cutover.lock'
    fd = os.open(lock, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        os.close(fd); main(); (rel_dir() / 'cutover.rc').write_text('0'); print(json.dumps(REPORT))
    except BaseException:
        (rel_dir() / 'cutover.rc').write_text('1'); raise
    finally:
        lock.unlink()
