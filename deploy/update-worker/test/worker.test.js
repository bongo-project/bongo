import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { D1Fake } from './d1fake.js';
import worker, { handle, rollup, utcDay, retentionCutoff, parseCheck } from '../src/index.js';

const migration = readFileSync(new URL('../migrations/0001_init.sql', import.meta.url), 'utf8');
const ID = 'a'.repeat(32);
const NOW = Date.UTC(2026, 10, 27, 12, 0, 0);
const VARS = {
  LATEST_VERSION: '0.17.0',
  RELEASE_NOTES_URL: 'https://update.bongo-project.org/releases/0.17.0',
  SECURITY: 'false',
};

function env(extra = {}) {
  return { DB: new D1Fake(migration), ...VARS, ...extra };
}
function post(body, headers = {}, path = '/v1/check') {
  return new Request('https://update.bongo-project.org' + path, { method: 'POST', body, headers });
}
function check(version, id = ID) {
  return post(JSON.stringify({ version, instance_id: id }));
}
async function rows(e, table = 'seen') {
  return (await e.DB.prepare(`SELECT * FROM ${table} ORDER BY 1,2,3`).all()).results;
}
function stream(text) {
  const bytes = new TextEncoder().encode(text);
  return new ReadableStream({
    start(c) {
      c.enqueue(bytes.slice(0, 600));
      c.enqueue(bytes.slice(600));
      c.close();
    },
  });
}

test('validation rejects with 400 and counts nothing', async () => {
  const big = JSON.stringify({ version: '0.17.0', instance_id: ID, pad: 'x'.repeat(1100) });
  assert.ok(big.length > 1024);
  const streamed = new Request('https://x/v1/check', { method: 'POST', body: stream(big), duplex: 'half' });
  assert.equal(streamed.headers.get('content-length'), null);
  const cases = [
    ['over 1 KiB with Content-Length', post(big, { 'content-length': String(big.length) })],
    ['over 1 KiB streamed', streamed],
    ['invalid JSON', post('{nope')],
    ['array', post('[]')],
    ['null', post('null')],
    ['extra key', post(JSON.stringify({ version: '0.17.0', instance_id: ID, extra: 'x' }))],
    ['missing key', post(JSON.stringify({ version: '0.17.0' }))],
    ['non-string version', post(JSON.stringify({ version: 17, instance_id: ID }))],
    ['non-string id', post(JSON.stringify({ version: '0.17.0', instance_id: 5 }))],
    ['dev', check('dev')],
    ['two parts', check('0.17')],
    ['leading zero', check('01.2.3')],
    ['build metadata', check('0.17.0+meta')],
    ['too long', check('0.17.0-' + 'a'.repeat(60))],
    ['31 char id', check('0.17.0', 'a'.repeat(31))],
    ['33 char id', check('0.17.0', 'a'.repeat(33))],
    ['non-hex id', check('0.17.0', 'g'.repeat(32))],
  ];
  for (const [name, req] of cases) {
    const e = env();
    const res = await handle(req, e, NOW);
    assert.equal(res.status, 400, name);
    assert.equal(await res.text(), '{"error":"bad request"}', name);
    assert.deepEqual(await rows(e), [], name);
  }
});

test('boundary and normalisation', async () => {
  const e = env();
  const exact = JSON.stringify({ version: 'v0.17.0', instance_id: 'A'.repeat(32) });
  const padded = exact + ' '.repeat(1024 - exact.length);
  assert.equal(padded.length, 1024);
  const res = await handle(post(padded), e, NOW);
  assert.equal(res.status, 200);
  assert.deepEqual(await rows(e), [{ day: '2026-11-27', version: '0.17.0', instance_id: 'a'.repeat(32) }]);
  const tooBig = await handle(post(padded + ' '), env(), NOW);
  assert.equal(tooBig.status, 400);
  assert.deepEqual(parseCheck({ version: 'v1.2.3-rc.1', instance_id: ID }), { version: '1.2.3-rc.1', id: ID });
});

