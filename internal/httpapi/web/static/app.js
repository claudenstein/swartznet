// SwartzNet local web UI — vanilla JS, no build step.
//
// Talks to the same loopback HTTP API the CLI uses
// (POST /search, GET /status, POST /confirm, POST /flag,
// POST /torrent in M8c). Designed to work with no
// dependencies and degrade gracefully if any endpoint
// fails — the daemon's text-mode CLI is always the
// fallback the user already has.

'use strict';

(function () {

  // ---------- tab switching ----------

  const tabs = document.querySelectorAll('.tab');
  const panels = {
    search: document.getElementById('panel-search'),
    add: document.getElementById('panel-add'),
    downloads: document.getElementById('panel-downloads'),
    companion: document.getElementById('panel-companion'),
    status: document.getElementById('panel-status'),
    settings: document.getElementById('panel-settings'),
  };

  tabs.forEach(t => t.addEventListener('click', () => {
    tabs.forEach(x => x.classList.remove('active'));
    t.classList.add('active');
    Object.values(panels).forEach(p => p.classList.add('hidden'));
    panels[t.dataset.tab].classList.remove('hidden');
    if (t.dataset.tab === 'status') refreshStatus();
    if (t.dataset.tab === 'downloads') refreshDownloads();
    if (t.dataset.tab === 'companion') refreshCompanion();
  }));

  // ---------- helpers ----------

  function humanBytes(n) {
    if (!n || n < 0) return '0 B';
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
    let u = 0;
    let v = n;
    while (v >= 1024 && u < units.length - 1) {
      v /= 1024;
      u += 1;
    }
    return v.toFixed(v < 10 ? 1 : 0) + ' ' + units[u];
  }

  function elt(tag, attrs, children) {
    const el = document.createElement(tag);
    if (attrs) {
      for (const [k, v] of Object.entries(attrs)) {
        if (k === 'class') {
          el.className = v;
        } else if (k === 'text') {
          el.textContent = v;
        } else if (k.startsWith('on') && typeof v === 'function') {
          el.addEventListener(k.slice(2), v);
        } else {
          el.setAttribute(k, v);
        }
      }
    }
    if (children) {
      for (const c of children) {
        if (c == null) continue;
        if (typeof c === 'string') {
          el.appendChild(document.createTextNode(c));
        } else {
          el.appendChild(c);
        }
      }
    }
    return el;
  }

  async function postJSON(path, body) {
    const r = await fetch(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (!r.ok) {
      const text = await r.text();
      throw new Error('HTTP ' + r.status + ': ' + text);
    }
    return r.json();
  }

  async function getJSON(path) {
    const r = await fetch(path);
    if (!r.ok) {
      throw new Error('HTTP ' + r.status);
    }
    return r.json();
  }

  // ---------- health badge ----------

  const healthBadge = document.getElementById('health-badge');
  const versionText = document.getElementById('version-text');

  async function pollHealth() {
    try {
      const r = await fetch('/healthz');
      if (r.ok) {
        const j = await r.json();
        if (j.ok) {
          healthBadge.textContent = 'connected';
          healthBadge.className = 'badge badge-good';
          if (j.version) versionText.textContent = j.version;
          return;
        }
      }
      throw new Error('not ok');
    } catch (e) {
      healthBadge.textContent = 'offline';
      healthBadge.className = 'badge badge-bad';
    }
  }

  pollHealth();
  setInterval(pollHealth, 5000);

  // ---------- search ----------

  const searchForm = document.getElementById('search-form');
  const resultsDiv = document.getElementById('results');
  const queryInput = document.getElementById('search-query');
  const optSwarm = document.getElementById('opt-swarm');
  const optDHT = document.getElementById('opt-dht');
  const optLimit = document.getElementById('opt-limit');

  searchForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const q = queryInput.value.trim();
    if (!q) return;
    resultsDiv.innerHTML = '<p class="hint">searching…</p>';
    try {
      const body = {
        q: q,
        limit: parseInt(optLimit.value, 10) || 20,
        swarm: optSwarm.checked,
        dht: optDHT.checked,
      };
      const res = await postJSON('/search', body);
      renderResults(res, q);
    } catch (err) {
      resultsDiv.innerHTML = '';
      resultsDiv.appendChild(elt('p', { class: 'hint', text: 'error: ' + err.message }));
    }
  });

  function renderResults(res, q) {
    resultsDiv.innerHTML = '';
    const localCount = res.local && res.local.hits ? res.local.hits.length : 0;
    const swarmCount = res.swarm && res.swarm.hits ? res.swarm.hits.length : 0;
    const dhtCount = res.dht && res.dht.hits ? res.dht.hits.length : 0;
    const totalCount = localCount + swarmCount + dhtCount;

    const headerLines = [];
    headerLines.push('Query: ' + q);
    headerLines.push('Local: ' + (res.local ? res.local.total : 0) + ' hits');
    if (res.swarm) {
      headerLines.push('Swarm: asked=' + res.swarm.asked +
        ', responded=' + res.swarm.responded +
        ', rejected=' + res.swarm.rejected);
      if (res.swarm.error) headerLines.push('Swarm error: ' + res.swarm.error);
    }
    if (res.dht) {
      headerLines.push('DHT: asked=' + res.dht.indexers_asked +
        ', responded=' + res.dht.indexers_responded);
      if (res.dht.error) headerLines.push('DHT error: ' + res.dht.error);
    }
    resultsDiv.appendChild(elt('div', { class: 'results-header' },
      [headerLines.join(' · ')]));

    if (totalCount === 0) {
      resultsDiv.appendChild(elt('p', { class: 'hint', text: '(no results)' }));
      return;
    }

    if (localCount > 0) {
      resultsDiv.appendChild(renderLocalSection(res.local.hits));
    }
    if (swarmCount > 0) {
      resultsDiv.appendChild(renderSwarmSection(res.swarm.hits));
    }
    if (dhtCount > 0) {
      resultsDiv.appendChild(renderDHTSection(res.dht.hits));
    }
  }

  function renderLocalSection(hits) {
    const section = elt('div', { class: 'results-section' });
    section.appendChild(elt('h3', { text: 'Local (Layer L)' }));
    hits.forEach(h => section.appendChild(renderLocalHit(h)));
    return section;
  }

  function renderLocalHit(h) {
    const hit = elt('div', { class: 'hit' });
    if (h.doc_type === 'content') {
      hit.appendChild(elt('div', { class: 'hit-name hit-content' },
        ['📄 ' + (h.file_path || h.name || '(unnamed)')]));
      hit.appendChild(elt('div', { class: 'hit-meta' },
        [h.infohash + ' · ' + humanBytes(h.size_bytes) +
          ' · extractor=' + (h.extractor || '?') +
          ' · score=' + (h.score || 0).toFixed(3)]));
    } else {
      hit.appendChild(elt('div', { class: 'hit-name' }, [h.name || '(unnamed)']));
      hit.appendChild(elt('div', { class: 'hit-meta' },
        [h.infohash + ' · ' + humanBytes(h.size_bytes) +
          ' · score=' + (h.score || 0).toFixed(3)]));
    }
    hit.appendChild(renderHitActions(h.infohash));
    return hit;
  }

  function renderSwarmSection(hits) {
    const section = elt('div', { class: 'results-section' });
    section.appendChild(elt('h3', { text: 'Swarm (Layer S — sn_search)' }));
    hits.forEach(h => {
      const hit = elt('div', { class: 'hit' });
      hit.appendChild(elt('div', { class: 'hit-name' }, [h.name || '(unnamed)']));
      hit.appendChild(elt('div', { class: 'hit-meta' },
        [h.infohash +
          ' · ' + humanBytes(h.size) +
          ' · seeders=' + (h.seeders || 0) +
          ' · score=' + (h.score || 0) +
          ' · sources=' + ((h.sources && h.sources.length) || 0)]));
      hit.appendChild(renderHitActions(h.infohash));
      section.appendChild(hit);
    });
    return section;
  }

  function renderDHTSection(hits) {
    const section = elt('div', { class: 'results-section' });
    section.appendChild(elt('h3', { text: 'DHT (Layer D — BEP-44)' }));
    hits.forEach(h => {
      const hit = elt('div', { class: 'hit' });
      hit.appendChild(elt('div', { class: 'hit-name' }, [h.name || '(unnamed)']));
      hit.appendChild(elt('div', { class: 'hit-meta' },
        [h.infohash +
          ' · ' + humanBytes(h.size) +
          ' · seeders=' + (h.seeders || 0) +
          ' · files=' + (h.files || 0) +
          ' · sources=' + ((h.sources && h.sources.length) || 0)]));
      hit.appendChild(renderHitActions(h.infohash));
      section.appendChild(hit);
    });
    return section;
  }

  function renderHitActions(infohash) {
    const actions = elt('div', { class: 'hit-actions' });

    actions.appendChild(elt('button', {
      class: 'confirm',
      title: 'Mark this infohash as known-good (boosts it in future lookups)',
      text: '👍 confirm',
      onclick: async () => {
        try {
          await postJSON('/confirm', { infohash: infohash });
          actions.innerHTML = '';
          actions.appendChild(elt('span', { class: 'hint hit-bloom', text: '✓ confirmed' }));
        } catch (err) {
          actions.innerHTML = '';
          actions.appendChild(elt('span', { class: 'hint', text: 'error: ' + err.message }));
        }
      },
    }));

    actions.appendChild(elt('button', {
      class: 'flag',
      title: 'Flag as spam (lowers reputation of every indexer that returned it)',
      text: '🚫 flag',
      onclick: async () => {
        if (!confirm('Flag this infohash as spam?')) return;
        try {
          await postJSON('/flag', { infohash: infohash });
          actions.innerHTML = '';
          actions.appendChild(elt('span', { class: 'hint', text: '✓ flagged' }));
        } catch (err) {
          actions.innerHTML = '';
          actions.appendChild(elt('span', { class: 'hint', text: 'error: ' + err.message }));
        }
      },
    }));

    return actions;
  }

  // ---------- add torrent ----------

  const addForm = document.getElementById('add-form');
  const addUri = document.getElementById('add-uri');
  const addResult = document.getElementById('add-result');

  addForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const uri = addUri.value.trim();
    if (!uri) return;
    addResult.innerHTML = '';
    addResult.appendChild(elt('p', { class: 'hint', text: 'submitting…' }));
    try {
      const res = await postJSON('/torrent', { uri: uri });
      addResult.innerHTML = '';
      addResult.appendChild(elt('p', { class: 'hint hit-bloom',
        text: '✓ added: ' + (res.infohash || '(unknown)') }));
      addUri.value = '';
    } catch (err) {
      addResult.innerHTML = '';
      addResult.appendChild(elt('p', { class: 'hint', text: 'error: ' + err.message }));
    }
  });

  // ---------- status ----------

  const statusDisplay = document.getElementById('status-display');
  let statusTimer = null;

  async function refreshStatus() {
    try {
      const s = await getJSON('/status');
      renderStatus(s);
    } catch (err) {
      statusDisplay.innerHTML = '';
      statusDisplay.appendChild(elt('p', { class: 'hint', text: 'error: ' + err.message }));
    }
    if (!panels.status.classList.contains('hidden')) {
      clearTimeout(statusTimer);
      statusTimer = setTimeout(refreshStatus, 4000);
    }
  }

  function renderStatus(s) {
    statusDisplay.innerHTML = '';
    const grid = elt('div', { class: 'status-grid' });

    grid.appendChild(card('Local index (L)', [
      ['enabled', s.local && s.local.indexed ? 'yes' : 'no'],
      ['documents', String((s.local && s.local.doc_count) || 0)],
    ]));

    grid.appendChild(card('Swarm peers (S)', [
      ['known peers', String((s.swarm && s.swarm.known_peers) || 0)],
      ['capable peers', String((s.swarm && s.swarm.capable_peers) || 0)],
    ]));

    const pubRows = [
      ['keywords', String((s.publisher && s.publisher.total_keywords) || 0)],
      ['hits', String((s.publisher && s.publisher.total_hits) || 0)],
    ];
    if (s.publisher && s.publisher.pubkey) {
      pubRows.unshift(['pubkey', s.publisher.pubkey.slice(0, 16) + '…']);
    }
    grid.appendChild(card('DHT publisher (D)', pubRows));

    if (s.bloom) {
      grid.appendChild(card('Bloom filter', [
        ['estimated items', Math.round(s.bloom.estimated_items).toString()],
        ['population bits', String(s.bloom.population_bits)],
        ['size', humanBytes(s.bloom.bit_size / 8)],
      ]));
    }

    if (s.reputation) {
      grid.appendChild(card('Reputation', [
        ['known indexers', String(s.reputation.known_indexers || 0)],
      ]));
    }

    statusDisplay.appendChild(grid);

    // Per-keyword publisher table.
    if (s.publisher && s.publisher.keywords && s.publisher.keywords.length > 0) {
      const heading = elt('h3', { text: 'Published keywords', class: 'results-section' });
      statusDisplay.appendChild(heading);
      const tbl = elt('table', { class: 'kw-table' });
      const thead = elt('thead');
      thead.appendChild(rowOf(['keyword', 'hits', 'publishes', 'state']));
      tbl.appendChild(thead);
      const tbody = elt('tbody');
      for (const k of s.publisher.keywords) {
        const state = k.last_error ? ('ERR: ' + k.last_error) : 'ok';
        tbody.appendChild(rowOf([k.keyword,
          String(k.hits_count),
          String(k.publish_count),
          state]));
      }
      tbl.appendChild(tbody);
      statusDisplay.appendChild(tbl);
    }
  }

  function card(title, rows) {
    const c = elt('div', { class: 'status-card' });
    c.appendChild(elt('h3', { text: title }));
    rows.forEach(([k, v]) => {
      c.appendChild(elt('div', { class: 'status-row' },
        [elt('span', { text: k }), elt('strong', { text: v })]));
    });
    return c;
  }

  function rowOf(values) {
    const tr = elt('tr');
    values.forEach(v => tr.appendChild(elt('td', { text: v })));
    return tr;
  }

  // ---------- downloads ----------

  const downloadsList = document.getElementById('downloads-list');
  let downloadsTimer = null;

  async function refreshDownloads() {
    try {
      const res = await getJSON('/torrents');
      renderDownloads(res.torrents || []);
    } catch (err) {
      downloadsList.innerHTML = '';
      downloadsList.appendChild(elt('p', { class: 'hint',
        text: 'error: ' + err.message }));
    }
    if (!panels.downloads.classList.contains('hidden')) {
      clearTimeout(downloadsTimer);
      downloadsTimer = setTimeout(refreshDownloads, 2000);
    }
  }

  function renderDownloads(torrents) {
    downloadsList.innerHTML = '';
    if (torrents.length === 0) {
      downloadsList.appendChild(elt('p', { class: 'hint',
        text: '(no torrents — add one from the Add torrent tab)' }));
      return;
    }
    torrents.forEach(t => downloadsList.appendChild(renderDownload(t)));
  }

  function renderDownload(t) {
    const card = elt('div', { class: 'download' });

    // Header: name + status pill.
    const header = elt('div', { class: 'download-header' });
    header.appendChild(elt('span', { class: 'download-name',
      text: t.name || '(fetching metadata…)' }));
    header.appendChild(elt('span', {
      class: 'download-status status-' + (t.status || 'metadata'),
      text: t.status || 'metadata',
    }));
    card.appendChild(header);

    // Progress bar.
    const bar = elt('div', { class: 'download-bar' });
    const fill = elt('div', { class: 'download-bar-fill' });
    fill.style.width = (Math.min(1, t.progress || 0) * 100).toFixed(1) + '%';
    bar.appendChild(fill);
    card.appendChild(bar);

    // Meta line.
    const meta = elt('div', { class: 'download-meta' });
    if (t.size > 0) {
      meta.appendChild(elt('span', {
        text: humanBytes(t.bytes_completed || 0) + ' / ' + humanBytes(t.size) +
              '  (' + (t.progress * 100).toFixed(1) + '%)',
      }));
    } else {
      meta.appendChild(elt('span', { text: 'size unknown' }));
    }
    meta.appendChild(elt('span', {
      text: 'peers ' + (t.active_peers || 0) + '/' + (t.total_peers || 0),
    }));
    if (t.seeders !== undefined) {
      meta.appendChild(elt('span', { text: 'seeders ' + (t.seeders || 0) }));
    }
    if (t.files > 0) {
      meta.appendChild(elt('span', { text: 'files ' + t.files }));
    }
    meta.appendChild(elt('span', { text: t.infohash.slice(0, 16) + '…' }));
    card.appendChild(meta);

    // Action buttons: pause/resume + remove.
    const actions = elt('div', { class: 'download-actions' });
    if (t.paused) {
      actions.appendChild(elt('button', {
        text: '▶ resume',
        title: 'Resume downloading',
        onclick: () => controlTorrent('resume', t.infohash),
      }));
    } else {
      actions.appendChild(elt('button', {
        text: '⏸ pause',
        title: 'Pause this torrent',
        onclick: () => controlTorrent('pause', t.infohash),
      }));
    }
    actions.appendChild(elt('button', {
      class: 'danger',
      text: '✕ remove',
      title: 'Drop this torrent (file content stays on disk)',
      onclick: async () => {
        if (!confirm('Remove this torrent from the daemon? (the downloaded files stay on disk)')) {
          return;
        }
        await controlTorrent('remove', t.infohash);
      },
    }));
    card.appendChild(actions);

    return card;
  }

  async function controlTorrent(action, infohash) {
    let path, method;
    if (action === 'remove') {
      path = '/torrents/' + infohash;
      method = 'DELETE';
    } else {
      path = '/torrents/' + infohash + '/' + action;
      method = 'POST';
    }
    try {
      const r = await fetch(path, { method: method });
      if (!r.ok) {
        const text = await r.text();
        throw new Error('HTTP ' + r.status + ': ' + text);
      }
      // Trigger an immediate refresh so the UI reflects the new
      // state without waiting for the next poll tick.
      refreshDownloads();
    } catch (err) {
      alert(action + ' failed: ' + err.message);
    }
  }

  // ---------- settings (sn_search capability) ----------

  document.getElementById('cap-save').addEventListener('click', async () => {
    const shareLevel = parseInt(document.querySelector('input[name="cap-share"]:checked').value, 10);
    const fileHits = document.getElementById('cap-files').checked ? 1 : 0;
    const contentHits = document.getElementById('cap-content').checked ? 1 : 0;
    try {
      await postJSON('/capabilities', {
        share_local: shareLevel,
        file_hits: fileHits,
        content_hits: contentHits,
      });
      const msg = document.getElementById('cap-saved');
      msg.textContent = '✓ saved';
      setTimeout(() => { msg.textContent = ''; }, 3000);
    } catch (err) {
      const msg = document.getElementById('cap-saved');
      msg.textContent = 'error: ' + err.message;
    }
  });

  async function loadCapabilities() {
    try {
      const c = await getJSON('/capabilities');
      const radio = document.querySelector('input[name="cap-share"][value="' + c.share_local + '"]');
      if (radio) radio.checked = true;
      document.getElementById('cap-files').checked = c.file_hits > 0;
      document.getElementById('cap-content').checked = c.content_hits > 0;
    } catch (err) {
      // Endpoint may not exist yet (M8c). Silently ignore.
    }
  }
  loadCapabilities();

  // ---------- companion (M11e) ----------

  async function refreshCompanion() {
    const pubBox = document.getElementById('companion-publisher');
    const followBox = document.getElementById('companion-follow-list');
    try {
      const c = await getJSON('/companion');
      pubBox.replaceChildren(renderCompanionPublisher(c.publisher || {}));
      followBox.replaceChildren(renderCompanionFollows(c.subscriber || []));
    } catch (err) {
      pubBox.replaceChildren(elt('p', { class: 'hint', text: 'error: ' + err.message }));
      followBox.replaceChildren();
    }
  }

  function renderCompanionPublisher(p) {
    if (!p.pubkey_hex) {
      return elt('p', { class: 'hint',
        text: 'No publisher running. Start the daemon with an identity and DHT enabled.' });
    }
    const dl = elt('dl', { class: 'companion-status' });
    function row(label, value) {
      dl.appendChild(elt('dt', { text: label }));
      dl.appendChild(elt('dd', { text: value }));
    }
    row('Publisher pubkey', p.pubkey_hex);
    row('Last refresh', p.last_refresh && p.last_refresh !== '0001-01-01T00:00:00Z'
      ? new Date(p.last_refresh).toLocaleString()
      : 'never');
    row('Last infohash', p.last_infohash || '(none)');
    row('Published count', p.published_count);
    if (p.last_error) {
      row('Last error', p.last_error);
    }
    return dl;
  }

  function renderCompanionFollows(rows) {
    if (!rows.length) {
      return elt('p', { class: 'hint',
        text: 'Not following any publishers yet. Add a pubkey above to start syncing.' });
    }
    const list = elt('div', { class: 'companion-follows' });
    for (const row of rows) {
      list.appendChild(renderCompanionFollow(row));
    }
    return list;
  }

  function renderCompanionFollow(row) {
    const card = elt('div', { class: 'companion-follow' });
    const head = elt('div', { class: 'companion-follow-head' });
    head.appendChild(elt('span', {
      class: 'companion-follow-label',
      text: row.label || '(unlabeled)',
    }));
    head.appendChild(elt('button', {
      class: 'danger',
      text: '✕ unfollow',
      onclick: () => unfollowPublisher(row.pubkey_hex),
    }));
    card.appendChild(head);

    card.appendChild(elt('div', {
      class: 'companion-follow-pubkey',
      text: row.pubkey_hex,
    }));

    const meta = elt('div', { class: 'companion-follow-meta' });
    meta.appendChild(elt('span', {
      text: 'imported ' + (row.torrents_imported || 0) + ' torrents, ' +
            (row.content_imported || 0) + ' content rows',
    }));
    if (row.pointer_infohash) {
      meta.appendChild(elt('span', {
        text: 'pointer: ' + row.pointer_infohash.slice(0, 16) + '…',
      }));
    }
    if (row.last_sync_at && row.last_sync_at !== '0001-01-01T00:00:00Z') {
      meta.appendChild(elt('span', {
        text: 'snapshot: ' + new Date(row.last_sync_at).toLocaleString(),
      }));
    }
    card.appendChild(meta);

    if (row.last_error) {
      card.appendChild(elt('div', {
        class: 'companion-follow-err',
        text: 'error: ' + row.last_error,
      }));
    }
    return card;
  }

  async function unfollowPublisher(pubkey) {
    if (!confirm('Unfollow publisher ' + pubkey.slice(0, 16) + '…?')) return;
    try {
      await postJSON('/companion/unfollow', { pubkey: pubkey });
      refreshCompanion();
    } catch (err) {
      alert('unfollow failed: ' + err.message);
    }
  }

  document.getElementById('companion-follow-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const pub = document.getElementById('companion-follow-pubkey').value.trim();
    const label = document.getElementById('companion-follow-label').value.trim();
    if (pub.length !== 64) {
      alert('pubkey must be exactly 64 hex characters');
      return;
    }
    try {
      await postJSON('/companion/follow', { pubkey: pub, label: label });
      document.getElementById('companion-follow-pubkey').value = '';
      document.getElementById('companion-follow-label').value = '';
      refreshCompanion();
    } catch (err) {
      alert('follow failed: ' + err.message);
    }
  });

  document.getElementById('companion-refresh').addEventListener('click', async () => {
    const out = document.getElementById('companion-refresh-result');
    out.textContent = 'refreshing…';
    try {
      await postJSON('/companion/refresh', {});
      out.textContent = '✓ refresh queued';
      setTimeout(() => { out.textContent = ''; }, 3000);
      refreshCompanion();
    } catch (err) {
      out.textContent = 'error: ' + err.message;
    }
  });

})();
