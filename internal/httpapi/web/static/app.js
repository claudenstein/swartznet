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

  // Keyboard shortcuts. Standard browser convention: pressing
  // "/" focuses the search input (and switches to the Search
  // tab if needed) — like GitHub, Slack, Discord, Linear, etc.
  // We also accept Ctrl/Cmd+K which a few apps use. Both are
  // ignored when the user is already typing in another input.
  document.addEventListener('keydown', ev => {
    const target = ev.target;
    const inEditable = target && (
      target.tagName === 'INPUT' ||
      target.tagName === 'TEXTAREA' ||
      target.isContentEditable
    );
    const isSlash = ev.key === '/' && !inEditable && !ev.ctrlKey && !ev.metaKey;
    const isCtrlK = ev.key === 'k' && (ev.ctrlKey || ev.metaKey);
    if (isSlash || isCtrlK) {
      ev.preventDefault();
      const searchTab = document.querySelector('[data-tab="search"]');
      if (searchTab) searchTab.click();
      const q = document.getElementById('search-query');
      if (q) {
        q.focus();
        q.select();
      }
    }
  });

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
  const optSignedBy = document.getElementById('search-signed-by');

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
        highlight: true,
      };
      const signedBy = (optSignedBy.value || '').trim().toLowerCase();
      if (signedBy) body.signed_by = signedBy;
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
      // Prefer highlighted name for the title row when Bleve
      // matched terms in the torrent name (rare for content
      // hits, but possible). Fall back to plain file path.
      const titleText = '📄 ' + (h.file_path || h.name || '(unnamed)');
      hit.appendChild(elt('div', { class: 'hit-name hit-content' }, [titleText]));
      hit.appendChild(elt('div', { class: 'hit-meta' },
        [h.infohash + ' · ' + humanBytes(h.size_bytes) +
          ' · extractor=' + (h.extractor || '?') +
          ' · score=' + (h.score || 0).toFixed(3)]));
      // Snippet fragments from the matched text body.
      appendFragments(hit, h, 'text');
    } else {
      hit.appendChild(renderHighlightedName(h));
      const meta = elt('div', { class: 'hit-meta' },
        [h.infohash + ' · ' + humanBytes(h.size_bytes) +
          ' · score=' + (h.score || 0).toFixed(3)]);
      if (h.signed_by) {
        meta.appendChild(elt('span', {
          class: 'signed',
          text: ' · ✓ ' + h.signed_by.slice(0, 8),
          title: 'Signed by ' + h.signed_by + ' — click to filter to this publisher',
          onclick: () => {
            document.getElementById('search-signed-by').value = h.signed_by;
            document.getElementById('search-go').click();
          },
        }));
      }
      hit.appendChild(meta);
      // File-list fragments on torrent hits help show which
      // file path matched.
      appendFragments(hit, h, 'files');
    }
    hit.appendChild(renderHitActions(h.infohash));
    return hit;
  }

  // renderHighlightedName renders a torrent-hit title using
  // Bleve's highlighted `name` fragment when present, otherwise
  // the plain name. The <mark>...</mark> markup Bleve emits is
  // inserted via innerHTML — it is pre-escaped by Bleve so this
  // is safe.
  function renderHighlightedName(h) {
    const div = elt('div', { class: 'hit-name' });
    const frag = h.fragments && h.fragments.name && h.fragments.name[0];
    if (frag) {
      div.innerHTML = frag;
    } else {
      div.textContent = h.name || '(unnamed)';
    }
    return div;
  }

  // appendFragments renders Bleve highlight fragments for a
  // specific field as a snippet block under the hit meta row.
  // Shows up to 2 fragments to keep the result card compact.
  function appendFragments(parent, h, field) {
    if (!h.fragments || !h.fragments[field] || h.fragments[field].length === 0) {
      return;
    }
    const block = elt('div', { class: 'hit-snippets' });
    const frags = h.fragments[field].slice(0, 2);
    for (const f of frags) {
      const p = elt('div', { class: 'hit-snippet' });
      // Bleve's html highlighter already escapes the text and
      // wraps matches in <mark>, so innerHTML is safe here.
      p.innerHTML = f;
      block.appendChild(p);
    }
    parent.appendChild(block);
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
      const [s, ixStats, torrents, agg] = await Promise.all([
        getJSON('/status'),
        getJSON('/index/stats').catch(() => null), // optional — tolerate older daemons
        getJSON('/torrents').catch(() => null),    // for the Torrents card
        getJSON('/aggregate').catch(() => null),   // v0.5 Aggregate track — older daemons lack this
      ]);
      renderStatus(s, ixStats, torrents, agg);
    } catch (err) {
      statusDisplay.innerHTML = '';
      statusDisplay.appendChild(elt('p', { class: 'hint', text: 'error: ' + err.message }));
    }
    if (!panels.status.classList.contains('hidden')) {
      clearTimeout(statusTimer);
      statusTimer = setTimeout(refreshStatus, 4000);
    }
  }

  function renderStatus(s, ixStats, torrents, agg) {
    statusDisplay.innerHTML = '';
    const grid = elt('div', { class: 'status-grid' });

    // Torrents card — counts by status + aggregate throughput.
    // Mirrors the native GUI's "Torrents" card. Computed from
    // the same /torrents poll the Downloads tab uses.
    if (torrents && Array.isArray(torrents.torrents)) {
      let total = 0, downloading = 0, seeding = 0, queued = 0, paused = 0;
      let totalDown = 0, totalUp = 0, signedCount = 0, trustedCount = 0;
      for (const t of torrents.torrents) {
        total++;
        switch (t.status) {
          case 'downloading': downloading++; break;
          case 'seeding': seeding++; break;
          case 'queued': queued++; break;
          case 'paused': paused++; break;
        }
        totalDown += t.download_rate || 0;
        totalUp += t.upload_rate || 0;
        if (t.signed_by) signedCount++;
        if (t.trusted_publisher) trustedCount++;
      }
      const rateStr = bps => bps > 0 ? humanBytes(bps) + '/s' : '—';
      grid.appendChild(card('Torrents', [
        ['total', String(total)],
        ['downloading', String(downloading)],
        ['seeding', String(seeding)],
        ['queued', String(queued)],
        ['paused', String(paused)],
        ['↓ rate', rateStr(totalDown)],
        ['↑ rate', rateStr(totalUp)],
        ['signed', String(signedCount)],
        ['trusted', String(trustedCount)],
      ]));
    }

    // Local index card. If the new /index/stats endpoint
    // answered, enrich the card with dir size + corpus text +
    // inflation ratio — the v1.0.0 measurement numbers.
    const localRows = [
      ['enabled', s.local && s.local.indexed ? 'yes' : 'no'],
      ['documents', String((s.local && s.local.doc_count) || 0)],
    ];
    if (ixStats) {
      localRows.push(['torrents', String(ixStats.torrent_count || 0)]);
      localRows.push(['content rows', String(ixStats.content_count || 0)]);
      localRows.push(['on-disk', humanBytes(ixStats.dir_bytes || 0)]);
      localRows.push(['corpus text', humanBytes(ixStats.corpus_text_bytes || 0)]);
      if (ixStats.inflation_ratio > 0) {
        localRows.push(['inflation', ixStats.inflation_ratio.toFixed(2) + '×']);
      }
    }
    grid.appendChild(card('Local index (L)', localRows));

    grid.appendChild(card('Swarm peers (S)', [
      ['known peers', String((s.swarm && s.swarm.known_peers) || 0)],
      ['capable peers', String((s.swarm && s.swarm.capable_peers) || 0)],
    ]));

    // DHT routing-table card. Only rendered when the daemon
    // reports a "dht" block (it's omitted entirely when the
    // daemon started with DisableDHT=true), so a DHT-off daemon
    // sees one fewer card rather than a card full of zeros.
    if (s.dht) {
      grid.appendChild(card('DHT routing', [
        ['good nodes', String(s.dht.good_nodes || 0)],
        ['total nodes', String(s.dht.nodes || 0)],
      ]));
    }

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

    // Aggregate (v0.5 redesign) card — only rendered when the
    // daemon exposes /aggregate (older daemons 404 on that path,
    // getJSON('/aggregate') resolves to null). PPMI enabled /
    // record source / cache size are the fields users care about
    // during the dual-read migration window.
    if (agg) {
      const aggRows = [
        ['PPMI enabled', agg.ppmi_enabled ? 'yes' : 'no'],
        ['known indexers', String(agg.known_indexers || 0)],
      ];
      if (agg.record_source_kind) {
        aggRows.push(['record source', agg.record_source_kind]);
      }
      if (agg.record_cache_size > 0) {
        aggRows.push(['cache size', String(agg.record_cache_size)]);
      }
      if (agg.bootstrap) {
        aggRows.push(['anchors', String(agg.bootstrap.anchors || 0)]);
        aggRows.push(['admitted', String(agg.bootstrap.admitted || 0)]);
        if (agg.bootstrap.pending > 0) {
          aggRows.push(['pending', String(agg.bootstrap.pending)]);
        }
      }
      grid.appendChild(card('Aggregate (v0.5)', aggRows));
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

    // Indexing progress bar — only when the torrent feeds the
    // index AND we already know its file count (post-metadata).
    // Advances from 0 toward t.files as the extraction pipeline
    // chews through completed files. Useful signal for big
    // text-heavy torrents (e.g. a 450k-file Gutenberg mirror)
    // where indexing can take orders of magnitude longer than
    // the download itself.
    if (t.indexing && t.files > 0) {
      const indexed = t.indexed_files || 0;
      const frac = Math.min(1, indexed / t.files);
      const ibar = elt('div', { class: 'indexing-bar' });
      const ifill = elt('div', { class: 'indexing-bar-fill' });
      ifill.style.width = (frac * 100).toFixed(1) + '%';
      ibar.appendChild(ifill);
      card.appendChild(ibar);

      const imeta = elt('div', { class: 'indexing-meta' });
      const extracted = t.index_extracted || 0;
      let label = '🔍 indexed ' + indexed.toLocaleString() +
                  ' / ' + t.files.toLocaleString() +
                  ' (' + (frac * 100).toFixed(1) + '%)';
      if (extracted > 0 && extracted !== indexed) {
        label += ' · ' + extracted.toLocaleString() + ' with text';
      }
      imeta.textContent = label;
      card.appendChild(imeta);
    }

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
    // Transfer rates — match the native GUI's ↓/↑ columns.
    if (t.download_rate > 0 || t.upload_rate > 0) {
      meta.appendChild(elt('span', {
        text: '↓ ' + humanBytes(t.download_rate || 0) + '/s · ↑ ' + humanBytes(t.upload_rate || 0) + '/s',
      }));
    }
    // Signed publisher, if any — gold star for trusted.
    if (t.signed_by) {
      const badge = t.trusted_publisher ? '★' : '✓';
      meta.appendChild(elt('span', {
        class: t.trusted_publisher ? 'signed-trusted' : 'signed',
        text: badge + ' ' + t.signed_by.slice(0, 8),
        title: 'Signed by ' + t.signed_by +
          (t.trusted_publisher ? ' (trusted publisher)' : ''),
      }));
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
      onclick: () => {
        if (!confirm('Remove this torrent from the daemon? (the downloaded files stay on disk)')) {
          return;
        }
        // controlTorrent handles its own errors via alert(), so
        // the fire-and-forget is intentional — using await here
        // would create an async onclick whose rejection path is
        // handled by the nested try/catch in controlTorrent, but
        // the outer handler looks like it could leak a rejection.
        // Dropping await removes that concern entirely.
        controlTorrent('remove', t.infohash);
      },
    }));
    actions.appendChild(elt('button', {
      text: '📁 files',
      title: 'Show per-file list and priorities',
      onclick: () => showFilesDialog(t),
    }));
    actions.appendChild(elt('button', {
      text: t.indexing ? '🔍 index: on' : '🔍 index: off',
      title: 'Toggle whether this torrent feeds the local index',
      onclick: () => toggleIndexing(t.infohash, !t.indexing),
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

  async function toggleIndexing(infohash, enabled) {
    try {
      const r = await fetch('/torrents/' + infohash + '/indexing', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled: enabled }),
      });
      if (!r.ok) throw new Error('HTTP ' + r.status + ': ' + await r.text());
      refreshDownloads();
    } catch (err) {
      alert('indexing toggle failed: ' + err.message);
    }
  }

  // ---------- files dialog ----------

  async function showFilesDialog(t) {
    let files;
    try {
      const r = await fetch('/torrents/' + t.infohash + '/files');
      if (!r.ok) throw new Error('HTTP ' + r.status + ': ' + await r.text());
      const body = await r.json();
      files = body.files || [];
    } catch (err) {
      alert('files: ' + err.message);
      return;
    }

    // Strip any existing overlay so double-clicks don't stack.
    const existing = document.getElementById('files-modal');
    if (existing) existing.remove();

    const overlay = elt('div', { id: 'files-modal', class: 'modal-overlay' });
    const dialog = elt('div', { class: 'modal-dialog' });
    dialog.appendChild(elt('h3', { text: 'Files in ' + (t.name || t.infohash) }));
    if (files.length === 0) {
      dialog.appendChild(elt('p', { class: 'hint',
        text: '(torrent metadata not yet available)' }));
    } else {
      // Bulk actions.
      const bulk = elt('div', { class: 'files-bulk' });
      bulk.appendChild(elt('button', {
        text: 'Select All',
        onclick: async () => { await setAllPriorities(t.infohash, files, 'normal'); showFilesDialog(t); },
      }));
      bulk.appendChild(elt('button', {
        text: 'Deselect All',
        onclick: async () => { await setAllPriorities(t.infohash, files, 'none'); showFilesDialog(t); },
      }));
      dialog.appendChild(bulk);

      const list = elt('table', { class: 'files-table' });
      files.forEach(f => {
        const row = elt('tr');
        row.appendChild(elt('td', { class: 'files-path', text: f.display_path, title: f.display_path }));
        row.appendChild(elt('td', { text: humanBytes(f.length || 0) }));
        row.appendChild(elt('td', { text: ((f.progress || 0) * 100).toFixed(1) + '%' }));
        const prioCell = elt('td');
        ['none', 'normal', 'high'].forEach(p => {
          const btn = elt('button', {
            text: p,
            class: f.priority === p ? 'priority-active' : '',
            onclick: async () => {
              try {
                const r = await fetch('/torrents/' + t.infohash + '/files/' + f.index + '/priority', {
                  method: 'POST',
                  headers: { 'Content-Type': 'application/json' },
                  body: JSON.stringify({ priority: p }),
                });
                if (!r.ok) throw new Error('HTTP ' + r.status + ': ' + await r.text());
                showFilesDialog(t);
              } catch (err) { alert('priority: ' + err.message); }
            },
          });
          prioCell.appendChild(btn);
        });
        row.appendChild(prioCell);
        list.appendChild(row);
      });
      dialog.appendChild(list);
    }
    dialog.appendChild(elt('button', {
      text: 'Close',
      class: 'primary',
      onclick: () => overlay.remove(),
    }));
    overlay.appendChild(dialog);
    overlay.addEventListener('click', ev => {
      if (ev.target === overlay) overlay.remove();
    });
    document.body.appendChild(overlay);
  }

  async function setAllPriorities(infohash, files, priority) {
    for (const f of files) {
      await fetch('/torrents/' + infohash + '/files/' + f.index + '/priority', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ priority: priority }),
      });
    }
  }

  // ---------- settings (sn_search capability) ----------

  document.getElementById('cap-save').addEventListener('click', async () => {
    // The radio group ships with value=2 checked in index.html,
    // so this should always find a match — but guard anyway so a
    // hand-edited page or a future layout change can't crash the
    // click handler.
    const checked = document.querySelector('input[name="cap-share"]:checked');
    const shareLevel = checked ? parseInt(checked.value, 10) : 2;
    const fileHits = document.getElementById('cap-files').checked ? 1 : 0;
    const contentHits = document.getElementById('cap-content').checked ? 1 : 0;
    const msg = document.getElementById('cap-saved');
    try {
      await postJSON('/capabilities', {
        share_local: shareLevel,
        file_hits: fileHits,
        content_hits: contentHits,
      });
      msg.textContent = '✓ saved';
      setTimeout(() => { msg.textContent = ''; }, 3000);
    } catch (err) {
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

  // ---------- rate limit + queue (parity with native GUI) ----------

  async function loadRateLimit() {
    try {
      const r = await getJSON('/config/rate-limit');
      document.getElementById('rate-download').value =
        r.download_bps > 0 ? Math.round(r.download_bps / 1024) : 0;
      document.getElementById('rate-upload').value =
        r.upload_bps > 0 ? Math.round(r.upload_bps / 1024) : 0;
    } catch (err) { /* endpoint missing — ignore */ }
  }
  document.getElementById('rate-save').addEventListener('click', async () => {
    const dl = parseInt(document.getElementById('rate-download').value, 10) || 0;
    const ul = parseInt(document.getElementById('rate-upload').value, 10) || 0;
    const msg = document.getElementById('rate-saved');
    try {
      await postJSON('/config/rate-limit', {
        upload_bps: ul * 1024,
        download_bps: dl * 1024,
      });
      msg.textContent = '✓ saved';
      setTimeout(() => { msg.textContent = ''; }, 3000);
    } catch (err) {
      msg.textContent = 'error: ' + err.message;
    }
  });
  loadRateLimit();

  async function loadQueue() {
    try {
      const r = await getJSON('/config/queue');
      document.getElementById('queue-max').value = r.max_active_downloads || 0;
    } catch (err) { /* ignore */ }
  }
  document.getElementById('queue-save').addEventListener('click', async () => {
    const n = parseInt(document.getElementById('queue-max').value, 10) || 0;
    const msg = document.getElementById('queue-saved');
    try {
      await postJSON('/config/queue', { max_active_downloads: n });
      msg.textContent = '✓ saved';
      setTimeout(() => { msg.textContent = ''; }, 3000);
    } catch (err) {
      msg.textContent = 'error: ' + err.message;
    }
  });
  loadQueue();

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
