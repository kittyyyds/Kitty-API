"""Back up the live CS install, then prove the dump restores in an isolated Postgres."""
import datetime as dt, hashlib, json, os, secrets, shutil, subprocess, time
from remote_common import ROOT, APP, PG, PROJECT, DATA_SQL, SETTINGS_SQL, config, run, inspect, healthy, query, rel_dir,runtime,consistent_dump

CADDYFILE = '/opt/codex-migration/20261019/public-caddy-production/Caddyfile'


def main():
    os.umask(0o077)
    cfg = config()
    current=runtime();app,pg=current['app'],current['pg']
    assert healthy(app) and healthy(pg)
    assert app['Config']['Image'] == cfg['old_image'], 'live image is not the expected previous release'
    size=int(query(PG,'SELECT pg_database_size(current_database())'))
    assert shutil.disk_usage(ROOT).free > max(70*1024**3,size*3), 'insufficient backup/rehearsal disk space'
    stamp = dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ') + '-' + secrets.token_hex(3)
    dest = ROOT / 'backups' / ('before-' + cfg['version'] + '-' + stamp)
    dest.mkdir(mode=0o700, parents=True)
    for f in ['.env', 'compose.json']:
        shutil.copy2(ROOT / f, dest / f.lstrip('.'))
    (dest/'runtime-env.private.json').write_text(json.dumps(current['env']))
    shutil.copy2(CADDYFILE, dest / 'Caddyfile')
    # The live log may append while the online backup reads it. Preserve its
    # captured contents, accepting only tar's documented file-changed warning
    # for logs; any other file change or archive error still stops the release.
    import subprocess
    archived = subprocess.run(['tar', '--numeric-owner', '-czf', str(dest / 'app-data.tgz'), '-C', str(current['data'].parent), current['data'].name], capture_output=True, timeout=1200)
    warnings = (archived.stderr or b'').decode(errors='replace').splitlines()
    if archived.returncode:
        assert archived.returncode == 1 and warnings and all('/logs/' in line and line.endswith('file changed as we read it') for line in warnings), 'application data archive failed'
        (dest/'live-log-snapshot.public.json').write_text(json.dumps({'logs_appended_during_capture':True,'warnings':len(warnings),'all_non_log_files_stable':True}))
    run(['tar','-tzf',str(dest/'app-data.tgz')], stdout=subprocess.DEVNULL, timeout=1200)
    def snapshot(container):
        return (json.loads(query(container, DATA_SQL)), json.loads(query(container, SETTINGS_SQL)))
    dump = dest / 'cs.pgcustom'
    expected=consistent_dump(PG,dump)
    before=(expected['business'],expected['settings'])
    (dest/'snapshot.public.json').write_text(json.dumps(expected,indent=2))
    target = 'kitty-cs-restore-' + stamp.lower()
    created = False
    try:
        env = {**os.environ, 'POSTGRES_PASSWORD': secrets.token_hex(40)}
        run(['docker', 'run', '-d', '--rm', '--network', 'none', '--name', target, '--label', 'kitty.rehearsal=' + stamp,
             '--memory=8g', '--cpus=4', '-e', 'POSTGRES_USER=cs_czc', '-e', 'POSTGRES_DB=cs_czc', '-e', 'POSTGRES_PASSWORD', pg['Image']], env=env)
        created = True
        for _ in range(90):
            if subprocess.run(['docker', 'exec', target, 'pg_isready', '-h', '127.0.0.1', '-U', 'cs_czc', '-d', 'cs_czc'], capture_output=True).returncode == 0:
                break
            time.sleep(1)
        else:
            raise RuntimeError('isolated PostgreSQL not ready')
        with dump.open('rb') as f:
            run(['docker', 'exec', '-i', target, 'pg_restore', '--single-transaction', '--no-owner','--no-acl','--exit-on-error', '-U', 'cs_czc', '-d', 'cs_czc'], stdin=f, timeout=3600)
        for stage, current in [('restored', snapshot(target))]:
            for category, lhs, rhs in zip(('business','settings'),before,current):
                changed = [k for k in lhs.keys() | rhs.keys() if lhs.get(k)!=rhs.get(k)]
                assert not changed, f'{stage} {category} mismatch: {len(changed)} fields'
    finally:
        if created:
            run(['docker', 'rm', '-f', target])
    r = {'utc': stamp, 'backup': str(dest), 'db_restored_and_compared': True, 'comparison':'exported snapshot; moving production not compared','database_host':PG,'app_image_id': app['Image'],
         'db_sha256': hashlib.file_digest(dump.open('rb'), 'sha256').hexdigest()}
    (dest / 'verified.json').write_text(json.dumps(r, indent=2))
    (rel_dir() / 'latest-backup.public.json').write_text(json.dumps(r, indent=2))
    print(json.dumps(r))


if __name__ == '__main__':
    import traceback
    try:main();(rel_dir()/'backup.rc').write_text('0')
    except BaseException:
        (rel_dir()/'backup-error.private.log').write_text(traceback.format_exc());(rel_dir()/'backup.rc').write_text('1');raise
