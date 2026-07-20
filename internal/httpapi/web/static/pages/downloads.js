// downloads.js — the Downloads tab: live torrent list, add-magnet, per-row
// pause/resume/remove, indexing toggle, and a lazy per-file priority drawer.
import * as api from '../api.js';
import { el, clear, humanBytes, fmtRate, pct, shortHex, isHex40, badge, toast } from '../util.js';

const POLL_MS = 1000;

export function render(container) {
  const list = el('div', { class: 'dl-list' }, ['Loading…']);
  const expanded = new Set();     // infohashes whose file drawer is open
  const filesCache = new Map();   // infohash → files[]
  let timer = null, busy = false, stopped = false;

  const addRow = el('div', { class: 'add-row' }, [
    el('input', { type: 'text', id: 'magnet', placeholder: 'magnet:?xt=urn:btih:… (magnet URI)', class: 'grow' }),
    el('button', { class: 'btn primary', onclick: onAdd }, ['Add']),
    el('button', { class: 'btn', onclick: () => toggleCreate() }, ['Create torrent']),
  ]);

  async function onAdd() {
    const input = container.querySelector('#magnet');
    const uri = input.value.trim();
    if (!uri) { toast('enter a magnet URI', 'err'); return; }
    try {
      const r = await api.addTorrent(uri);
      input.value = '';
      toast('added ' + shortHex(r.infohash, 16), 'ok');
      await refresh();
    } catch (e) { toast(e.message, 'err'); }
  }

  // ---- create torrent (from a file/folder on the DAEMON machine) ----
  const cSrc = el('input', { type: 'text', placeholder: '/path/to/file-or-folder', class: 'grow' });
  const cOut = el('input', { type: 'text', placeholder: '/path/to/output.torrent', class: 'grow' });
  const cTrk = el('input', { type: 'text', placeholder: 'optional trackers (space or comma separated)', class: 'grow' });
  const cCmt = el('input', { type: 'text', placeholder: 'optional comment', class: 'grow' });
  const cPriv = el('input', { type: 'checkbox' });
  const cSign = el('input', { type: 'checkbox' });
  const cSeed = el('input', { type: 'checkbox' });
  cSeed.checked = true;
  const cSignLabel = el('label', {}, [cSign, ' Sign with my identity']);
  const createSubmit = el('button', { class: 'btn primary', onclick: onCreate }, ['Create']);
  const createForm = el('div', { class: 'create-form', style: 'display:none' }, [
    el('div', { class: 'muted small' }, ['Paths are on the machine running the daemon (localhost) — the daemon does the hashing.']),
    el('div', { class: 'add-row' }, [el('span', { class: 'flabel' }, ['Source']), cSrc]),
    el('div', { class: 'add-row' }, [el('span', { class: 'flabel' }, ['Output']), cOut]),
    el('div', { class: 'add-row' }, [el('span', { class: 'flabel' }, ['Trackers']), cTrk]),
    el('div', { class: 'add-row' }, [el('span', { class: 'flabel' }, ['Comment']), cCmt]),
    el('div', { class: 'create-opts' }, [
      el('label', {}, [cPriv, ' Private (BEP-27: no DHT/PEX)']),
      cSignLabel,
      el('label', {}, [cSeed, ' Seed after creating']),
    ]),
    el('div', { class: 'add-row' }, [createSubmit, el('button', { class: 'btn', onclick: () => toggleCreate(false) }, ['Cancel'])]),
  ]);

  // Auto-derive the output path from the source, re-deriving on change unless the
  // user typed their own (matches the native GUI's create dialog).
  let lastDerived = '';
  cSrc.addEventListener('input', () => {
    if (cOut.value !== lastDerived) return;
    const s = cSrc.value.trim().replace(/[/\\]+$/, '');
    lastDerived = s ? s + '.torrent' : '';
    cOut.value = lastDerived;
  });

  let createOpen = false, signProbed = false;
  async function toggleCreate(open) {
    createOpen = open === undefined ? !createOpen : open;
    createForm.style.display = createOpen ? '' : 'none';
    if (createOpen && !signProbed) {
      signProbed = true; // disable "Sign" when no identity is loaded
      try {
        const pk = (await api.getStatus()).publisher?.pubkey;
        if (!pk) { cSign.checked = false; cSign.disabled = true; cSignLabel.appendChild(el('span', { class: 'muted small' }, [' (no identity)'])); }
      } catch { /* leave enabled — the API fails closed on sign-without-identity */ }
    }
  }

  async function onCreate() {
    const root = cSrc.value.trim(), output = cOut.value.trim();
    if (!root || !output) { toast('source and output paths are required', 'err'); return; }
    createSubmit.disabled = true;
    const prev = createSubmit.textContent;
    createSubmit.textContent = 'Creating…';
    try {
      const r = await api.createTorrent({
        root, output,
        trackers: cTrk.value.split(/[\s,]+/).filter(Boolean),
        comment: cCmt.value.trim(),
        private: cPriv.checked, sign: cSign.checked, seed: cSeed.checked,
      });
      if (r.seed_error) {
        // Created, but the seed-in-place step failed — surface it as a warning.
        toast('created ' + shortHex(r.infohash, 16) + ' but seeding failed: ' + r.seed_error, 'err');
      } else {
        toast('created ' + shortHex(r.infohash, 16) + (r.seeded ? ' — seeding' : ''), 'ok');
      }
      cSrc.value = ''; cOut.value = ''; cTrk.value = ''; cCmt.value = ''; cPriv.checked = false; lastDerived = '';
      toggleCreate(false);
      await refresh();
    } catch (e) { toast(e.message, 'err'); }
    finally { createSubmit.disabled = false; createSubmit.textContent = prev; }
  }

  async function rowAction(fn, label) {
    try { await fn(); await refresh(); toast(label + ' ok', 'ok'); }
    catch (e) { toast(e.message, 'err'); }
  }

  async function toggleFiles(ih) {
    if (expanded.has(ih)) { expanded.delete(ih); }
    else {
      expanded.add(ih);
      try { filesCache.set(ih, (await api.getFiles(ih)).files || []); }
      catch (e) { toast(e.message, 'err'); expanded.delete(ih); }
    }
    await refresh();
  }

  async function setPrio(ih, index, priority) {
    try {
      await api.setFilePriority(ih, index, priority);
      filesCache.set(ih, (await api.getFiles(ih)).files || []);
      await refresh();
    } catch (e) { toast(e.message, 'err'); }
  }

  function fileDrawer(ih) {
    const files = filesCache.get(ih) || [];
    const rows = files.map((f) => el('tr', {}, [
      el('td', { class: 'mono' }, [f.display_path || f.path]),
      el('td', { class: 'num' }, [humanBytes(f.length)]),
      el('td', { class: 'num' }, [pct(f.progress)]),
      el('td', {}, [prioSelect(ih, f)]),
    ]));
    return el('div', { class: 'drawer' }, [
      el('table', { class: 'files' }, [
        el('thead', {}, [el('tr', {}, [el('th', {}, ['File']), el('th', { class: 'num' }, ['Size']),
          el('th', { class: 'num' }, ['%']), el('th', {}, ['Priority'])])]),
        el('tbody', {}, rows.length ? rows : [el('tr', {}, [el('td', { colspan: 4 }, ['(no files yet)'])])]),
      ]),
    ]);
  }

  function prioSelect(ih, f) {
    const sel = el('select', { class: 'prio' });
    for (const p of ['none', 'normal', 'high']) {
      sel.appendChild(el('option', { value: p, selected: f.priority === p }, [p]));
    }
    sel.addEventListener('change', () => setPrio(ih, f.index, sel.value));
    return sel;
  }

  function torrentRow(t) {
    const ih = t.infohash;
    const title = t.name || shortHex(ih, 16);
    const badges = [badge(t.status, t.status)];
    if (t.queued) badges.push(badge('queued'));
    if (t.signed_by) badges.push(badge('✓ ' + shortHex(t.signed_by, 8), t.trusted_publisher ? 'trusted' : ''));
    if (t.indexing) badges.push(badge(`idx ${t.indexed_files || 0}/${t.files || 0}`, 'idx'));

    const bar = el('div', { class: 'progress' }, [
      el('div', { class: 'progress-fill', style: `width:${Math.round((t.progress || 0) * 100)}%` }),
    ]);

    const controls = el('div', { class: 'row-controls' }, [
      t.paused
        ? el('button', { class: 'btn', onclick: () => rowAction(() => api.resumeTorrent(ih), 'resume') }, ['Resume'])
        : el('button', { class: 'btn', onclick: () => rowAction(() => api.pauseTorrent(ih), 'pause') }, ['Pause']),
      el('button', { class: 'btn', onclick: () => rowAction(() => api.setIndexing(ih, !t.indexing), 'indexing') },
        [t.indexing ? 'Unindex' : 'Index']),
      el('button', { class: 'btn', onclick: () => onRemove(ih) }, ['Remove']),
      el('button', { class: 'btn link', onclick: () => toggleFiles(ih) },
        [expanded.has(ih) ? '▾ files' : '▸ files']),
    ]);

    const meta = el('div', { class: 'row-meta' }, [
      `${humanBytes(t.bytes_completed)} / ${humanBytes(t.size)} · ${pct(t.progress)}`,
      el('span', { class: 'sep' }, ['·']),
      `${t.active_peers} peer(s)`,
      el('span', { class: 'sep' }, ['·']),
      `↓ ${fmtRate(t.download_rate)}  ↑ ${fmtRate(t.upload_rate)}`,
    ]);

    return el('div', { class: 'torrent' + (t.paused ? ' paused' : '') }, [
      el('div', { class: 'row-head' }, [el('div', { class: 'row-title', title: ih }, [title]),
        el('div', { class: 'row-badges' }, badges)]),
      bar, meta, controls,
      expanded.has(ih) ? fileDrawer(ih) : null,
    ]);
  }

  async function onRemove(ih) {
    // Two-step so Cancel on the first prompt truly aborts (files on disk are
    // always kept either way; the second prompt only chooses index forgetting).
    if (!confirm('Remove this torrent?\n\nThe downloaded files on disk are always kept.')) return;
    const forget = confirm('Also forget its search-index entries?\n\nOK = forget the index docs · Cancel = keep them.');
    await rowAction(() => api.removeTorrent(ih, forget), 'remove');
  }

  async function refresh() {
    if (busy) return;
    busy = true;
    try {
      const r = await api.getTorrents();
      const torrents = r.torrents || [];
      clear(list);
      if (!torrents.length) { list.appendChild(el('p', { class: 'empty' }, ['No torrents. Add a magnet above.'])); }
      else torrents.forEach((t) => list.appendChild(torrentRow(t)));
    } catch (e) {
      clear(list); list.appendChild(el('p', { class: 'error' }, ['Failed to load: ' + e.message]));
    } finally { busy = false; }
  }

  clear(container).append(addRow, createForm, list);

  return {
    // Guard against a tab switch that calls stop() while the initial refresh()
    // is still in flight: without the flag, the interval would be created after
    // stop() ran and leak forever.
    async start() { await refresh(); if (stopped) return; timer = setInterval(refresh, POLL_MS); },
    stop() { stopped = true; clearInterval(timer); timer = null; },
  };
}
