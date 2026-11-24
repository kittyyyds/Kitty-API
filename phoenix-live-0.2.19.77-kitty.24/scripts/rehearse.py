"""Start the new image on a restored copy of the latest backup in an internal network and run checks."""
import importlib.util, json, os, secrets, shutil, subprocess, time, traceback, hashlib
import retire950
from pathlib import Path
from remote_common import ROOT, PG, REDIS, DATA_SQL, SETTINGS_SQL, config, run, inspect, query, http_client, admin_token, base_checks, rel_dir,runtime,guard_rehearsal_settings
import retired_schema

TAG = 'cs-main-release-rehearsal'
NET, RPG, RRED, RAPP = 'kitty-cs-main-rehearsal', 'kitty-cs-main-rehearsal-pg', 'kitty-cs-main-rehearsal-redis', 'kitty-cs-main-rehearsal-app'


def main():
    os.umask(0o077)
    cfg, rel = config(), rel_dir()
    back = Path(json.loads((rel / 'latest-backup.public.json').read_text())['backup'])
    assert json.loads((back / 'verified.json').read_text())['db_restored_and_compared']
    stage = json.loads((rel / 'stage.public.json').read_text())
    assert not run(['docker', 'ps', '-aq', '--filter', 'label=kitty.rehearsal=' + TAG]), 'rehearsal containers exist; inspect before retry'
    network, created, report = False, [], {}
    try:
        run(['docker', 'network', 'create', '--internal', '--label', 'kitty.rehearsal=' + TAG, NET]); network = True
        current=runtime()
        envpath=back/'runtime-env.private.json'
        if not envpath.exists():
            # Older verified backups omitted this derived, private runtime file.
            # Only recover it from the unchanged live installation.
            verified=json.loads((back/'verified.json').read_text())
            assert current['app']['Image']==verified['app_image_id'], 'backup/live image differs'
            for name in ['compose.json', '.env']:
                assert hashlib.sha256((ROOT/name).read_bytes()).digest()==hashlib.sha256((back/name.lstrip('.')).read_bytes()).digest(), 'backup/live config differs'
            envpath.write_text(json.dumps(current['env']))
            envpath.chmod(0o600)
        env=json.loads(envpath.read_text())
        assert env['DATABASE_HOST']==PG and env['DATABASE_USER']==env['DATABASE_DBNAME']=='cs_czc'
        pw, rpw = secrets.token_hex(30), secrets.token_hex(30)
        run(['docker', 'run', '-d', '--name', RPG, '--network', NET, '--label', 'kitty.rehearsal=' + TAG, '--memory', '8g', '--cpus', '4',
             '-e', 'POSTGRES_USER=cs_czc', '-e', 'POSTGRES_DB=cs_czc', '-e', 'POSTGRES_PASSWORD', inspect(PG)['Config']['Image']],
            env={**os.environ, 'POSTGRES_PASSWORD': pw}); created.append(RPG)
        run(['docker', 'run', '-d', '--name', RRED, '--network', NET, '--label', 'kitty.rehearsal=' + TAG, '--memory', '256m',
             inspect(REDIS)['Config']['Image'], 'redis-server', '--requirepass', rpw]); created.append(RRED)
        for _ in range(90):
            if subprocess.run(['docker', 'exec', RPG, 'pg_isready', '-h', '127.0.0.1', '-U', 'cs_czc', '-d', 'cs_czc'], capture_output=True).returncode == 0:
                break
            time.sleep(1)
        else:
            raise RuntimeError('rehearsal pg not ready')
        with (back / 'cs.pgcustom').open('rb') as f:
            run(['docker', 'exec', '-i', RPG, 'pg_restore', '--single-transaction', '--no-owner','--no-acl','--exit-on-error', '-U', 'cs_czc', '-d', 'cs_czc'], stdin=f, timeout=3600)
        # Clone-only guards stop QQ/paid probes from changing live-like business state.
        guard_rehearsal_settings(RPG)
        settings_sql = retire950.settings_sql(cfg)
        data_before, settings_before = query(RPG, DATA_SQL), json.loads(query(RPG, settings_sql))
        datadir = rel / ('rehearsal-data-' + secrets.token_hex(3)); shutil.copytree(current['data'], datadir)
        import tempfile
        snapdir = Path(tempfile.mkdtemp(prefix='rehearsal-private-',dir=rel))
        retire950.snapshot(cfg,RPG,snapdir)
        jwt=secrets.token_hex(32)
        query(RPG,"UPDATE security_secrets SET value='"+jwt+"' WHERE key='jwt_secret'")
        env.update(DATABASE_HOST=RPG,DATABASE_USER='cs_czc',DATABASE_DBNAME='cs_czc', DATABASE_PASSWORD=pw, REDIS_HOST=RRED, REDIS_PASSWORD=rpw, JWT_SECRET=jwt)
        envfile = rel / 'rehearsal.private.env'; envfile.write_text('\n'.join(k + '=' + v for k, v in env.items()) + '\n')
        run(['docker', 'run', '-d', '--name', RAPP, '--network', NET, '--label', 'kitty.rehearsal=' + TAG, '--memory', '2g', '--cpus', '2',
             '--env-file', str(envfile), '-v', str(datadir) + ':/app/data', stage['image']]); created.append(RAPP)
        ip = inspect(RAPP)['NetworkSettings']['Networks'][NET]['IPAddress']
        req = http_client('http://' + ip + ':8080')
        for _ in range(120):
            try:
                if req('/health')[0] == 200:
                    break
            except OSError:
                pass
            time.sleep(1)
        else:
            raise RuntimeError('new app did not start on restored database')
        assert json.loads(data_before) == json.loads(query(RPG, DATA_SQL)), 'business data changed by upgrade'
        after = json.loads(query(RPG, settings_sql))
        retire950.check_settings(cfg,settings_before,after)
        for m in cfg.get('new_migrations', []):
            assert query(RPG, "SELECT count(*) FROM schema_migrations WHERE filename='" + m + "'") == '1', 'migration not applied: ' + m
        retired_schema.assert_applied(cfg,RPG)
        token = admin_token(req,RPG,RAPP)
        checks, locked = base_checks(req, token, cfg['version'])
        report.update(base_checks=checks, compliance_locked=locked)
        extra = rel / 'extra_checks.py'
        if extra.exists():
            spec = importlib.util.spec_from_file_location('extra_checks', extra); mod = importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)
            report['extra'] = mod.check(req, token, lambda sql: query(RPG, sql), locked)
        if retire950.enabled(cfg) or retired_schema.enabled(cfg):
            run(['docker','stop',RAPP]); retire950.recover(cfg,RPG,snapdir)
            assert json.loads(query(RPG,settings_sql)) == settings_before, 'restored settings differ'
            old = RAPP+'-old'
            run(['docker','run','-d','--name',old,'--network',NET,'--label','kitty.rehearsal='+TAG,
                 '--env-file',str(envfile),'-v',str(datadir)+':/app/data',cfg['old_image']]); created.append(old)
            ip = inspect(old)['NetworkSettings']['Networks'][NET]['IPAddress']; oldreq=http_client('http://'+ip+':8080')
            for _ in range(90):
                try:
                    if oldreq('/health')[0] == 200: break
                except OSError: pass
                time.sleep(1)
            else: raise RuntimeError('old application did not recover')
            base_checks(oldreq,admin_token(oldreq,RPG,old),cfg['old_image'].rsplit(':',1)[-1]); report['old_version_rollback_verified']=True
        report.update(passed=True, version=cfg['version'], business_counts=json.loads(data_before), settings_preserved=True)
        (rel / 'rehearsal.public.json').write_text(json.dumps(report, indent=2)); print(json.dumps(report))
    finally:
        if RAPP in created:
            (rel / 'rehearsal-app.private.log').write_text(run(['docker', 'logs', RAPP]))
        for n in reversed(created):
            assert inspect(n)['Config']['Labels'].get('kitty.rehearsal') == TAG
            run(['docker', 'rm', '-fv', n])
        if network:
            run(['docker', 'network', 'rm', NET])


if __name__ == '__main__':
    try:
        main(); (rel_dir() / 'rehearsal.rc').write_text('0')
    except BaseException:
        (rel_dir() / 'rehearsal-exception.private.log').write_text(traceback.format_exc()); (rel_dir() / 'rehearsal.rc').write_text('1'); raise
