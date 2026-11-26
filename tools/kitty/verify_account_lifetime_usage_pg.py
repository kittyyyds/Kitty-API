#!/usr/bin/env python3
"""Kitty 949/955 real PostgreSQL multi-connection release-safety review.

Python >=3.10, STANDARD LIBRARY ONLY; no psycopg, pip, psql, credentials or DSN.
Values use protocol-v3 Parse/Bind/Execute; migrations run unchanged in a transaction.

  python3 tools/kitty/verify_account_lifetime_usage_pg.py --container kitty7-preflight-pg
  python tools/kitty/verify_account_lifetime_usage_pg.py --backend docker
  python tools/kitty/verify_account_lifetime_usage_pg.py --backend local --pg-bin C:\\pg\\bin

Docker: requires an ALREADY PRESENT postgres:16 image and local Docker endpoint.
Fresh empty Docker config, --pull=never, tmpfs data, no host mounts, random
LOOPBACK-only port. No existing containers, Docker contexts or credentials read.
Local: initdb/postgres/pg_ctl from --pg-bin create a NEW temporary trust-auth
cluster on 127.0.0.1. Runtime data/logs go to TEMP, not the repository.
Default mode contacts only the server CREATED HERE. Explicit --container
kitty7-preflight-pg instead uses the authorized clone via container-local psql,
creating random test schemas in postgres with NO public search_path or business
queries. Container mode runs OS user postgres, PG superuser cs_czc, test database
postgres (POSTGRES_USER=cs_czc / POSTGRES_DB=cs_czc created the clone).
It requires credential-free local Unix-socket trust, psql
>=18, and kitty7-preflight-internal network; otherwise it fails without credentials.
It never stops/removes that container and drops only its own test schemas.
No other existing server is contacted. No --dsn, --host, existing-container,
.env, pgpass, credential files or production access. No downloads by this script.

Fixture supplies the usage_logs shape needed by real 920/921/949/955, NOT the
full application schema. Real usage_logs -> permanent -> receipt -> 949 -> 955
triggers run. A fixture-only BEFORE UPDATE financial trigger pauses the FIRST
financial-row update for reciprocal moves; it does not replace 949 or choose
its lock order. 921 is disabled ONLY to seed history before 949/955, never in
concurrency tests. All tests use three independent PostgreSQL backend sessions.

Limits: statement_timeout=4000ms, lock_timeout=2500ms, deadlock_timeout=100ms,
idle_in_transaction_session_timeout=15000ms, socket/worker deadline=8s. Every
poll has a deadline. Deadlock victim ROLLBACK, survivor COMMIT, then independent
Python financial/identity/token/counter invariant checks. NO automatic retries.
40P01 is deadlock; 55P03/57014 is timeout risk, not called a detected deadlock.
Exit 0 = no new 955 risk observed (inherited 949 risks are still reported);
1 = harness/precondition/invariant failure; 2 = verification completed, NEW
candidate lock/deadlock regression reproduced. 949-only control distinguishes
inherited from newly introduced risks. Finite successful schedules cannot prove
global deadlock freedom. Live source triggers append identity events only.
assert_state drains in a separate transaction before checking canonical
snapshots/counters. Not a production-sized backfill benchmark or release approval.
"""
from __future__ import annotations
import argparse
import csv
import io
import queue
import re
import threading
import concurrent.futures as futures
import hashlib
import json
import os
from pathlib import Path
import shutil
import socket
import struct
import subprocess
import tempfile
import time
import uuid
from collections import defaultdict
from decimal import Decimal

ROOT = Path(__file__).resolve().parents[2]
WALL = 8.0
LIMITS = '-c statement_timeout=4000 -c lock_timeout=2500 -c deadlock_timeout=100 -c idle_in_transaction_session_timeout=15000'
RISK_STATES = {'40P01', '55P03', '57014'}
MIGRATIONS = ['920_kitty_procurement.sql', '921_kitty_permanent_usage.sql',
              '949_kitty_account_lifetime_costs.sql', '955_kitty_account_lifetime_usage.sql']


def emit(kind, **fields):
    print(json.dumps({'kind': kind, **fields}, ensure_ascii=False), flush=True)


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def command(args, timeout=30):
    env = {k: v for k, v in os.environ.items() if not k.upper().startswith(('PG', 'DOCKER_'))}
    # Windows initdb relaunches with a restricted token; keep its DLLs discoverable.
    executable = Path(args[0])
    if executable.is_absolute():
        env['PATH'] = str(executable.parent) + os.pathsep + env.get('PATH', '')
    # A daemon launched by pg_ctl can inherit PIPE handles on Windows, causing
    # subprocess.communicate to wait after the CLI exits. Files have no EOF wait.
    with tempfile.TemporaryFile() as out, tempfile.TemporaryFile() as err:
        result = subprocess.run([str(x) for x in args], env=env, stdout=out, stderr=err, timeout=timeout)
        out.seek(0)
        err.seek(0)
        stdout, stderr = out.read().decode('utf-8', 'replace'), err.read().decode('utf-8', 'replace')
    if result.returncode:
        raise RuntimeError(f'CLI failed ({result.returncode}): {args[0]}\n{stdout[-3000:]}\n{stderr[-3000:]}')
    return stdout.strip()


class PgError(Exception):
    def __init__(self, fields):
        self.state, self.fields = fields.get('C', '?????'), fields
        super().__init__(f"{self.state}: {fields.get('M', '')}")


def error_fields(data):
    result, pos = {}, 0
    while pos < len(data) and data[pos]:
        tag = chr(data[pos])
        end = data.index(0, pos + 1)
        result[tag] = data[pos + 1:end].decode('utf-8', 'replace')
        pos = end + 1
    return result


def frame(tag, data=b''):
    return tag + struct.pack('!I', len(data) + 4) + data


