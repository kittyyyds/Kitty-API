"""Build the immutable release image from the uploaded binary on top of the live image."""
import hashlib, json, os, subprocess, traceback
from remote_common import APP, config, run, inspect, rel_dir


def main():
    os.umask(0o077)
    cfg = config()
    rel = rel_dir()
    b = rel / 'release-build' / 'sub2api'
    assert hashlib.file_digest(b.open('rb'), 'sha256').hexdigest() == cfg['binary_sha256']
    live = inspect(APP)
    assert live['Config']['Image'] == cfg['old_image']
    assert subprocess.run(['docker', 'image', 'inspect', cfg['image']], capture_output=True).returncode != 0, 'release image already exists; inspect before retry'
    (b.parent / 'Dockerfile').write_text(
        'FROM ' + live['Config']['Image'] + '\n'
        'COPY --chown=1000:1000 sub2api /app/sub2api\n'
        'COPY --chown=1000:1000 resources /app/resources\n'
        'RUN chmod 755 /app/sub2api\n'
        'LABEL org.opencontainers.image.version="' + cfg['version'] + '" org.opencontainers.image.revision="' + cfg['commit'] + '" '
        'org.opencontainers.image.ref.name="' + cfg['tag'] + '" kitty.binary.sha256="' + cfg['binary_sha256'] + '"\n')
    run(['docker', 'build', '--pull=false', '-t', cfg['image'], str(b.parent)], timeout=300)
    v = run(['docker', 'run', '--rm', '--network', 'none', '--entrypoint', '/app/sub2api', cfg['image'], '--version'])
    assert cfg['version'] in v and cfg['commit'] in v
    out = {'image': cfg['image'], 'image_id': inspect(cfg['image'])['Id'], 'version_output': v, 'binary_sha256': cfg['binary_sha256']}
    (rel / 'stage.public.json').write_text(json.dumps(out, indent=2))
    print(json.dumps(out))


if __name__ == '__main__':
    try:
        main(); (rel_dir() / 'stage.rc').write_text('0')
    except BaseException:
        (rel_dir() / 'stage.private.log').write_text(traceback.format_exc()); (rel_dir() / 'stage.rc').write_text('1'); raise
