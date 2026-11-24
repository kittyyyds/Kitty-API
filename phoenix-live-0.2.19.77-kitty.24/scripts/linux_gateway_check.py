"""Run the release's service regression binary without network or credentials."""
import datetime as dt,gzip,json,os,shutil,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parent
def main():
    os.umask(0o077)
    binary=ROOT/'service-linux.test'
    with gzip.open(ROOT/'service-linux.test.gz','rb') as src,binary.open('wb') as dst:shutil.copyfileobj(src,dst)
    binary.chmod(0o700)
    pattern='Test.*(WebSearch|HostedSearch|ExecNotification|WSHTTP|HTTPContinuation|HTTPContinues|CodexSynthesis|Continuation|ResponsesLite|FinalProtocol)'
    command=['docker','run','--rm','--network=none','--memory=3g','--cpus=3','--read-only','--tmpfs','/tmp:rw,size=256m','-e','GOMAXPROCS=3','-v',str(binary)+':/test/service.test:ro','--entrypoint','/test/service.test','kitty/sub2api-cs-zuchengchen:0.2.19.77-kitty.24','-test.run='+pattern,'-test.v','-test.timeout=8m','-test.count=1']
    with (ROOT/'linux-gateway.public.log').open('w') as log:
        result=subprocess.run(command,stdout=log,stderr=subprocess.STDOUT,timeout=550)
    lines=(ROOT/'linux-gateway.public.log').read_text().splitlines()
    passed=[line.strip() for line in lines if line.strip().startswith('--- PASS:')]
    failed=[line.strip() for line in lines if line.strip().startswith('--- FAIL:')]
    skipped=[line.strip() for line in lines if line.strip().startswith('--- SKIP:')]
    receipt={'passed':result.returncode==0,'return_code':result.returncode,'tests_including_subtests':{'passed':len(passed),'failed':len(failed),'skipped':len(skipped)},'failed_tests':failed,'platform':'linux/amd64','network':'none','includes_posix_path_regressions':any('TestCodexSynthesisEnvironmentRequiresExplicitFacts' in x for x in passed) and any('TestCodexSynthesisExplicitEnvironmentBoundary' in x for x in passed),'utc':dt.datetime.now(dt.timezone.utc).isoformat()}
    (ROOT/'linux-gateway.public.json').write_text(json.dumps(receipt,indent=2));print(json.dumps(receipt))
    assert receipt['passed'] and receipt['includes_posix_path_regressions']
if __name__=='__main__':
    try:main();(ROOT/'linux-gateway.rc').write_text('0')
    except BaseException:(ROOT/'linux-gateway.rc').write_text('1');raise