class PG:
    """Small trust-only PostgreSQL v3 client; rejects password/SCRAM auth.

    One socket per backend, bound text parameters, text results. Only operations
    used here are implemented; this is deliberately not a general driver.
    """
    def __init__(self, endpoint, schema='public', name='coordinator'):
        self.socket = socket.create_connection(endpoint, timeout=WALL)
        self.deadline, self.pid = time.monotonic() + WALL, None
        try:
            options = f'{LIMITS} -c search_path={schema},pg_catalog'
            startup = (f'user\0kitty\0database\0postgres\0client_encoding\0UTF8\0'
                       f'application_name\0kitty_verify_{name}\0options\0{options}\0\0').encode()
            self.socket.sendall(struct.pack('!II', len(startup) + 8, 196608) + startup)
            while True:
                tag, data = self.message()
                if tag == b'R' and struct.unpack('!I', data[:4])[0] != 0:
                    raise RuntimeError('Refusing non-trust authentication; no credentials read')
                if tag == b'K':
                    self.pid = struct.unpack('!II', data)[0]
                if tag == b'E':
                    raise PgError(error_fields(data))
                if tag == b'Z':
                    break
        except BaseException:
            self.socket.close()
            raise

    def read(self, count):
        chunks = bytearray()
        while len(chunks) < count:
            remaining = self.deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError('PostgreSQL wire deadline exceeded')
            self.socket.settimeout(remaining)
            chunk = self.socket.recv(count - len(chunks))
            if not chunk:
                raise ConnectionError('PostgreSQL disconnected')
            chunks.extend(chunk)
        return bytes(chunks)

    def message(self):
        tag = self.read(1)
        size = struct.unpack('!I', self.read(4))[0]
        require(4 <= size <= 64 * 1024 * 1024, 'Invalid protocol message length')
        return tag, self.read(size - 4)

    def execute(self, sql, params=None):
        self.deadline = time.monotonic() + WALL
        if params is None:
            packet = frame(b'Q', sql.encode() + b'\0')
        else:
            parse = frame(b'P', b'\0' + sql.encode() + b'\0\0\0')
            values = []
            for value in params:
                if value is None:
                    values.append(struct.pack('!i', -1))
                else:
                    raw = str(value).encode('utf-8')
                    values.append(struct.pack('!I', len(raw)) + raw)
            bind = frame(b'B', b'\0\0\0\0' + struct.pack('!H', len(values)) + b''.join(values) + b'\0\0')
            packet = parse + bind + frame(b'D', b'P\0') + frame(b'E', b'\0\0\0\0\0') + frame(b'S')
        self.socket.sendall(packet)
        rows, error = [], None
        while True:
            tag, data = self.message()
            if tag == b'D':
                pos, row = 2, []
                for _ in range(struct.unpack('!H', data[:2])[0]):
                    length = struct.unpack('!i', data[pos:pos + 4])[0]
                    pos += 4
                    row.append(None if length == -1 else data[pos:pos + length].decode('utf-8'))
                    if length != -1:
                        pos += length
                rows.append(row)
            elif tag == b'E':
                error = PgError(error_fields(data))
            elif tag == b'Z':
                if error:
                    raise error
                return rows

    def scalar(self, sql, params=None):
        rows = self.execute(sql, params)
        if len(rows) != 1 or len(rows[0]) != 1:
            # Queries/values here are synthetic fixtures or pg_catalog metadata.
            # Do not print environment, connection strings, SQL bind values or data.
            emit('scalar_shape_failure', adapter=type(self).__name__, backend_pid=getattr(self, 'pid', None),
                 schema=getattr(self, 'schema', None), response_shape=getattr(self, 'response_shape', None),
                 sql=' '.join(sql.split())[:350], bind_count=None if params is None else len(params),
                 row_count=len(rows), column_counts=[len(row) for row in rows[:8]],
                 empty_rows=sum(not row or row == [''] for row in rows),
                 first_cell_lengths=[None if not row or row[0] is None else len(row[0]) for row in rows[:8]])
            raise AssertionError('Expected one scalar result; see scalar_shape_failure')
        return rows[0][0]

    def close(self):
        try:
            self.socket.sendall(frame(b'X'))
        except OSError:
            pass
        self.socket.close()


class Psql(PG):
    """Persistent docker exec psql, Unix socket only; no TCP or credentials.

    psql bind uses extended-protocol parameters. -X disables psqlrc;
    env -i plus PGPASSFILE=/dev/null disables inherited credentials/services.
    """
    def __init__(self, endpoint, schema='pg_catalog', name='coordinator'):
        self.schema = schema
        args = endpoint['docker'] + ['exec', '-i', '--user', 'postgres', '--workdir', '/tmp',
            endpoint['container'], 'env', '-i',
            'PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin',
            'HOME=/nonexistent', 'PGPASSFILE=/dev/null', 'PGSERVICEFILE=/dev/null',
            'PGSYSCONFDIR=/nonexistent', 'PGCLIENTENCODING=UTF8',
            f'PGAPPNAME=kitty_verify_{name}',
            f'PGOPTIONS={LIMITS} -c search_path={schema},pg_catalog -c client_min_messages=error',
            'psql', '-X', '-w', '-q', '-t', '--csv', '-h', '/var/run/postgresql',
            '-p', '5432', '-U', 'cs_czc', '-d', 'postgres',
            '-v', 'VERBOSITY=verbose', '-v', 'ON_ERROR_STOP=off']
        env = {k: v for k, v in os.environ.items() if not k.upper().startswith(('PG', 'DOCKER_'))}
        self.process = subprocess.Popen(args, env=env, stdin=subprocess.PIPE,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
            encoding='utf-8', errors='replace', bufsize=1)
        self.lines = queue.Queue()
        self.null = '__KITTY_NULL_' + uuid.uuid4().hex
        def reader():
            try:
                for line in self.process.stdout:
                    self.lines.put(line)
            finally:
                self.lines.put(None)
        self.reader = threading.Thread(target=reader, daemon=True)
        self.reader.start()
        try:
            self.execute('\\pset null ' + self.null)
            self.pid = int(self.scalar('SELECT pg_backend_pid()'))
        except BaseException:
            self.close()
            raise

    def execute(self, sql, params=None):
        marker = '__KITTY_DONE_' + uuid.uuid4().hex
        if params is not None:
            def quoted(value):
                require(value is not None, 'This fixture does not bind NULL values')
                text = str(value)
                require('\x00' not in text, 'NUL bind value')
                return "'" + text.replace('\\', '\\\\').replace("'", "''").replace('\n', '\\n').replace('\r', '\\r').replace('\t', '\\t') + "'"
            request = sql.rstrip().rstrip(';') + '\n\\bind ' + ' '.join(quoted(v) for v in params) + '\n\\g\n'
        else:
            request = sql.rstrip() + ('\n' if sql.lstrip().startswith('\\') else ';\n')
        request += f'\\echo {marker} :SQLSTATE\n'
        self.process.stdin.write(request)
        self.process.stdin.flush()
        deadline, output, state = time.monotonic() + WALL, [], None
        while time.monotonic() < deadline:
            try:
                line = self.lines.get(timeout=max(0.01, deadline-time.monotonic()))
            except queue.Empty:
                break
            if line is None:
                raise RuntimeError('Container psql exited: ' + ''.join(output)[-3000:])
            if line.startswith(marker + ' '):
                state = line.strip().split()[-1]
                break
            output.append(line)
        if state is None:
            self.close()
            raise TimeoutError('Container psql request exceeded finite deadline')
        text = ''.join(output)
        # Multi-statement migrations can end in COMMIT/ROLLBACK after an error;
        # preserve the FIRST verbose SQLSTATE rather than the final COMMIT state.
        error = re.search(r'(?:ERROR|FATAL|PANIC):\s+([0-9A-Z]{5}):\s*(.*)', text)
        if error or state not in ('00000', ':SQLSTATE'):
            raise PgError({'C': error.group(1) if error else state,
                           'M': error.group(2) if error else text[-2000:], 'D': text[-5000:]})
        parsed = list(csv.reader(io.StringIO(text)))
        # Blank records ([]) are psql/transport separators, not database rows.
        # Keep [''] / quoted empty strings: a legitimate void/empty SQL value.
        self.response_shape = {'characters': len(text), 'raw_records': len(parsed),
                               'blank_records': sum(not row for row in parsed),
                               'record_columns': [len(row) for row in parsed[:8]]}
        return [[None if v == self.null else v for v in row] for row in parsed if row]

    def close(self):
        if self.process.poll() is None:
            try:
                self.process.stdin.write('\\q\n')
                self.process.stdin.flush()
                self.process.wait(timeout=1)
            except (OSError, subprocess.TimeoutExpired):
                self.process.kill()
                self.process.wait(timeout=2)
        for stream in (self.process.stdin, self.process.stdout):
            if stream:
                stream.close()


