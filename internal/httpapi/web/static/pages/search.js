// search.js — the Search tab. Renders THREE strictly-separate result groups
// (L=local Bleve, S=swarm sn_search, D=DHT keyword). They are never merged,
// never cross-sorted, never deduped — the layer isolation is the whole point.
import * as api from '../api.js';
import { el, clear, humanBytes, shortHex, escapeHtml, fmtFloat, fmtInt, badge, toast } from '../util.js';

export function render(container) {
  const q = el('input', { type: 'text', id: 'q', placeholder: 'search the index…', class: 'grow' });
  const swarm = el('input', { type: 'checkbox', id: 'swarm' });
  const dht = el('input', { type: 'checkbox', id: 'dht' });
  const hl = el('input', { type: 'checkbox', id: 'hl', checked: true });
  const limit = el('input', { type: 'number', id: 'limit', value: 20, min: 1, max: 500, class: 'limit' });
  const results = el('div', { class: 'results' });

  const form = el('form', { class: 'search-form', onsubmit: onSubmit }, [
    el('div', { class: 'search-row' }, [q, el('button', { class: 'btn primary', type: 'submit' }, ['Search'])]),
    el('div', { class: 'search-opts' }, [
      el('label', {}, [swarm, ' Swarm (Layer S)']),
      el('label', {}, [dht, ' DHT (Layer D)']),
      el('label', {}, [hl, ' Highlight']),
      el('label', {}, ['Limit ', limit]),
    ]),
  ]);

  async function onSubmit(e) {
    e.preventDefault();
    const query = q.value.trim();
    if (!query) { toast('enter a query', 'err'); return; }
    if (new TextEncoder().encode(query).length > 1024) { toast('query too long (max 1024 bytes)', 'err'); return; }
    clear(results).appendChild(el('p', { class: 'empty' }, ['Searching…']));
    const body = { q: query, limit: Number(limit.value) || 20, highlight: hl.checked };
    if (swarm.checked) { body.swarm = true; body.swarm_timeout_ms = 8000; }
    if (dht.checked) { body.dht = true; body.dht_timeout_ms = 8000; }
    try {
      renderResults(await api.search(body), { swarm: swarm.checked, dht: dht.checked });
    } catch (err) {
      clear(results).appendChild(el('p', { class: 'error' }, ['Search failed: ' + err.message]));
    }
  }

  function actions(infohash) {
    const line = el('span', { class: 'act-status' });
    const doConfirm = async () => {
      try {
        const r = await api.confirm(infohash);
        line.textContent = `confirmed (boosted ${r.indexers_confirmed} indexer(s))`;
      } catch (e) { line.textContent = e.status === 503 ? 'confirm not available on this node' : e.message; }
    };
    const doFlag = async () => {
      try {
        const r = await api.flag(infohash);
        line.textContent = r.indexers_flagged > 0
          ? `flagged (demoted ${r.indexers_flagged})`
          : `no reputations changed: ${r.attribution}`;
      } catch (e) { line.textContent = e.status === 503 ? 'flag not available on this node' : e.message; }
    };
    return el('div', { class: 'hit-actions' }, [
      el('button', { class: 'btn tiny', onclick: doConfirm }, ['Confirm']),
      el('button', { class: 'btn tiny', onclick: doFlag }, ['Flag']),
      line,
    ]);
  }

  function card(title, ih, badges, extra) {
    return el('div', { class: 'hit' }, [
      el('div', { class: 'hit-title', title: ih }, [title]),
      el('div', { class: 'hit-badges' }, badges),
      extra || null,
      actions(ih),
    ]);
  }

  function localCard(h) {
    const badges = [badge(h.doc_type || 'torrent'), badge('score ' + fmtFloat(h.score))];
    if (h.signed_by) badges.push(badge('✓ ' + shortHex(h.signed_by, 8)));
    if (h.size_bytes) badges.push(badge(humanBytes(h.size_bytes)));
    let extra = null;
    if (h.doc_type === 'content') {
      const parts = [];
      if (h.file_path) parts.push('file: ' + h.file_path);
      if (h.mime) parts.push(h.mime);
      if (h.extractor) parts.push(h.extractor);
      extra = el('div', { class: 'hit-sub' }, [el('div', { class: 'mono' }, [parts.join(' · ')]), fragments(h.fragments)]);
    }
    return card(h.name || h.infohash, h.infohash, badges, extra);
  }

  function fragments(frags) {
    if (!frags) return null;
    const box = el('div', { class: 'fragments' });
    for (const arr of Object.values(frags)) {
      for (const snip of arr || []) {
        // Server highlight fragments wrap matches in <mark>…</mark>. Escape the
        // text, then re-allow ONLY <mark> so we never inject arbitrary markup.
        const safe = escapeHtml(snip).replaceAll('&lt;mark&gt;', '<mark>').replaceAll('&lt;/mark&gt;', '</mark>');
        box.appendChild(el('div', { class: 'frag', html: safe }));
      }
    }
    return box.children.length ? box : null;
  }

  function swarmCard(h) {
    const badges = [badge('score ' + fmtInt(h.score))];
    if (h.seeders) badges.push(badge(h.seeders + ' seeders'));
    if (h.size) badges.push(badge(humanBytes(h.size)));
    if ((h.sources || []).length) badges.push(badge((h.sources.length) + ' src'));
    return card(h.name || h.infohash, h.infohash, badges);
  }

  function dhtCard(h) {
    const badges = [badge('score ' + fmtFloat(h.score))];
    if (h.seeders) badges.push(badge(h.seeders + ' seeders'));
    if (h.size) badges.push(badge(humanBytes(h.size)));
    if ((h.sources || []).length) badges.push(badge(h.sources.length + ' src'));
    if (h.bloom_hit) badges.push(badge('known-good ✓', 'trusted'));
    return card(h.name || h.infohash, h.infohash, badges);
  }

  function group(heading, cards, emptyNote) {
    return el('section', { class: 'result-group' }, [
      el('h3', { class: 'group-head' }, [heading]),
      cards.length ? el('div', { class: 'hits' }, cards) : el('p', { class: 'empty' }, [emptyNote || '(no results)']),
    ]);
  }

  function renderResults(resp, asked) {
    clear(results);
    let anyHit = false;

    // Group L — always present.
    const local = resp.local || { total: 0, hits: [] };
    const lHits = (local.hits || []).map(localCard);
    anyHit = anyHit || lHits.length > 0;
    results.appendChild(group(`Local — ${local.total} match(es)`, lHits));

    // Group S — only if requested.
    if (asked.swarm && resp.swarm) {
      const s = resp.swarm;
      if (s.error) {
        results.appendChild(group(`Swarm — error: ${s.error}`, []));
      } else {
        const cards = (s.hits || []).map(swarmCard);
        anyHit = anyHit || cards.length > 0;
        results.appendChild(group(
          `Swarm — ${(s.hits || []).length} hit(s) · asked ${s.asked} · responded ${s.responded} · rejected ${s.rejected}`,
          cards));
      }
    }

    // Group D — only if requested.
    if (asked.dht && resp.dht) {
      const d = resp.dht;
      if (d.error) {
        results.appendChild(group(`DHT — error: ${d.error}`, []));
      } else {
        const cards = (d.hits || []).map(dhtCard);
        anyHit = anyHit || cards.length > 0;
        results.appendChild(group(
          `DHT — ${(d.hits || []).length} hit(s) · indexers ${d.indexers_responded}/${d.indexers_asked}`,
          cards));
      }
    }

    if (!anyHit) results.appendChild(el('p', { class: 'empty big' }, ['(no results)']));
  }

  clear(container).append(form, results);
  return { start() { q.focus(); }, stop() {} };
}