test('200 shape', async () => {
  const res = await handle(check('0.17.0'), env({ SECURITY: 'true' }), NOW);
  assert.equal(res.status, 200);
  assert.equal(res.headers.get('content-type'), 'application/json');
  assert.equal(res.headers.get('cache-control'), 'no-store');
  const body = await res.json();
  assert.deepEqual(Object.keys(body).sort(), ['latest', 'release_notes_url', 'security']);
  assert.equal(body.security, true);
  assert.equal(body.latest, '0.17.0');
});

test('bad variables give 503 and count nothing', async () => {
  const bad = [
    { LATEST_VERSION: 'v0.17.0' },
    { LATEST_VERSION: '' },
    { SECURITY: 'yes' },
    { SECURITY: undefined },
    { RELEASE_NOTES_URL: 'http://update.bongo-project.org/x' },
    { RELEASE_NOTES_URL: 'not a url' },
  ];
  for (const b of bad) {
    const e = env(b);
    const res = await handle(check('0.17.0'), e, NOW);
    assert.equal(res.status, 503, JSON.stringify(b));
    assert.deepEqual(await rows(e), []);
  }
});

test('method and path', async () => {
  const e = env();
  assert.equal((await handle(post('{}', {}, '/other'), e, NOW)).status, 404);
  assert.equal((await handle(post('{}', {}, '/v1/check/'), e, NOW)).status, 404);
  const get = await handle(new Request('https://x/v1/check'), e, NOW);
  assert.equal(get.status, 405);
  assert.equal(get.headers.get('allow'), 'POST');
  assert.equal((await handle(new Request('https://x/', { method: 'POST', body: '{}' }), e, NOW)).status, 404);
});

test('distinct counting', async () => {
  const e = env();
  await handle(check('0.17.0'), e, NOW);
  await handle(check('0.17.0'), e, NOW + 1000);
  assert.equal((await rows(e)).length, 1);
  await handle(check('0.17.0'), e, NOW + 86400000);
  assert.equal((await rows(e)).length, 2);
});

test('a D1 failure on insert still answers 200', async () => {
  const e = env();
  e.DB = {
    prepare() {
      throw new Error('boom');
    },
  };
  const res = await handle(check('0.17.0'), e, NOW);
  assert.equal(res.status, 200);
});

test('rollup counts, deletes past days, keeps today, is idempotent', async () => {
  const e = env();
  const day1 = Date.UTC(2026, 10, 25, 10);
  const day2 = Date.UTC(2026, 10, 26, 10);
  for (const [t, v, id] of [
    [day1, '0.17.0', 'a'.repeat(32)],
    [day1, '0.17.0', 'b'.repeat(32)],
    [day1, '0.16.0', 'c'.repeat(32)],
    [day2, '0.17.0', 'a'.repeat(32)],
    [NOW, '0.17.0', 'a'.repeat(32)],
  ]) {
    await handle(check(v, id), e, t);
  }
  await rollup(e.DB, NOW);
  assert.deepEqual(await rows(e, 'daily_counts'), [
    { day: '2026-11-25', version: '0.16.0', instances: 1 },
    { day: '2026-11-25', version: '0.17.0', instances: 2 },
    { day: '2026-11-26', version: '0.17.0', instances: 1 },
  ]);
  assert.deepEqual(await rows(e), [{ day: '2026-11-27', version: '0.17.0', instance_id: 'a'.repeat(32) }]);
  await rollup(e.DB, NOW);
  assert.equal((await rows(e, 'daily_counts'))[1].instances, 2);
  // A straggler for a past day is added, not lost.
  await handle(check('0.17.0', 'd'.repeat(32)), e, day1);
  await rollup(e.DB, NOW);
  assert.equal((await rows(e, 'daily_counts'))[1].instances, 3);
  // The scheduled handler is driven by scheduledTime.
  await worker.scheduled({ scheduledTime: NOW + 86400000 }, e);
  assert.deepEqual(await rows(e), []);
});