def connect(endpoint, schema='pg_catalog', name='coordinator'):
    return Psql(endpoint, schema, name) if isinstance(endpoint, dict) else PG(endpoint, schema, name)


class Runtime:
    def __init__(self, args):
        self.args = args
        self.temp = tempfile.TemporaryDirectory(prefix='kitty-pg-verify-')
        self.root = Path(self.temp.name)
        self.container, self.endpoint, self.docker, self.pg_bin = None, None, None, None
        self.local_started = False

    def start(self):
        backend = self.args.backend
        if self.args.container:
            return self.start_existing()
        if backend == 'auto':
            backend = 'local' if self.args.pg_bin else 'docker'
        if backend == 'local':
            require(self.args.pg_bin, '--backend local requires --pg-bin')
            self.pg_bin = Path(self.args.pg_bin).resolve()
            self.suffix = '.exe' if os.name == 'nt' else ''
            for exe in ('initdb', 'postgres', 'pg_ctl'):
                require((self.pg_bin / (exe + self.suffix)).is_file(), f'Missing {exe}')
            if os.name == 'nt':
                # tempfile's Windows OWNER RIGHTS-only ACL is inaccessible after
                # initdb drops Administrators SID. Grant only this user on OUR
                # newly created temp root; do not touch caller-supplied paths.
                sid = subprocess.run(['whoami', '/user', '/fo', 'csv', '/nh'],
                    capture_output=True, text=True, check=True, timeout=5).stdout
                sid = next(csv.reader(io.StringIO(sid)))[1]
                command(['icacls.exe', self.root, '/grant', f'*{sid}:(OI)(CI)F'], timeout=5)
            self.data = self.root / 'data'
            command([self.pg_bin / ('initdb' + self.suffix), '-D', self.data, '-U', 'kitty',
                     '--auth-local=trust', '--auth-host=trust', '--encoding=UTF8', '--no-locale'], timeout=40)
            with socket.socket() as probe:
                probe.bind(('127.0.0.1', 0))
                port = probe.getsockname()[1]
            # Port race fails startup; never connect until OUR pg_ctl confirms it.
            self.local_started = True
            try:
                command([self.pg_bin / ('pg_ctl' + self.suffix), '-D', self.data, '-l', self.root / 'server.log',
                         '-w', '-t', '25', '-o', f"-h 127.0.0.1 -p {port} -c fsync=off", 'start'], timeout=30)
            except Exception as exc:
                log = self.root / 'server.log'
                emit('local_start_failure', detail=log.read_text(encoding='utf-8', errors='replace')[-4000:] if log.exists() else str(exc))
                raise
            self.endpoint = ('127.0.0.1', port)
        else:
            exe = shutil.which('docker')
            if not exe:
                raise RuntimeError('Docker CLI unavailable; use --backend local --pg-bin <temporary PostgreSQL bin>')
            host = 'npipe:////./pipe/docker_engine' if os.name == 'nt' else 'unix:///var/run/docker.sock'
            self.docker = [exe, '--config', str(self.root), '--host', host]
            command(self.docker + ['version', '--format', '{{.Server.Version}}'], timeout=10)
            self.container = 'kitty-pg-verify-' + uuid.uuid4().hex[:12]
            command(self.docker + ['run', '--detach', '--rm', '--pull=never', '--name', self.container,
                    '--label', 'kitty.release-safety-verifier=true', '--publish', '127.0.0.1::5432',
                    '--tmpfs', '/var/lib/postgresql/data', '--tmpfs', '/var/run/postgresql',
                    '--env', 'PGDATA=/var/lib/postgresql/data/pgdata', '--env', 'POSTGRES_USER=kitty',
                    '--env', 'POSTGRES_DB=postgres', '--env', 'POSTGRES_HOST_AUTH_METHOD=trust', self.args.image], timeout=35)
            mapping = command(self.docker + ['port', self.container, '5432/tcp'])
            require(mapping.startswith('127.0.0.1:') and '\n' not in mapping, 'Not loopback-only mapping')
            self.endpoint = ('127.0.0.1', int(mapping.rsplit(':', 1)[1]))
            emit('runtime_image', image=self.args.image, image_id=command(
                self.docker + ['inspect', '--format', '{{.Image}}', self.container]))
        deadline, last_error = time.monotonic() + 25, None
        while time.monotonic() < deadline:
            try:
                pg = connect(self.endpoint)
                version = pg.scalar('SELECT version()')
                require(int(pg.scalar('SHOW server_version_num')) >= 140000, 'Need PostgreSQL >=14')
                pg.close()
                emit('runtime', backend=backend, version=version, isolated=True, parameter_binding='protocol-v3 Parse/Bind/Execute (stdlib)')
                return
            except (ConnectionError, OSError, PgError) as exc:
                last_error = str(exc)
                time.sleep(0.1)
        raise RuntimeError(f'Isolated PostgreSQL not ready: {last_error}')

    def start_existing(self):
        require(self.args.container == 'kitty7-preflight-pg', 'Only explicitly authorized kitty7-preflight-pg allowed')
        exe = shutil.which('docker')
        require(exe, 'Docker CLI required on Phoenix host for --container')
        host = 'npipe:////./pipe/docker_engine' if os.name == 'nt' else 'unix:///var/run/docker.sock'
        cli = [exe, '--config', str(self.root), '--host', host]
        networks = json.loads(command(cli + ['inspect', '--format', '{{json .NetworkSettings.Networks}}', self.args.container]))
        require('kitty7-preflight-internal' in networks, 'Authorized isolated network missing')
        self.endpoint = {'docker': cli, 'container': self.args.container}
        pg = connect(self.endpoint)
        try:
            require(int(pg.scalar('SHOW server_version_num')) >= 180000, 'Container mode requires PostgreSQL/psql >=18')
            emit('runtime', backend='authorized_existing_container', container=self.args.container,
                 version=pg.scalar('SELECT version()'), parameter_binding='psql bind (extended protocol)',
                 business_schema_access=False)
        finally:
            pg.close()

    def close(self):
        ok = True
        try:
            if self.container and self.docker:
                command(self.docker + ['rm', '--force', self.container], timeout=15)
            if self.local_started and (self.data / 'postmaster.pid').exists():
                command([self.pg_bin / ('pg_ctl' + self.suffix), '-D', self.data, '-m', 'immediate', '-w', '-t', '10', 'stop'], timeout=15)
        except Exception as exc:
            ok = False
            emit('cleanup_failure', error=str(exc), temp=str(self.root))
        if ok:
            self.temp.cleanup()
        return ok


