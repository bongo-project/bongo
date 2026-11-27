// Bongo update endpoint: POST /v1/check answers with the current release and
// counts distinct instance IDs per version per UTC day. See README.md for
// exactly what is stored. This file reads nothing about the client beyond
// the two body fields: no IP address, no headers, no request metadata.

const MAX_BODY = 1024;
const SEMVER = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$/;
const HEX32 = /^[0-9a-fA-F]{32}$/;
const MAX_VERSION = 64;
const BAD = '{"error":"bad request"}';

function json(body, status, extra = {}) {
  return new Response(body, {
    status,
    headers: { 'Content-Type': 'application/json', 'Cache-Control': 'no-store', ...extra },
  });
}

// parseCheck validates a decoded body and returns {version, id}, or null.
// The body must hold exactly the two string fields version and instance_id.
export function parseCheck(obj) {
  if (obj === null || typeof obj !== 'object' || Array.isArray(obj)) return null;
  const keys = Object.keys(obj);
  if (keys.length !== 2 || !keys.includes('version') || !keys.includes('instance_id')) return null;
  if (typeof obj.version !== 'string' || typeof obj.instance_id !== 'string') return null;
  let version = obj.version;
  if (version.startsWith('v')) version = version.slice(1);
  if (version.length > MAX_VERSION || !SEMVER.test(version)) return null;
  if (!HEX32.test(obj.instance_id)) return null;
  return { version, id: obj.instance_id.toLowerCase() };
}

export function utcDay(ms) {
  return new Date(ms).toISOString().slice(0, 10);
}

// retentionCutoff is the same day of the month 13 months earlier, clamped to
// that month's last day. Days strictly before it are deleted.
export function retentionCutoff(ms) {
  const d = new Date(ms);
  let y = d.getUTCFullYear();
  let m = d.getUTCMonth() - 13;
  while (m < 0) {
    m += 12;
    y -= 1;
  }
  const last = new Date(Date.UTC(y, m + 1, 0)).getUTCDate();
  const day = Math.min(d.getUTCDate(), last);
  return new Date(Date.UTC(y, m, day)).toISOString().slice(0, 10);
}

// readBody reads at most MAX_BODY bytes and returns the text, or null when
// the body is larger. A chunked body with no Content-Length is bounded too.
async function readBody(request) {
  const declared = request.headers.get('content-length');
  if (declared !== null && Number(declared) > MAX_BODY) return null;
  if (!request.body) return '';
  const reader = request.body.getReader();
  const chunks = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    total += value.byteLength;
    if (total > MAX_BODY) {
      await reader.cancel();
      return null;
    }
    chunks.push(value);
  }
  const all = new Uint8Array(total);
  let off = 0;
  for (const c of chunks) {
    all.set(c, off);
    off += c.byteLength;
  }
  return new TextDecoder('utf-8', { fatal: true }).decode(all);
}

function configValid(env) {
  if (typeof env.LATEST_VERSION !== 'string' || !SEMVER.test(env.LATEST_VERSION)) return false;
  if (env.SECURITY !== 'true' && env.SECURITY !== 'false') return false;
  try {
    return new URL(env.RELEASE_NOTES_URL).protocol === 'https:';
  } catch {
    return false;
  }
}

const RELEASES = '/releases/';
const RELEASE_REPO = 'https://github.com/bongo-project/bongo/releases/tag/v';

// release answers GET /releases/{version} with a redirect to the GitHub
// release page. It counts nothing and stores nothing.
function release(request, pathname) {
  if (request.method !== 'GET' && request.method !== 'HEAD') {
    return json('{"error":"method not allowed"}', 405, { Allow: 'GET, HEAD' });
  }
  let version = pathname.slice(RELEASES.length);
  if (version.startsWith('v')) version = version.slice(1);
  if (version.length > MAX_VERSION || !SEMVER.test(version)) return json(BAD, 400);
  return new Response(null, {
    status: 302,
    headers: { Location: RELEASE_REPO + version, 'Cache-Control': 'no-store' },
  });
}

export async function handle(request, env, nowMs) {
  const url = new URL(request.url);
  if (url.pathname.startsWith(RELEASES) && url.pathname.length > RELEASES.length) {
    return release(request, url.pathname);
  }
  if (url.pathname !== '/v1/check') return json('{"error":"not found"}', 404);
  if (request.method !== 'POST') return json('{"error":"method not allowed"}', 405, { Allow: 'POST' });
  if (!configValid(env)) return json('{"error":"unavailable"}', 503);

  let parsed = null;
  try {
    const text = await readBody(request);
    if (text !== null) parsed = parseCheck(JSON.parse(text));
  } catch {
    parsed = null;
  }
  if (parsed === null) return json(BAD, 400);

  // Counting is best effort: the client's answer comes first.
  try {
    await env.DB.prepare('INSERT OR IGNORE INTO seen(day, version, instance_id) VALUES (?1, ?2, ?3)')
      .bind(utcDay(nowMs), parsed.version, parsed.id)
      .run();
  } catch {
    // Nothing is logged.
  }
  return json(
    JSON.stringify({
      latest: env.LATEST_VERSION,
      release_notes_url: env.RELEASE_NOTES_URL,
      security: env.SECURITY === 'true',
    }),
    200,
  );
}

// rollup writes the counts of every past day and deletes those days' ID sets
// in one transaction, then drops counts past the retention window. Today's
// set is never touched.
export async function rollup(db, nowMs) {
  const today = utcDay(nowMs);
  const cutoff = retentionCutoff(nowMs);
  await db.batch([
    db
      .prepare(
        'INSERT INTO daily_counts(day, version, instances) ' +
          'SELECT day, version, COUNT(*) FROM seen WHERE day < ?1 GROUP BY day, version ' +
          'ON CONFLICT(day, version) DO UPDATE SET instances = instances + excluded.instances',
      )
      .bind(today),
    db.prepare('DELETE FROM seen WHERE day < ?1').bind(today),
    db.prepare('DELETE FROM daily_counts WHERE day < ?1').bind(cutoff),
  ]);
}

export default {
  async fetch(request, env) {
    return handle(request, env, Date.now());
  },
  async scheduled(event, env) {
    await rollup(env.DB, event.scheduledTime);
  },
};
