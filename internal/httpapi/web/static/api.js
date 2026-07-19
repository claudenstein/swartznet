// api.js — the single fetch layer between the SPA and the daemon HTTP API.
// Two invariants live here so no page has to repeat them:
//   1. On any non-2xx, the body is PLAIN TEXT (never JSON) — read .text() and
//      throw an ApiError{status, message}. Only /search carries structured
//      errors, and those ride INSIDE a 200 (the swarm.error/dht.error strings).
//   2. Writes are same-origin JSON; the daemon's CSRF guard passes by
//      construction (loopback Host/Origin), so we add no auth header.

export class ApiError extends Error {
  constructor(status, message) {
    super(message || ('HTTP ' + status));
    this.name = 'ApiError';
    this.status = status;
  }
}

async function req(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  let resp;
  try {
    resp = await fetch(path, opts);
  } catch (e) {
    throw new ApiError(0, 'network error: ' + e.message);
  }
  if (resp.status === 204) return null;
  if (!resp.ok) {
    let text = '';
    try { text = (await resp.text()).trim(); } catch { /* ignore */ }
    throw new ApiError(resp.status, text || ('HTTP ' + resp.status));
  }
  const ct = resp.headers.get('content-type') || '';
  if (ct.includes('application/json')) return resp.json();
  return resp.text();
}

const get = (p) => req('GET', p);

// ---- reads ----
export const getHealthz = () => get('/healthz');
export const getStatus = () => get('/status');
export const getPublish = () => get('/publish');
export const getTorrents = () => get('/torrents');
export const getFiles = (ih) => get(`/torrents/${ih}/files`);
export const getIndexStats = () => get('/index/stats');
export const getAggregate = () => get('/aggregate');
export const getCapabilities = () => get('/capabilities');
export const getRateLimit = () => get('/config/rate-limit');
export const getQueue = () => get('/config/queue');
export const getCompanion = () => get('/companion');

// ---- writes ----
export const addTorrent = (uri) => req('POST', '/torrent', { uri });
export const pauseTorrent = (ih) => req('POST', `/torrents/${ih}/pause`);
export const resumeTorrent = (ih) => req('POST', `/torrents/${ih}/resume`);
export const removeTorrent = (ih, forget) =>
  req('DELETE', `/torrents/${ih}` + (forget ? '?forget=1' : ''));
export const setIndexing = (ih, enabled) => req('POST', `/torrents/${ih}/indexing`, { enabled });
export const setFilePriority = (ih, index, priority) =>
  req('POST', `/torrents/${ih}/files/${index}/priority`, { priority });
export const search = (body) => req('POST', '/search', body);
export const confirm = (infohash) => req('POST', '/confirm', { infohash });
export const flag = (infohash) => req('POST', '/flag', { infohash });
export const patchCapabilities = (patch) => req('PATCH', '/capabilities', patch);
export const patchRateLimit = (patch) => req('PATCH', '/config/rate-limit', patch);
export const patchQueue = (patch) => req('PATCH', '/config/queue', patch);
export const refreshCompanion = () => req('POST', '/companion/refresh');
export const followPublisher = (pubkey, label) =>
  req('POST', '/companion/follow', label ? { pubkey, label } : { pubkey });
export const unfollowPublisher = (pubkey) => req('POST', '/companion/unfollow', { pubkey });