FIXTURE = """
CREATE TABLE usage_logs (
 id bigint PRIMARY KEY, api_key_id bigint NOT NULL, request_id text,
 account_id bigint NOT NULL, user_id bigint NOT NULL DEFAULT 1, group_id bigint,
 model text DEFAULT 'fixture', requested_model text, upstream_model text, upstream_request_id text,
 input_tokens bigint NOT NULL DEFAULT 10, output_tokens bigint NOT NULL DEFAULT 2,
 cache_creation_tokens bigint NOT NULL DEFAULT 3, cache_read_tokens bigint NOT NULL DEFAULT 4,
 cache_creation_5m_tokens bigint DEFAULT 0, cache_creation_1h_tokens bigint DEFAULT 0,
 actual_cost numeric DEFAULT 1, total_cost numeric DEFAULT 2,
 account_stats_cost numeric DEFAULT 2, account_rate_multiplier numeric DEFAULT 1,
 billing_type smallint NOT NULL DEFAULT 0, created_at timestamptz NOT NULL DEFAULT now(),
 image_count bigint DEFAULT 0, video_count bigint DEFAULT 0, video_duration_seconds numeric DEFAULT 0
);
"""
CAPTURE = 'SELECT kitty_capture_permanent_usage(u, TRUE) FROM usage_logs u WHERE id=$1::bigint'
PROMOTE = 'SELECT kitty_capture_procurement(u) FROM usage_logs u WHERE id=$1::bigint'