test('rollup is atomic', async () => {
  const e = env();
  await handle(check('0.17.0'), e, Date.UTC(2026, 10, 20));
  // The third statement (retention delete) fails after the first two ran.
  e.DB.db.exec("INSERT INTO daily_counts VALUES ('2000-01-01','0.1.0',1)");
  e.DB.db.exec('CREATE TRIGGER fail BEFORE DELETE ON daily_counts BEGIN SELECT RAISE(ABORT, \'x\'); END');
  await assert.rejects(rollup(e.DB, NOW));
  assert.equal((await rows(e)).length, 1);
  assert.equal((await rows(e, 'daily_counts')).length, 1);
});

test('utcDay and retention cutoff', () => {
  assert.equal(utcDay(Date.UTC(2026, 10, 27, 23, 59)), '2026-11-27');
  const cases = [
    [[2026, 10, 27], '2025-10-27'],
    [[2026, 2, 31], '2025-02-28'],
    [[2024, 2, 31], '2023-02-28'],
    [[2025, 2, 29], '2024-02-29'],
    [[2025, 0, 15], '2023-12-15'],
    [[2026, 0, 31], '2024-12-31'],
  ];
  for (const [[y, m, d], want] of cases) {
    assert.equal(retentionCutoff(Date.UTC(y, m, d, 5)), want, `${y}-${m + 1}-${d}`);
  }
});

test('retention deletes only days before the cutoff', async () => {
  const e = env();
  e.DB.db.exec("INSERT INTO daily_counts VALUES ('2025-10-26','0.1.0',1)");
  e.DB.db.exec("INSERT INTO daily_counts VALUES ('2025-10-27','0.1.0',1)");
  await rollup(e.DB, NOW);
  assert.deepEqual((await rows(e, 'daily_counts')).map((r) => r.day), ['2025-10-27']);
});

test('Content-Length over 1 KiB is refused before the body is read', async () => {
  const e = env();
  let pulled = false;
  const body = new ReadableStream({
    pull(c) {
      pulled = true;
      c.enqueue(new TextEncoder().encode(JSON.stringify({ version: '0.17.0', instance_id: ID })));
      c.close();
    },
  }, { highWaterMark: 0 });
  const req = {
    url: 'https://update.bongo-project.org/v1/check',
    method: 'POST',
    headers: new Headers({ 'content-length': '2000' }),
    body,
  };
  const res = await handle(req, e, NOW);
  assert.equal(res.status, 400);
  assert.equal(pulled, false);
  assert.deepEqual(await rows(e), []);
});

test('GET /releases/{version} redirects to the GitHub release', async () => {
  const e = env();
  const cases = [
    ['0.17.0', 'https://github.com/bongo-project/bongo/releases/tag/v0.17.0'],
    ['v0.17.0', 'https://github.com/bongo-project/bongo/releases/tag/v0.17.0'],
    ['1.2.3-rc.1', 'https://github.com/bongo-project/bongo/releases/tag/v1.2.3-rc.1'],
  ];
  for (const [v, want] of cases) {
    const res = await handle(new Request('https://update.bongo-project.org/releases/' + v), e, NOW);
    assert.equal(res.status, 302, v);
    assert.equal(res.headers.get('location'), want, v);
  }
  assert.deepEqual(await rows(e), []);
  assert.deepEqual(await rows(e, 'daily_counts'), []);
});

test('GET /releases/{version} refuses anything but strict semver', async () => {
  const e = env();
  for (const v of ['dev', '0.17', '01.2.3', '0.17.0+meta', '0.17.0/x', '..%2Fx', 'x'.repeat(80), '']) {
    const res = await handle(new Request('https://update.bongo-project.org/releases/' + v), e, NOW);
    assert.equal(res.status, v === '' ? 404 : 400, v);
    assert.equal(res.headers.get('location'), null, v);
  }
  const head = await handle(new Request('https://update.bongo-project.org/releases/0.17.0', { method: 'POST', body: '{}' }), e, NOW);
  assert.equal(head.status, 405);
  assert.equal(head.headers.get('allow'), 'GET, HEAD');
});
