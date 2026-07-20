// settings.js — the Settings tab: bandwidth + queue caps, the operator Sharing
// (capabilities) bits, a read-only services/publisher view, and About. Saves use
// pointer-merge: only CHANGED fields are sent, and the echoed document (which may
// clamp values) is authoritative — we repopulate from the response.
import * as api from '../api.js';
import { el, clear, section, toast, copyButton } from '../util.js';

export function render(container) {
  const view = el('div', { class: 'settings' }, ['Loading…']);

  // Live copies of the last-read documents so a save can diff against them.
  let rate = { upload_bps: 0, download_bps: 0 };
  let queue = { max_active_downloads: 0 };
  let caps = { share_local: 0, file_hits: false, content_hits: false, publisher: false, services: '' };

  function numInput(id, val) {
    return el('input', { type: 'number', id, min: 0, value: Number(val) || 0, class: 'num-in' });
  }

  async function saveRate() {
    const up = Number(container.querySelector('#up').value);
    const dn = Number(container.querySelector('#dn').value);
    const patch = {};
    if (up !== rate.upload_bps) patch.upload_bps = up;
    if (dn !== rate.download_bps) patch.download_bps = dn;
    if (!Object.keys(patch).length) { toast('no changes', ''); return; }
    try { rate = await api.patchRateLimit(patch); toast('rate limits saved', 'ok'); await load(); }
    catch (e) { toast(e.message, 'err'); }
  }

  async function saveQueue() {
    const m = Number(container.querySelector('#maxdl').value);
    if (m === queue.max_active_downloads) { toast('no changes', ''); return; }
    try { queue = await api.patchQueue({ max_active_downloads: m }); toast('queue saved', 'ok'); await load(); }
    catch (e) { toast(e.message, 'err'); }
  }

  async function saveSharing() {
    const share = Number(container.querySelector('#share').value);
    const fh = container.querySelector('#fh').checked;
    const ch = container.querySelector('#ch').checked;
    const patch = {};
    if (share !== caps.share_local) patch.share_local = share;
    if (fh !== caps.file_hits) patch.file_hits = fh;
    if (ch !== caps.content_hits) patch.content_hits = ch;
    if (!Object.keys(patch).length) { toast('no changes', ''); return; }
    try { caps = await api.patchCapabilities(patch); toast('sharing saved', 'ok'); await load(); }
    catch (e) { toast(e.message, 'err'); }
  }

  function rateSection() {
    return section('Bandwidth', el('div', {}, [
      el('div', { class: 'field' }, [el('label', {}, ['Upload B/s (0 = unlimited)']), numInput('up', rate.upload_bps)]),
      el('div', { class: 'field' }, [el('label', {}, ['Download B/s (0 = unlimited)']), numInput('dn', rate.download_bps)]),
      el('button', { class: 'btn primary', onclick: saveRate }, ['Save bandwidth']),
    ]));
  }

  function queueSection() {
    return section('Queue', el('div', {}, [
      el('div', { class: 'field' }, [el('label', {}, ['Max active downloads (0 = unlimited)']), numInput('maxdl', queue.max_active_downloads)]),
      el('button', { class: 'btn primary', onclick: saveQueue }, ['Save queue']),
    ]));
  }

  function sharingSection() {
    const share = el('select', { id: 'share' });
    [['0', "Off — don't answer"], ['1', 'In-swarm peers only'], ['2', 'Full local index']].forEach(([v, t]) =>
      share.appendChild(el('option', { value: v, selected: String(caps.share_local) === v }, [t])));
    return section('Sharing (sn_search capabilities)', el('div', {}, [
      el('div', { class: 'field' }, [el('label', {}, ['Answer local-index queries']), share]),
      el('label', { class: 'check' }, [el('input', { type: 'checkbox', id: 'fh', checked: caps.file_hits }), ' Share file-name hits']),
      el('label', { class: 'check' }, [el('input', { type: 'checkbox', id: 'ch', checked: caps.content_hits }), ' Share content hits']),
      el('button', { class: 'btn primary', onclick: saveSharing }, ['Save sharing']),
      el('div', { class: 'readonly' }, [
        el('div', {}, ['Publisher bit (daemon-owned): ', el('b', {}, [caps.publisher ? 'on' : 'off'])]),
        el('div', {}, ['Live services mask: ', el('code', {}, [caps.services || '—'])]),
      ]),
    ]));
  }

  async function aboutSection() {
    let version = '', pubkey = '';
    try { version = (await api.getHealthz()).version || ''; } catch { /* ignore */ }
    try { pubkey = (await api.getStatus()).publisher?.pubkey || ''; } catch { /* ignore */ }
    return section('About', el('div', {}, [
      el('div', {}, ['SwartzNet ', el('b', {}, [version || 'unknown'])]),
      pubkey
        ? el('div', { class: 'pubkey-row' }, ['Publisher: ', el('code', { class: 'selectable' }, [pubkey]), copyButton(pubkey)])
        : el('div', { class: 'muted' }, ['No publisher identity']),
      el('div', { class: 'muted' }, ['Apache-2.0 (SwartzNet) · engine anacrolix/torrent: MPL-2.0']),
    ]));
  }

  async function load() {
    const [r, q, c] = await Promise.all([
      api.getRateLimit().catch(() => rate),
      api.getQueue().catch(() => queue),
      api.getCapabilities().catch(() => caps),
    ]);
    rate = r; queue = q; caps = c;
    clear(view).append(rateSection(), queueSection(), sharingSection(), await aboutSection());
  }

  clear(container).append(view);
  return { async start() { await load(); }, stop() {} };
}