class Suite:
    def __init__(self, endpoint, schema, candidate, migrations):
        self.endpoint, self.schema, self.candidate, self.migrations = endpoint, schema, candidate, migrations
        self.sessions = []
        bootstrap = connect(endpoint)
        try:
            bootstrap.execute(f'CREATE SCHEMA {schema}')  # fixed internal identifier
        finally:
            bootstrap.close()
        self.c, self.a, self.b = self.connect('control'), self.connect('writer_a'), self.connect('writer_b')
        require(len({p.pid for p in self.sessions}) == 3, 'Not independent server sessions')
        self.c.execute(FIXTURE)
        self.migrate(0)
        self.migrate(1)
        self.c.execute('ALTER TABLE usage_logs DISABLE TRIGGER USER')
        for log_id, account in ((101, 10), (102, 10), (103, 20), (104, 20)):
            self.c.execute('INSERT INTO usage_logs(id,api_key_id,request_id,account_id) VALUES($1,1,$2,$3)', (log_id, f'seed-{log_id}', account))
        self.c.execute('ALTER TABLE usage_logs ENABLE TRIGGER USER')
        for log_id in (101, 102, 103):
            self.c.execute(CAPTURE, (log_id,))
        for log_id, account in ((201, 10), (202, 20)):
            self.c.execute('INSERT INTO usage_logs(id,api_key_id,request_id,account_id) VALUES($1,1,$2,$3)', (log_id, f'seed-{log_id}', account))
        self.migrate(2)
        frozen = self.financial_fingerprint()
        if candidate:
            self.migrate(3)
            require(self.financial_fingerprint() == frozen, '955 changed financial functions, trigger or amounts')
        self.assert_state()
        emit('fixture_ready', schema=schema, candidate=candidate, backend_pids=[p.pid for p in self.sessions], financial_fingerprint=frozen)

    def connect(self, name):
        pg = connect(self.endpoint, self.schema, name)
        self.sessions.append(pg)
        return pg

    def migrate(self, index):
        self.c.execute('BEGIN;\n' + self.migrations[index] + '\nCOMMIT;')

    def financial_fingerprint(self):
        defs = self.c.execute("""SELECT pg_get_functiondef(oid) FROM pg_proc
            WHERE pronamespace=current_schema()::regnamespace
            AND proname IN ('kitty_account_cost_capture','kitty_adjust_account_lifetime') ORDER BY proname""")
        trigger = self.c.scalar("""SELECT pg_get_triggerdef(oid) FROM pg_trigger
            WHERE tgrelid='kitty_procurement_receipts'::regclass AND tgname='kitty_account_cost_receipt'""")
        costs = self.c.execute('SELECT account_id,account_cost::text,user_cost::text FROM kitty_account_lifetime_costs ORDER BY account_id')
        return hashlib.sha256(json.dumps([defs, trigger, costs]).encode()).hexdigest()

    def assert_state(self):
        if self.candidate:
            self.c.scalar('SELECT kitty_drain_account_usage_events()')
            require(self.c.scalar('SELECT count(*) FROM kitty_account_usage_events') == '0', 'Fixture drain did not finish')
        expected_cost, expected_snap = defaultdict(lambda: [Decimal(0), Decimal(0)]), {}
        for api, key, acct, raw, val in self.c.execute('SELECT api_key_id,request_key,account_id,measurements::text,request_valuation::text FROM kitty_procurement_receipts'):
            m = json.loads(raw, parse_float=Decimal, parse_int=Decimal)
            expected_cost[int(acct)][0] += Decimal(m.get('account_credits') or 0)
            expected_cost[int(acct)][1] += Decimal(val or 0)
            expected_snap[(api, key)] = (int(acct), measured_tokens(m), 'receipt')
        for api, key, acct, ac, uc, inp, out, read, write, invalid in self.c.execute("""
            SELECT l.api_key_id,l.request_key,l.account_id,l.account_cost::text,l.user_cost::text,
                   p.input_tokens,p.output_tokens,p.cache_read_tokens,p.cache_creation_tokens,p.invalid_metrics
            FROM kitty_account_cost_legacy l LEFT JOIN kitty_permanent_usage p
              ON (l.api_key_id,l.request_key)=(p.api_key_id,p.request_key)
            WHERE NOT EXISTS(SELECT FROM kitty_procurement_receipts r
              WHERE (r.api_key_id,r.request_key)=(l.api_key_id,l.request_key))"""):
            expected_cost[int(acct)][0] += Decimal(ac)
            expected_cost[int(acct)][1] += Decimal(uc)
            tokens = None if inp is None or invalid != 'f' else sum(map(Decimal, (inp, out, read, write)))
            expected_snap[(api, key)] = (int(acct), tokens, 'legacy')
        actual_cost = {int(a): [Decimal(c), Decimal(u)] for a, c, u in self.c.execute('SELECT account_id,account_cost::text,user_cost::text FROM kitty_account_lifetime_costs')}
        for acct in set(expected_cost) | set(actual_cost):
            require(actual_cost.get(acct, [Decimal(0), Decimal(0)]) == expected_cost[acct], f'Financial drift at {acct}: {actual_cost.get(acct)} != {expected_cost[acct]}')
        require(set(expected_cost).issubset(actual_cost), 'Missing financial row; FOR UPDATE guard would be a no-op')
        if not self.candidate:
            return
        actual_snap = {(api, k): (int(a), None if t is None else Decimal(t), src) for api, k, a, t, src in self.c.execute('SELECT api_key_id,request_key,account_id,tokens::text,source FROM kitty_account_usage_snapshots')}
        require(actual_snap == expected_snap, f'Snapshot drift: {actual_snap} != {expected_snap}')
        expected_usage = defaultdict(lambda: [0, Decimal(0), 0])
        for account, tokens, _ in expected_snap.values():
            expected_usage[account][0] += 1
            expected_usage[account][1] += tokens or 0
            expected_usage[account][2] += int(tokens is None)
        actual_usage = {int(a): [int(r), Decimal(t), int(u)] for a, r, t, u in self.c.execute('SELECT account_id,requests,token_sum::text,unknown_requests FROM kitty_account_lifetime_usage')}
        for acct in set(expected_usage) | set(actual_usage):
            require(actual_usage.get(acct, [0, Decimal(0), 0]) == expected_usage[acct], f'Counter drift at {acct}')
        require(set(expected_usage).issubset(actual_usage), 'Missing usage counter row')

    def wait_block(self, waiter, blocker, timeout=1.5):
        deadline, last = time.monotonic() + timeout, []
        while time.monotonic() < deadline:
            last = self.c.execute('SELECT wait_event_type,wait_event,pg_blocking_pids(pid)::text FROM pg_stat_activity WHERE pid=$1::integer AND $2::integer=ANY(pg_blocking_pids(pid))', (waiter.pid, blocker.pid))
            if last:
                emit('observed_wait', schema=self.schema, waiter=waiter.pid, blocker=blocker.pid, detail=last)
                return
            time.sleep(0.02)
        raise AssertionError(f'Expected wait not observed ({waiter.pid} -> {blocker.pid}), last={last}')

    def finish(self, session, sql, params=None):
        start = time.monotonic()
        try:
            session.execute(sql, params)
            session.execute('COMMIT')
            return {'state': '00000', 'seconds': round(time.monotonic() - start, 3)}
        except PgError as exc:
            session.execute('ROLLBACK')
            return {'state': exc.state, 'seconds': round(time.monotonic() - start, 3), 'message': exc.fields.get('M'), 'detail': exc.fields.get('D'), 'context': exc.fields.get('W')}

    def serialized(self, pool, label, first, first_args, second, second_args, candidate_only_wait=False):
        self.a.execute('BEGIN')
        self.b.execute('BEGIN')
        self.a.execute(first, first_args)
        job = pool.submit(self.finish, self.b, second, second_args)
        try:
            if not candidate_only_wait:
                self.wait_block(self.b, self.a)
            else:
                require(job.result(timeout=WALL)['state'] == '00000', '949-only worker failed')
        finally:
            self.a.execute('COMMIT')
        outcome = job.result(timeout=WALL)
        require(outcome['state'] == '00000', f'Unexpected {label} lock failure: {outcome}')
        self.assert_state()
        emit('pass', test=label, candidate=self.candidate, outcome=outcome)

    def positive(self, pool):
        self.serialized(pool, 'permanent_vs_receipt_promotion',
                        'UPDATE kitty_permanent_usage SET input_tokens=input_tokens+7 WHERE request_key=$1',
                        ('request:seed-101',), PROMOTE, (101,), candidate_only_wait=True)
        require(self.c.scalar('SELECT count(*) FROM kitty_account_cost_legacy WHERE request_key=$1', ('request:seed-101',)) == '0', 'Promotion did not remove legacy')
        # Original 921 correction path executes in both connections.
        self.serialized(pool, 'usage_log_correction',
                        'UPDATE usage_logs SET input_tokens=41,actual_cost=1.25,account_stats_cost=3 WHERE id=$1', (201,),
                        'UPDATE usage_logs SET input_tokens=51,actual_cost=1.5,account_stats_cost=4 WHERE id=$1', (201,))
        self.serialized(pool, 'legacy_row_vs_receipt_promotion',
                        'UPDATE kitty_account_cost_legacy SET account_id=account_id WHERE request_key=$1',
                        ('request:seed-102',), PROMOTE, (102,))
        self.a.execute('BEGIN')
        self.b.execute('BEGIN')
        jobs = [pool.submit(self.finish, self.a, 'UPDATE kitty_procurement_receipts SET account_id=$1 WHERE request_key=$2', (30, 'request:seed-201')),
                pool.submit(self.finish, self.b, CAPTURE, (103,))]
        outcomes = [job.result(timeout=WALL) for job in jobs]
        require(all(x['state'] == '00000' for x in outcomes), f'Independent move failed: {outcomes}')
        self.assert_state()
        emit('pass', test='move_account_and_independent_permanent', candidate=self.candidate)
        self.c.execute('UPDATE kitty_procurement_receipts SET request_valuation=2 WHERE request_key=$1', ('request:seed-201',))
        self.c.execute('UPDATE kitty_procurement_receipts SET measurements=measurements WHERE request_key=$1', ('request:seed-201',))
        self.c.execute(CAPTURE, (104,))
        self.c.execute('UPDATE kitty_permanent_usage SET invalid_metrics=true WHERE request_key=$1', ('request:seed-104',))
        self.assert_state()
        self.c.execute('UPDATE kitty_permanent_usage SET invalid_metrics=false WHERE request_key=$1', ('request:seed-104',))
        for token in ('100', 100, Decimal('100.0')):
            value = '{"input":100.0,"output":2,"cache_read":4,"cache_write":3,"account_credits":2}' if isinstance(token, Decimal) else json.dumps(
                {'input': token, 'output': 2, 'cache_read': 4, 'cache_write': 3, 'account_credits': 2})
            self.c.execute('UPDATE kitty_procurement_receipts SET measurements=$1::jsonb WHERE request_key=$2', (value, 'request:seed-101'))
            self.assert_state()
        self.c.execute('UPDATE kitty_permanent_usage SET input_tokens=999 WHERE request_key=$1', ('request:seed-101',))
        self.assert_state()
        before = self.financial_fingerprint()
        self.c.execute('DELETE FROM usage_logs')
        self.assert_state()
        require(self.financial_fingerprint() == before, 'Retention changed financial history')
        if self.candidate:
            self.migrate(3)
            self.assert_state()
            require(self.financial_fingerprint() == before, 'Repeated 955 changed 949')
        emit('pass', test='late_evidence_precedence_validation_retention_rerun', candidate=self.candidate)

    def migration_order(self, pool):
        self.a.execute('BEGIN')
        self.a.execute('UPDATE kitty_permanent_usage SET input_tokens=input_tokens WHERE request_key=$1', ('request:seed-103',))
        self.b.execute('BEGIN')
        job = pool.submit(self.finish, self.b, self.migrations[3])
        try:
            self.wait_block(self.b, self.a)
            # Migration waits on permanent BEFORE receipt/legacy. The in-flight
            # writer must still be able to complete permanent -> receipt -> legacy.
            self.a.execute(PROMOTE, (103,))
            self.a.execute('COMMIT')
        finally:
            self.a.execute('ROLLBACK')
        outcome = job.result(timeout=WALL)
        require(outcome['state'] == '00000', f'Migration/write lock-order failure: {outcome}')
        self.assert_state()
        emit('pass', test='migration_source_table_lock_progress', outcome=outcome)

    def multi_key(self, pool):
        # 921 skips an UPDATE when all metrics already match. Make two test-only
        # permanent rows differ so ORIGINAL 921 must correct both and take locks.
        # Setup is autocommitted; no business rows or usage_logs triggers altered.
        self.c.execute('UPDATE kitty_permanent_usage SET input_tokens=input_tokens+1 WHERE request_key IN ($1,$2)',
                       ('request:seed-101', 'request:seed-102'))
        self.assert_state()
        self.a.execute('BEGIN')
        self.b.execute('BEGIN')
        self.a.execute(CAPTURE, (101,))
        bjob = pool.submit(self.finish, self.b, CAPTURE, (102,))
        # B must finish while A is still open with append-only journaling.
        # If regressed, start A's second key and observe the real PostgreSQL cycle.
        deadline = time.monotonic() + 1.5
        while time.monotonic() < deadline and not bjob.done():
            blocked = self.c.execute('SELECT 1 FROM pg_stat_activity WHERE pid=$1::integer AND $2::integer=ANY(pg_blocking_pids(pid))',
                                     (self.b.pid, self.a.pid))
            if blocked:
                emit('observed_wait', schema=self.schema, waiter=self.b.pid, blocker=self.a.pid)
                break
            time.sleep(0.02)
        ajob = pool.submit(self.finish, self.a, CAPTURE, (102,))
        outcomes = [ajob.result(timeout=WALL), bjob.result(timeout=WALL)]
        self.assert_state()
        emit('schedule', test='multi_key_permanent_same_account', candidate=self.candidate, outcomes=outcomes)
        return classify(outcomes)

    def journal(self, pool):
        require(self.candidate, 'Journal tests need 955')
        # Holding derived rows must NOT block any live source trigger.
        self.c.execute('BEGIN')
        self.c.execute('SELECT 1 FROM kitty_account_usage_snapshots FOR UPDATE')
        self.c.execute('SELECT 1 FROM kitty_account_lifetime_usage FOR UPDATE')
        self.a.execute('BEGIN')
        job = pool.submit(self.finish, self.a,
            'UPDATE kitty_permanent_usage SET input_tokens=input_tokens+1 WHERE request_key=$1', ('request:seed-101',))
        try:
            require(job.result(timeout=WALL)['state'] == '00000', 'Live source waited for derived row lock')
        finally:
            self.c.execute('ROLLBACK')
        self.assert_state()
        emit('pass', test='live_journal_does_not_lock_snapshot_or_counter')

        # Queue carries NEW account, snapshot carries OLD account until drained.
        self.c.execute('UPDATE kitty_procurement_receipts SET account_id=$1 WHERE request_key=$2', (30, 'request:seed-201'))
        pending = self.c.execute("""SELECT DISTINCT a.account_id FROM (VALUES (10),(20),(30)) a(account_id)
            WHERE EXISTS(SELECT FROM kitty_account_usage_events e LEFT JOIN kitty_account_usage_snapshots s
              ON (s.api_key_id,s.request_key)=(e.api_key_id,e.request_key)
              WHERE e.account_id=a.account_id OR s.account_id=a.account_id) ORDER BY 1""")
        require(pending == [['10'], ['30']], f'Pending must cover old/new accounts: {pending}')
        self.assert_state()
        emit('pass', test='pending_covers_new_and_old_snapshot_accounts')

        # One consumer holds its advisory xact lock through COMMIT, even when
        # finished processing. A competing processor returns 0, not a lock wait.
        self.c.execute('UPDATE kitty_permanent_usage SET input_tokens=input_tokens+1 WHERE request_key=$1', ('request:seed-103',))
        self.a.execute('BEGIN')
        require(int(self.a.scalar('SELECT kitty_drain_account_usage_events()')) > 0, 'First drainer consumed nothing')
        start = time.monotonic()
        require(self.b.scalar('SELECT kitty_drain_account_usage_events()') == '0', 'Competing drainer acquired singleton')
        require(time.monotonic()-start < 1.5, 'try advisory lock did not return promptly')
        self.a.execute('COMMIT')
        self.assert_state()
        emit('pass', test='single_consumer_try_lock_returns_immediately')

        for size in (0, 2049):
            try:
                self.c.scalar('SELECT kitty_drain_account_usage_events($1)', (size,))
            except PgError as exc:
                require(exc.state == '22023', f'Wrong batch validation: {exc}')
            else:
                raise AssertionError('Invalid batch accepted')
        self.c.execute("""INSERT INTO kitty_account_usage_events(api_key_id,request_key,account_id)
            SELECT 1,'request:seed-103',20 FROM generate_series(1,2049)""")
        require(self.c.scalar('SELECT kitty_drain_account_usage_events()') == '2048', 'Unbounded/default batch')
        require(self.c.scalar('SELECT count(*) FROM kitty_account_usage_events') == '1', 'Wrong residual batch')
        require(self.c.scalar('SELECT kitty_drain_account_usage_events(1)') == '1', 'Last event lost')
        self.assert_state()
        emit('pass', test='exact_2048_batch_limit_duplicate_identities_and_validation')

        # Pause the drainer AFTER it has captured high id, but BEFORE DELETE.
        # Commit the previously invisible low id while paused. A max-id delete
        # would now lose it; exact-array deletion MUST preserve the late event.
        self.c.execute("""
            CREATE FUNCTION verify_snapshot_gate() RETURNS trigger LANGUAGE plpgsql AS $$
            BEGIN PERFORM pg_advisory_xact_lock(955949,777); RETURN NEW; END; $$;
            CREATE TRIGGER verify_snapshot_gate BEFORE INSERT OR UPDATE ON kitty_account_usage_snapshots
                FOR EACH ROW EXECUTE FUNCTION verify_snapshot_gate();
        """)
        self.c.execute('SELECT pg_advisory_lock(955949,777)')
        self.a.execute('BEGIN')
        self.a.execute('UPDATE kitty_permanent_usage SET input_tokens=input_tokens+5 WHERE request_key=$1', ('request:seed-101',))
        low = int(self.a.scalar('SELECT min(id) FROM kitty_account_usage_events'))
        self.b.execute('UPDATE kitty_permanent_usage SET input_tokens=input_tokens+7 WHERE request_key=$1', ('request:seed-102',))
        high = int(self.c.scalar('SELECT min(id) FROM kitty_account_usage_events'))
        require(low < high, 'Fixture did not allocate lower uncommitted id')
        self.b.execute('BEGIN')
        job = pool.submit(self.finish, self.b, 'SELECT kitty_drain_account_usage_events()')
        try:
            self.wait_block(self.b, self.c)
            # Source commit cannot need snapshot/counter/advisory drain locks.
            self.a.execute('COMMIT')
        finally:
            self.c.execute('SELECT pg_advisory_unlock_all()')
        outcome = job.result(timeout=WALL)
        require(outcome['state'] == '00000', f'Late-commit drainer failed: {outcome}')
        self.c.execute('DROP TRIGGER verify_snapshot_gate ON kitty_account_usage_snapshots; DROP FUNCTION verify_snapshot_gate()')
        remaining = self.c.execute('SELECT id FROM kitty_account_usage_events ORDER BY id')
        require(remaining == [[str(low)]], f'Late commit event lost: low={low}, high={high}, left={remaining}')
        self.assert_state()
        emit('pass', test='lower_id_committed_after_capture_before_delete_preserved', low_id=low, captured_id=high)

    def opposite_moves(self, pool):
        # BEFORE UPDATE runs with the financial tuple locked; unchanged 949
        # chooses both first/second account updates. Advisory locks only pause.
        self.c.execute("""
            CREATE FUNCTION verify_financial_gate() RETURNS trigger LANGUAGE plpgsql AS $$
            BEGIN
              IF current_setting('kitty_verify.pause_account',true)=NEW.account_id::text THEN
                PERFORM pg_advisory_xact_lock(955949,NEW.account_id::integer);
              END IF;
              RETURN NEW;
            END; $$;
            CREATE TRIGGER verify_financial_gate BEFORE UPDATE ON kitty_account_lifetime_costs
            FOR EACH ROW EXECUTE FUNCTION verify_financial_gate();
        """)
        for acct in (10, 20):
            self.c.execute('SELECT pg_advisory_lock(955949,$1::integer)', (acct,))
        try:
            for pg, acct in ((self.a, 10), (self.b, 20)):
                pg.execute('BEGIN')
                pg.execute("SELECT set_config('kitty_verify.pause_account',$1,true)", (str(acct),))
            ajob = pool.submit(self.finish, self.a, 'UPDATE kitty_procurement_receipts SET account_id=$1 WHERE request_key=$2', (20, 'request:seed-201'))
            bjob = pool.submit(self.finish, self.b, 'UPDATE kitty_procurement_receipts SET account_id=$1 WHERE request_key=$2', (10, 'request:seed-202'))
            self.wait_block(self.a, self.c)
            self.wait_block(self.b, self.c)
        finally:
            self.c.execute('SELECT pg_advisory_unlock_all()')
        outcomes = [ajob.result(timeout=WALL), bjob.result(timeout=WALL)]
        self.c.execute('DROP TRIGGER verify_financial_gate ON kitty_account_lifetime_costs; DROP FUNCTION verify_financial_gate()')
        self.assert_state()
        emit('schedule', test='opposite_receipt_account_moves', candidate=self.candidate, outcomes=outcomes)
        return classify(outcomes)

    def close(self):
        for pg in reversed(self.sessions):
            pg.close()


