// Isolated SQL regression; never connects to a server or production database.
// Install @electric-sql/pglite@0.6.0 into a TEMP npm prefix, then set
// KITTY_PGLITE_ROOT to that prefix. No repository dependency is installed.
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
const runtime = process.env.KITTY_PGLITE_ROOT;
if (!runtime || !path.isAbsolute(runtime)) throw new Error('Set KITTY_PGLITE_ROOT to the absolute isolated npm prefix');
const require = createRequire(path.join(runtime, 'package.json'));
const { PGlite } = require('@electric-sql/pglite');
import { readFile } from 'node:fs/promises';
import assert from 'node:assert/strict';
const repo = fileURLToPath(new URL('../..', import.meta.url));
const db = new PGlite();
await db.exec(`
CREATE TABLE usage_logs (
 id bigint PRIMARY KEY, api_key_id bigint NOT NULL, request_id text, account_id bigint NOT NULL,
 account_stats_cost numeric, total_cost numeric, actual_cost numeric, account_rate_multiplier numeric
);
CREATE TABLE kitty_procurement_receipts (
 id bigserial PRIMARY KEY, api_key_id bigint NOT NULL, request_key text NOT NULL,
 account_id bigint NOT NULL, source_log_id bigint, user_id bigint, observed_at timestamptz DEFAULT now(),
 measurements jsonb NOT NULL, request_valuation numeric,
 UNIQUE(api_key_id,request_key)
);
CREATE INDEX receipt_account_idx ON kitty_procurement_receipts(account_id,observed_at);
CREATE TABLE kitty_permanent_usage (
 source_log_id bigint, api_key_id bigint NOT NULL, request_key text NOT NULL, user_id bigint,
 input_tokens bigint NOT NULL DEFAULT 0, output_tokens bigint NOT NULL DEFAULT 0,
 cache_read_tokens bigint NOT NULL DEFAULT 0, cache_creation_tokens bigint NOT NULL DEFAULT 0,
 cache_creation_30m_tokens bigint NOT NULL DEFAULT 0, cache_creation_12h_tokens bigint NOT NULL DEFAULT 0,
 invalid_metrics boolean NOT NULL DEFAULT false, actual_cost numeric, total_cost numeric,
 observed_at timestamptz DEFAULT now(), captured_at timestamptz DEFAULT now(), billing_type smallint DEFAULT 0,
 PRIMARY KEY(api_key_id,request_key)
);
INSERT INTO kitty_procurement_receipts(api_key_id,request_key,account_id,measurements,request_valuation) VALUES
 (1,'request:r1',10,'{"input":100,"output":10,"cache_read":20,"cache_write":5,"cache_write_5m":999,"cache_write_12h":999,"account_credits":2}',1.5),
 (1,'request:r2',20,'{"input":10,"output":2,"cache_read":0,"account_credits":3}',2);
INSERT INTO usage_logs(id,api_key_id,request_id,account_id,account_stats_cost,total_cost,actual_cost,account_rate_multiplier) VALUES
 (101,1,'l1',10,NULL,4,2,0.5),(102,1,'l2',20,3,99,1.5,2);
INSERT INTO kitty_permanent_usage(source_log_id,api_key_id,request_key,input_tokens,output_tokens,cache_read_tokens,cache_creation_tokens)
 VALUES(101,1,'request:l1',800,100,50,50);
`);
const costMigration = await readFile(repo+'/backend/migrations/949_kitty_account_lifetime_costs.sql','utf8');
await db.exec('BEGIN;\n'+costMigration+'\nCOMMIT;');
const costs = async () => (await db.query('SELECT account_id,account_cost::text,user_cost::text FROM kitty_account_lifetime_costs ORDER BY account_id')).rows;
const initialCosts = await costs();
assert.deepEqual(initialCosts,[{account_id:10,account_cost:'4.0',user_cost:'3.5'},{account_id:20,account_cost:'9',user_cost:'3.5'}]);
console.log('REAL_949_FIXTURE_OK', JSON.stringify(initialCosts));
// Run the actual counter migration, not a reimplementation of its functions.
{
 const migration = process.argv[2] || path.join(repo, 'backend/migrations/955_kitty_account_lifetime_usage.sql');
 const sql = await readFile(migration, 'utf8');
 await db.exec('BEGIN;\n'+sql+'\nCOMMIT;');
 assert.deepEqual(await costs(),initialCosts,'the counter migration must not reprice financial history');
 console.log('COUNTER_MIGRATION_EXECUTED');
 const drain = async (limit=2048) => db.query('SELECT kitty_drain_account_usage_events($1) AS drained',[limit]);
 const count = async (id) => {
  await drain();
  const row = (await db.query('SELECT requests::text, token_sum::text, unknown_requests::text FROM kitty_account_lifetime_usage WHERE account_id=$1',[id])).rows[0];
  return row ? {requests:Number(row.requests), tokens:Number(row.unknown_requests)>0?null:Number(row.token_sum), unknown:Number(row.unknown_requests)} : null;
 };
 const check = async (id,requests,tokens,unknown=0) => assert.deepEqual(await count(id),{requests,tokens,unknown},'account '+id);
 await check(10,2,1135); await check(20,2,null,2);
 assert.equal((await db.query('SELECT count(*)::int AS n FROM kitty_account_usage_snapshots')).rows[0].n,4);
 console.log('BACKFILL_AND_UNKNOWNS_OK');
 await db.exec(`INSERT INTO kitty_procurement_receipts(api_key_id,request_key,account_id,source_log_id,measurements,request_valuation)
 VALUES(1,'request:l1',10,101,'{"input":900,"output":100,"cache_read":200,"cache_write":0,"account_credits":2}',2);`);
 await check(10,2,1335); assert.deepEqual(await costs(),initialCosts);
 assert.equal((await db.query("SELECT count(*)::int AS n FROM kitty_account_cost_legacy WHERE request_key='request:l1'")).rows[0].n,0);
 await db.exec("UPDATE kitty_procurement_receipts SET measurements=measurements WHERE request_key='request:l1'");
 await check(10,2,1335);
 console.log('PROMOTION_AND_REPEATED_CORRECTION_OK');
 await db.exec("UPDATE kitty_procurement_receipts SET account_id=30 WHERE request_key='request:r1'");
 await check(10,1,1200); await check(30,1,135);
 assert.deepEqual(await costs(),[{account_id:10,account_cost:'2.0',user_cost:'2.0'},{account_id:20,account_cost:'9',user_cost:'3.5'},{account_id:30,account_cost:'2',user_cost:'1.5'}]);
 console.log('ACCOUNT_REATTRIBUTION_OK');
 await db.exec("INSERT INTO kitty_permanent_usage(source_log_id,api_key_id,request_key,input_tokens,output_tokens,cache_read_tokens,cache_creation_tokens) VALUES(102,1,'request:l2',10,2,1,0)");
 await check(20,2,null,1);
 await db.exec(`UPDATE kitty_procurement_receipts SET measurements='{"input":10,"output":2,"cache_read":0,"cache_write":0,"account_credits":3}' WHERE request_key='request:r2'`);
 await check(20,2,25);
 await db.exec(`UPDATE kitty_procurement_receipts SET measurements='{"input":100.0,"output":2,"cache_read":0,"cache_write":0,"account_credits":3}' WHERE request_key='request:r2'`);
 await check(20,2,115);
 await db.exec(`UPDATE kitty_procurement_receipts SET measurements='{"input":"100","output":2,"cache_read":0,"cache_write":0,"account_credits":3}' WHERE request_key='request:r2'`);
 await check(20,2,null,1);
 await db.exec(`UPDATE kitty_procurement_receipts SET measurements='{"input":100,"output":2,"cache_read":0,"cache_write":0,"account_credits":3}' WHERE request_key='request:r2'`);
 await check(20,2,115);
 await db.exec("UPDATE kitty_permanent_usage SET invalid_metrics=true WHERE request_key='request:l2'");
 await check(20,2,null,1);
 await db.exec("UPDATE kitty_permanent_usage SET invalid_metrics=false WHERE request_key='request:l2'");
 await check(20,2,115);
 console.log('LATE_EVIDENCE_AND_INVALID_METRICS_OK');
 await db.exec("INSERT INTO kitty_permanent_usage(api_key_id,request_key,input_tokens,output_tokens,cache_read_tokens,cache_creation_tokens) VALUES(1,'request:r1',999,999,999,999)");
 await check(30,1,135);
 await db.exec("INSERT INTO kitty_permanent_usage(api_key_id,request_key,input_tokens,output_tokens,cache_read_tokens,cache_creation_tokens) VALUES(1,'request:orphan',1,2,3,4)");
 assert.equal((await db.query("SELECT count(*)::int AS n FROM kitty_account_usage_snapshots WHERE request_key='request:orphan'")).rows[0].n,0);
 await db.exec("INSERT INTO kitty_account_cost_legacy(api_key_id,request_key,account_id,account_cost,user_cost) VALUES(1,'request:l3',40,1,1)");
 await check(40,1,null,1);
 await db.exec("INSERT INTO kitty_permanent_usage(api_key_id,request_key,input_tokens,output_tokens,cache_read_tokens,cache_creation_tokens) VALUES(1,'request:l3',2,3,0,0)");
 await check(40,1,5);
 console.log('RECEIPT_PRECEDENCE_AND_ORPHAN_EXCLUSION_OK');
 await db.exec(`INSERT INTO kitty_procurement_receipts(api_key_id,request_key,account_id,measurements,request_valuation) VALUES(1,'request:r3',50,'{"input":5,"output":1,"cache_read":0,"cache_write":0,"account_credits":2}',0.5)`);
 await check(50,1,6);
 await db.exec(`INSERT INTO kitty_procurement_receipts(api_key_id,request_key,account_id,measurements,request_valuation) VALUES(1,'request:r3',50,'{"input":5,"output":1,"cache_read":0,"cache_write":0,"account_credits":2}',0.5) ON CONFLICT DO NOTHING`);
 await check(50,1,6);
 const beforeRerun=await db.query('SELECT * FROM kitty_account_lifetime_usage ORDER BY account_id');
 const beforeCosts=await costs();
 await db.exec('BEGIN;\n'+sql+'\nCOMMIT;');
 const normalize = rows => rows.map(row => ({...row, token_sum:BigInt(row.token_sum.split('.')[0]).toString()}));
 assert.deepEqual(normalize((await db.query('SELECT * FROM kitty_account_lifetime_usage ORDER BY account_id')).rows),normalize(beforeRerun.rows));
 assert.deepEqual(await costs(),beforeCosts);
 console.log('NEW_WRITES_AND_MIGRATION_IDEMPOTENCY_OK');
 for (const [json,valid] of [
  ['{"input":100.0,"output":2,"cache_read":0,"cache_write":0}',true],
  ['{"input":-1,"output":2,"cache_read":0,"cache_write":0}',false],
  ['{"input":0.5,"output":2,"cache_read":0,"cache_write":0}',false],
  ['{"input":1,"output":2,"cache_read":0}',false],
  ['{"input":1,"output":2,"cache_read":0,"cache_write":null}',false],
  ['{"input":1,"output":2,"cache_read":0,"cache_write":0,"invalid_metrics":true}',false],
 ]) {
  const value=(await db.query('SELECT kitty_account_usage_tokens($1::jsonb)::text AS tokens',[json])).rows[0].tokens;
  assert.equal(value!==null,valid,json);
 }
 console.log('TOKEN_VALIDATION_OK');

 const repository = await readFile(path.join(repo,'backend/internal/repository/usage_log_account_lifetime_usage_stats.go'),'utf8');
 const readSQL = repository.match(/const accountLifetimeUsageStatsSQL = `([\s\S]*?)`/)[1];
 const projected = await db.query(readSQL,[[10,20,30,40,50]]);
 assert.equal(projected.rows.length,5);
 assert.equal(Number(projected.rows.find(row=>Number(row.account_id)===20).tokens),115);
 await db.exec(`INSERT INTO kitty_procurement_receipts(api_key_id,request_key,account_id,measurements,request_valuation)
  VALUES(1,'request:pending-move',70,'{"input":5,"output":1,"cache_read":0,"cache_write":0,"account_credits":1}',1)`);
 let pending = await db.query(readSQL,[[70]]);
 assert.equal(pending.rows[0].requests,null,'new-account totals stay unknown while its event is pending');
 assert.equal(pending.rows[0].tokens,null);
 await drain();
 await db.exec("UPDATE kitty_procurement_receipts SET account_id=71 WHERE request_key='request:pending-move'");
 pending = await db.query(readSQL,[[70,71]]);
 assert.deepEqual(pending.rows.map(row=>[row.requests,row.tokens]),[[null,null],[null,null]],'both the new event account and old snapshot account stay unknown');
 await drain();
 const moved = await db.query(readSQL,[[70,71]]);
 assert.deepEqual(moved.rows.map(row=>[Number(row.requests),Number(row.tokens)]),[[0,0],[1,6]]);
 console.log('PENDING_ACCOUNT_MOVE_READS_UNKNOWN_UNTIL_DRAIN_OK');
 await db.exec(`INSERT INTO kitty_procurement_receipts(api_key_id,request_key,account_id,measurements,request_valuation)
  VALUES(1,'request:huge',60,'{"input":1e30,"output":1,"cache_read":0,"cache_write":0,"account_credits":1}',1)`);
 await drain();
 const huge = (await db.query(readSQL,[[60]])).rows[0];
 assert.equal(Number(huge.requests),1);
 assert.equal(huge.tokens,null,'API overflow must be unknown, not a negative value or a batch failure');
 const beforeRetention = normalize((await db.query('SELECT * FROM kitty_account_lifetime_usage ORDER BY account_id')).rows);
 const moneyBeforeRetention = await costs();
 await db.exec('DELETE FROM usage_logs');
 assert.deepEqual(normalize((await db.query('SELECT * FROM kitty_account_lifetime_usage ORDER BY account_id')).rows),beforeRetention);
 assert.deepEqual(await costs(),moneyBeforeRetention);
 console.log('REAL_READER_RANGE_AND_LOG_RETENTION_OK');
 console.log('ALL_COUNTER_SQL_TRANSITIONS_PASSED');

}
await db.close();
