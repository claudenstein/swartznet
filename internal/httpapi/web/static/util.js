// util.js — dependency-free helpers shared by every page module.
// No framework, no build step: this is a native ES module loaded over http.

// el builds a DOM node: el('div', {class:'x', onclick:fn}, [children|strings]).
export function el(tag, attrs = {}, children = []) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v == null || v === false) continue;
    if (k === 'class') n.className = v;
    else if (k === 'html') n.innerHTML = v;
    else if (k.startsWith('on') && typeof v === 'function') n.addEventListener(k.slice(2), v);
    else if (k === 'dataset') Object.assign(n.dataset, v);
    else n.setAttribute(k, v === true ? '' : v);
  }
  for (const c of [].concat(children)) {
    if (c == null || c === false) continue;
    n.appendChild(typeof c === 'string' || typeof c === 'number' ? document.createTextNode(String(c)) : c);
  }
  return n;
}

// clear removes every child of a node and returns it.
export function clear(node) {
  while (node.firstChild) node.removeChild(node.firstChild);
  return node;
}

// escapeHtml makes a string safe to drop into innerHTML.
export function escapeHtml(s) {
  return String(s == null ? '' : s)
    .replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;').replaceAll("'", '&#39;');
}

// humanBytes formats a byte count as B/KiB/MiB/GiB/TiB.
export function humanBytes(n) {
  n = Number(n) || 0;
  const u = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return (i === 0 ? n : n.toFixed(n < 10 ? 2 : 1)) + ' ' + u[i];
}

// fmtRate formats a bytes/second rate.
export function fmtRate(bps) {
  const v = Number(bps) || 0;
  return v <= 0 ? '—' : humanBytes(v) + '/s';
}

// pct renders a 0..1 progress fraction as an integer percentage.
export function pct(fraction) {
  return Math.round((Number(fraction) || 0) * 100) + '%';
}

// shortHex returns the first n hex chars (default 16) with an ellipsis.
export function shortHex(h, n = 16) {
  h = String(h || '');
  return h.length > n ? h.slice(0, n) + '…' : h;
}

export const isHex40 = (s) => /^[0-9a-fA-F]{40}$/.test(String(s || ''));
export const isHex64 = (s) => /^[0-9a-fA-F]{64}$/.test(String(s || ''));

// timeAgo renders an RFC3339 timestamp (or the Go zero time) as a relative
// string. The Go zero time (year 0001) means "never".
export function timeAgo(rfc3339) {
  if (!rfc3339 || rfc3339.startsWith('0001-01-01')) return 'never';
  const t = Date.parse(rfc3339);
  if (Number.isNaN(t)) return String(rfc3339);
  return relFromMillis(Date.now() - t);
}

// unixAgo renders a unix-seconds timestamp (0 ⇒ never) as a relative string.
export function unixAgo(secs) {
  const n = Number(secs) || 0;
  if (n <= 0) return 'never';
  return relFromMillis(Date.now() - n * 1000);
}

function relFromMillis(ms) {
  if (ms < 0) ms = 0;
  const s = Math.floor(ms / 1000);
  if (s < 60) return s + 's ago';
  const m = Math.floor(s / 60);
  if (m < 60) return m + 'm ago';
  const h = Math.floor(m / 60);
  if (h < 24) return h + 'h ago';
  return Math.floor(h / 24) + 'd ago';
}

// fmtFloat / fmtInt format scores by their block's declared type.
export const fmtFloat = (v) => (Number(v) || 0).toFixed(2);
export const fmtInt = (v) => String(Math.trunc(Number(v) || 0));

// badge builds a small labelled chip; kind adds a modifier class.
export function badge(text, kind) {
  return el('span', { class: 'badge' + (kind ? ' badge-' + kind : '') }, [String(text)]);
}

// toast shows a transient message bottom-center. kind: 'ok' | 'err' | ''.
let toastTimer = null;
export function toast(msg, kind = '') {
  let box = document.getElementById('toast');
  if (!box) {
    box = el('div', { id: 'toast' });
    document.body.appendChild(box);
  }
  box.className = 'toast-show' + (kind ? ' toast-' + kind : '');
  box.textContent = msg;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { box.className = ''; }, 4000);
}

// section builds a titled panel used across Status/Settings.
export function section(title, body) {
  return el('section', { class: 'panel' }, [el('h3', {}, [title]), body]);
}

// copyText copies a string to the clipboard, resolving true on success. Uses the
// async Clipboard API (available over http on localhost, a secure context) and
// falls back to a hidden textarea + execCommand for older/edge cases.
export async function copyText(text) {
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch { /* fall through to the legacy path */ }
  try {
    const ta = el('textarea', {}, [String(text)]);
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(ta);
    return ok;
  } catch { return false; }
}

// copyButton returns a small button that copies `text` to the clipboard and
// toasts feedback. Used to make identifiers (e.g. the publisher pubkey) copyable.
export function copyButton(text, label = 'Copy') {
  return el('button', { class: 'btn tiny', title: 'Copy to clipboard', onclick: async () => {
    const ok = await copyText(text);
    toast(ok ? 'copied to clipboard' : 'copy failed', ok ? 'ok' : 'err');
  } }, [label]);
}