def measured_tokens(m):
    if not isinstance(m, dict) or ('invalid_metrics' in m and m['invalid_metrics'] is not False):
        return None
    result = Decimal(0)
    for key in ('input', 'output', 'cache_read', 'cache_write'):
        value = m.get(key)
        if not isinstance(value, Decimal) or not value.is_finite() or value < 0 or value != value.to_integral_value():
            return None
        result += value
    return result


def classify(outcomes):
    states = [x['state'] for x in outcomes]
    require(all(x == '00000' or x in RISK_STATES for x in states), f'Unexpected SQLSTATEs: {states}')
    require('00000' in states, f'No surviving transaction: {states}')
    return 'deadlock' if '40P01' in states else 'timeout' if any(x in RISK_STATES for x in states) else 'clear'


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--backend', choices=('auto', 'local', 'docker'), default='auto')
    parser.add_argument('--pg-bin', help='PostgreSQL bin directory; always initializes a NEW temporary cluster')
    parser.add_argument('--image', default='postgres:16', help='Already local Docker image; never pulled here')
    parser.add_argument('--container', choices=('kitty7-preflight-pg',), help='Authorized isolated clone only; never stops/removes this container')
    args = parser.parse_args()
    migrations = [(ROOT / 'backend' / 'migrations' / name).read_text(encoding='utf-8') for name in MIGRATIONS]
    emit('inputs', migrations={name: hashlib.sha256(sql.encode()).hexdigest() for name, sql in zip(MIGRATIONS, migrations)},
         executed_at=time.strftime('%Y-%m-%dT%H:%M:%S%z'), credentials_read=False,
         limits={'statement_ms': 4000, 'lock_ms': 2500, 'deadlock_ms': 100, 'worker_seconds': WALL})
    runtime, results, exit_code = Runtime(args), {}, 1
    prefix = 'kitty_verify_' + uuid.uuid4().hex[:12]
    created_schemas = []
    emit('test_namespace', prefix=prefix, container=args.container)
    try:
        runtime.start()
        with futures.ThreadPoolExecutor(max_workers=2) as pool:
            for candidate in (False, True):
                group = 'candidate' if candidate else 'baseline'
                for case in (('positive', 'multi_key', 'opposite_moves', 'journal') if candidate else ('positive', 'multi_key', 'opposite_moves')):
                    schema = f'{prefix}_{group}_{case}'
                    created_schemas.append(schema)
                    suite = Suite(runtime.endpoint, schema, candidate, migrations)
                    try:
                        if case in ('positive', 'journal'):
                            getattr(suite, case)(pool)
                        else:
                            results[(group, case)] = getattr(suite, case)(pool)
                    finally:
                        suite.close()
            schema = prefix + '_migration_order'
            created_schemas.append(schema)
            suite = Suite(runtime.endpoint, schema, True, migrations)
            try:
                suite.migration_order(pool)
            finally:
                suite.close()
        risks, inherited_risks = [], []
        for case in ('multi_key', 'opposite_moves'):
            baseline, candidate = results[('baseline', case)], results[('candidate', case)]
            comparison = 'new_in_955' if baseline == 'clear' and candidate != 'clear' else 'inherited_from_949' if candidate != 'clear' else 'not_observed'
            emit('comparison', test=case, baseline=baseline, candidate=candidate, classification=comparison)
            if candidate != 'clear':
                target = inherited_risks if comparison == 'inherited_from_949' else risks
                target.append({'test': case, 'classification': comparison, 'outcome': candidate})
        emit('summary', verification_completed=True, regression_free=not risks, risks=risks, inherited_risks=inherited_risks,
             not_executed=['production', 'full application schema', 'production-sized backfill benchmark', 'all possible lock schedules'],
             note='No retries; finite successful schedules do not prove global deadlock freedom')
        exit_code = 2 if risks else 0
    except Exception as exc:
        emit('failure', type=type(exc).__name__, error=str(exc), verification_completed=False)
        exit_code = 1
    finally:
        if args.container and runtime.endpoint and created_schemas:
            cleanup = None
            try:
                cleanup = connect(runtime.endpoint)
                for schema in created_schemas:
                    require(schema.startswith(prefix + '_') and re.fullmatch(r'[a-z0-9_]+', schema), 'Unsafe cleanup target')
                    cleanup.execute(f'DROP SCHEMA IF EXISTS {schema} CASCADE')
                emit('cleanup', schemas_removed=created_schemas, container_preserved=True)
            except Exception as exc:
                emit('cleanup_failure', error=str(exc), test_schemas=created_schemas)
                exit_code = 1
            finally:
                if cleanup:
                    cleanup.close()
        if not runtime.close():
            exit_code = 1
    return exit_code


if __name__ == '__main__':
    raise SystemExit(main())
