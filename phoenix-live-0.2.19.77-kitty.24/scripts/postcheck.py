"""After cutover: binary hash, restarts, migrations, startup panics, untouched neighbours."""
import json,subprocess
import retired_schema
from remote_common import APP, PG, config, run, inspect, query, rel_dir,runtime


def main():
    cfg, rel = config(), rel_dir()
    rep = json.loads((rel / 'cutover.public.json').read_text()); assert rep['passed']
    app = runtime()['app']
    assert not inspect('kitty-subcustom-app')['State']['Running'], 'old production unexpectedly running'
    assert app['State']['Health']['Status'] == 'healthy' and app['RestartCount'] == 0
    assert run(['docker', 'exec', APP, 'sha256sum', '/app/sub2api']).split()[0] == cfg['binary_sha256']
    missing = [m for m in cfg.get('new_migrations', []) if query(PG, "SELECT count(*) FROM schema_migrations WHERE filename='" + m + "'") != '1']
    assert not missing, 'migrations missing: ' + ','.join(missing)
    retired_schema.assert_applied(cfg,PG)
    p=subprocess.run(['docker','logs','--since',app['State']['StartedAt'],APP],capture_output=True,text=True,timeout=60)
    assert p.returncode==0
    logs=p.stdout+p.stderr
    assert not any('panic:' in l.lower() or 'failed to initialize application' in l.lower() for l in logs.splitlines())
    assert not run(['docker', 'ps', '-aq', '--filter', 'label=kitty.rehearsal=cs-main-release-rehearsal'])
    out = {'passed': True, 'version': cfg['version'], 'binary_hash_matches': True, 'restart_count': 0,
           'migrations_total': query(PG, 'SELECT count(*) FROM schema_migrations'), 'new_migrations_applied': cfg.get('new_migrations', [])}
    (rel / 'postcheck.public.json').write_text(json.dumps(out, indent=2)); print(json.dumps(out))


if __name__ == '__main__':
    main()
