#!/usr/bin/env python3
"""Run real diagnostics persistence checks in a lane-owned ephemeral PostgreSQL."""
import fcntl
import hashlib
import json
import pathlib
import subprocess
import time

ROOT = pathlib.Path(__file__).resolve().parents[1]
CONTROL = pathlib.Path('/Users/dev/Global-session/xiao-kiss-implementation-20261105')
ARTIFACTS = CONTROL / 'lanes/connection-diagnostics'
MANIFEST = json.loads((CONTROL / 'manifest.json').read_text())
DOCKER = MANIFEST['docker']
STAMP = time.strftime('%Y%m%d-%H%M%S')
NAME = 'xiao-diagnostics-db-' + STAMP
LOG = ARTIFACTS / ('database-' + STAMP + '.log')
COMMAND = ['go', 'test', '-tags=unit', './internal/repository', '-run', '^TestConnectionDiagnosticsPostgres', '-count=1', '-timeout=120s', '-json']

def run(args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)

with (CONTROL / 'test.lock').open('a+') as lock:
    print('Waiting for shared diagnostics database slot', flush=True)
    fcntl.flock(lock, fcntl.LOCK_EX)
    print('Acquired diagnostics database slot', flush=True)
    code = 1
    try:
        run([DOCKER, 'run', '-d', '--rm', '--name', NAME, '--label', 'xiao.lane=connection-diagnostics', '--network', 'none', '--memory', '256m', '-e', 'POSTGRES_HOST_AUTH_METHOD=trust', '-e', 'POSTGRES_DB=diagnostics', 'postgres:16-alpine'], stdout=subprocess.DEVNULL)
        for _ in range(50):
            if subprocess.run([DOCKER, 'exec', NAME, 'pg_isready', '-U', 'postgres'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
                break
            time.sleep(.2)
        else:
            raise RuntimeError('temporary PostgreSQL never became ready')
        args = [DOCKER, 'run', '--rm', '--network', 'container:' + NAME, '--cpus', '3', '--memory', '6500m', '-v', str(ROOT) + ':/work:ro', '-v', MANIFEST['module_cache'] + ':/go/pkg/mod:ro', '-v', MANIFEST['go_cache'] + ':/root/.cache/go-build', '-e', 'DIAGNOSTICS_TEST_DSN=host=127.0.0.1 user=postgres dbname=diagnostics sslmode=disable', '-e', 'GOTOOLCHAIN=local', '-e', 'GOPROXY=off', '-e', 'GOSUMDB=off', '-e', 'GOFLAGS=-p=1', '-e', 'GOMAXPROCS=2', MANIFEST['go_image'], 'sh', '-eu', '-c', 'mkdir -p /review; cp -a /work/backend /review/backend; cd /review/backend; exec "$@"', 'sh', *COMMAND]
        with LOG.open('w') as log:
            code = subprocess.run(args, stdout=log, stderr=subprocess.STDOUT).returncode
    finally:
        cleanup = subprocess.run([DOCKER, 'rm', '-f', NAME], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode
        meta = {'command': COMMAND, 'exit_code': code, 'log': str(LOG), 'source_head': subprocess.check_output(['git', '-C', str(ROOT), 'rev-parse', 'HEAD'], text=True).strip(), 'diff_sha256': hashlib.sha256(subprocess.check_output(['git', '-C', str(ROOT), 'diff', 'HEAD', '--binary'])).hexdigest(), 'ephemeral_database': NAME, 'cleanup_exit_code': cleanup}
        LOG.with_suffix('.json').write_text(json.dumps(meta, indent=2))
        print(json.dumps(meta), flush=True)
        if LOG.exists():
            print(LOG.read_text()[-10000:])
    raise SystemExit(code)
