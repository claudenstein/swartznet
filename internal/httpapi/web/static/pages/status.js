// status.js — the Status tab: node health composed from several reads. Each
// section renders its honest degraded state (an absent dht/bloom/reputation
// block means the subsystem is DISABLED; a 503 from /index/stats or /aggregate
// means that backend is off).
import * as api from '../api.js';
import { el, clear, humanBytes, fmtRate, shortHex, timeAgo, section, badge, copyButton } from '../util.js';

const POLL_MS = 2000;

export function render(container) {
  const view = el('div', { class: 'status-grid' }, ['Loading…']);
  let timer = null, busy = false, stopped = false;

  // settle resolves a read to its value, or null on 503 (feature off), or an
  // {__err} marker on any other failure so a section can show it.
  async function settle(p) {
    try { return await p; }
    catch (e) { return e.status === 503 ? null : { __err: e.message }; }
  }

  function kv(label, value) {
    return el('div', { class: 'kv' }, [el('span', { class: 'k' }, [label]), el('span', { class: 'v' }, [String(value)])]);
  }

  function torrentsSection(r) {
    if (!r || r.__err) return section('Torrents', el('p', { class: 'error' }, [r ? r.__err : 'unavailable']));
    const t = r.torrents || [];
    const byStatus = {};
    let dn = 0, up = 0;
    for (const x of t) { byStatus[x.status] = (byStatus[x.status] || 0) + 1; dn += x.download_rate || 0; up += x.upload_rate || 0; }
    const chips = Object.entries(byStatus).map(([s, n]) => badge(`${s}: ${n}`, s));
    return section('Torrents', el('div', {}, [
      kv('total', t.length),
      el('div', { class: 'chips' }, chips.length ? chips : ['—']),
      kv('throughput', `↓ ${fmtRate(dn)}  ↑ ${fmtRate(up)}`),
    ]));
  }

  function localSection(status, stats) {
    const body = el('div', {});
    const local = (status && !status.__err && status.local) || { indexed: false, doc_count: 0 };
    body.append(kv('indexed', local.indexed ? 'yes' : 'no'), kv('doc count', local.doc_count || 0));
    if (stats === null) body.append(el('p', { class: 'muted' }, ['index disabled (--no-index)']));
    else if (stats && stats.__err) body.append(el('p', { class: 'error' }, [stats.__err]));
    else if (stats) body.append(
      kv('torrents', stats.torrent_count), kv('content docs', stats.content_count),
      kv('index size', humanBytes(stats.dir_bytes)), kv('corpus text', humanBytes(stats.corpus_text_bytes)),
      kv('inflation', (stats.inflation_ratio || 0).toFixed(2) + '×'));
    return section('Local index (Layer L)', body);
  }

  function swarmSection(status) {
    const s = (status && !status.__err && status.swarm) || { known_peers: 0, capable_peers: 0 };
    return section('Swarm (Layer S)', el('div', {}, [kv('known peers', s.known_peers), kv('capable peers', s.capable_peers)]));
  }

  function rendezvousSection(status) {
    const r = status && !status.__err ? status.rendezvous : undefined;
    return section('Discovery (rendezvous + PEX)', r
      ? el('div', {}, [kv('rendezvous swarms', r.swarms), kv('gossiping peers', r.peer_gossip ? 'yes' : 'no')])
      : el('p', { class: 'muted' }, ['rendezvous off']));
  }

  function dhtSection(status) {
    const d = status && !status.__err ? status.dht : undefined;
    return section('DHT', d
      ? el('div', {}, [kv('good nodes', d.good_nodes), kv('nodes', d.nodes)])
      : el('p', { class: 'muted' }, ['DHT disabled']));
  }

  function publisherSection(status, publish) {
    const p = (status && !status.__err && status.publisher) || {};
    const pubRow = p.pubkey
      ? el('div', { class: 'kv' }, [
          el('span', { class: 'k' }, ['pubkey']),
          el('span', { class: 'v' }, [
            el('span', { class: 'mono', title: p.pubkey }, [shortHex(p.pubkey, 16)]),
            ' ',
            copyButton(p.pubkey),
          ]),
        ])
      : kv('pubkey', '(no identity)');
    const body = el('div', {}, [
      pubRow,
      kv('keywords', p.total_keywords || 0), kv('hits', p.total_hits || 0),
    ]);
    if (publish && !publish.__err && (publish.keywords || []).length) {
      const rows = publish.keywords.map((k) => el('tr', {}, [
        el('td', { class: 'mono' }, [k.keyword]), el('td', { class: 'num' }, [k.hits_count]),
        el('td', { class: 'num' }, [k.publish_count]),
        el('td', {}, [k.last_published ? timeAgo(k.last_published) : '—']),
        el('td', { class: 'err' }, [k.last_error || '']),
      ]));
      body.append(el('table', { class: 'kw' }, [
        el('thead', {}, [el('tr', {}, ['keyword', 'hits', 'pubs', 'last', 'error'].map((h) => el('th', {}, [h])))]),
        el('tbody', {}, rows),
      ]));
    }
    return section('Publisher (Layer D)', body);
  }

  function aggregateSection(agg) {
    if (agg === null) return section('Aggregate', el('p', { class: 'muted' }, ['aggregate backend off']));
    if (!agg || agg.__err) return section('Aggregate', el('p', { class: 'error' }, [agg ? agg.__err : 'unavailable']));
    const b = agg.bootstrap || {};
    return section('Aggregate', el('div', {}, [
      kv('known indexers', agg.known_indexers), kv('cache size', agg.cache_size),
      kv('bootstrap', `anchors ${b.anchors} · admitted ${b.admitted} · pending ${b.pending}`),
      kv('reconciliation', agg.reconciliation ? 'advertised' : 'off'),
      kv('services', agg.services),
    ]));
  }

  function reputationSection(status) {
    const r = status && !status.__err ? status.reputation : undefined;
    if (!r) return section('Reputation', el('p', { class: 'muted' }, ['reputation disabled']));
    const rows = (r.top_indexers || []).map((x) => el('tr', {}, [
      el('td', { class: 'mono' }, [shortHex(x.pubkey, 12)]), el('td', { class: 'num' }, [(x.score || 0).toFixed(3)]),
      el('td', { class: 'num' }, [x.hits_returned]), el('td', { class: 'num' }, [x.hits_confirmed]),
      el('td', { class: 'num' }, [x.hits_flagged]),
    ]));
    return section('Reputation', el('div', {}, [
      kv('known indexers', r.known_indexers),
      rows.length ? el('table', { class: 'kw' }, [
        el('thead', {}, [el('tr', {}, ['pubkey', 'score', 'ret', 'conf', 'flag'].map((h) => el('th', {}, [h])))]),
        el('tbody', {}, rows)]) : el('p', { class: 'muted' }, ['no indexers yet']),
    ]));
  }

  function bloomSection(status) {
    const b = status && !status.__err ? status.bloom : undefined;
    if (!b) return null;
    return section('Known-good filter', el('div', {}, [
      kv('bits', b.bit_size), kv('hash fns', b.hash_functions),
      kv('population', b.population_bits), kv('est. items', Math.round(b.estimated_items || 0)),
    ]));
  }

  async function refresh() {
    if (busy) return;
    busy = true;
    try {
      const [status, torrents, stats, publish, agg] = await Promise.all([
        settle(api.getStatus()), settle(api.getTorrents()), settle(api.getIndexStats()),
        settle(api.getPublish()), settle(api.getAggregate()),
      ]);
      clear(view);
      view.append(
        torrentsSection(torrents),
        localSection(status, stats),
        swarmSection(status),
        rendezvousSection(status),
        dhtSection(status),
        publisherSection(status, publish),
        aggregateSection(agg),
        reputationSection(status),
      );
      const bloom = bloomSection(status);
      if (bloom) view.append(bloom);
    } finally { busy = false; }
  }

  clear(container).append(view);
  return {
    // stopped guards against a tab switch during the initial in-flight refresh.
    async start() { await refresh(); if (stopped) return; timer = setInterval(refresh, POLL_MS); },
    stop() { stopped = true; clearInterval(timer); timer = null; },
  };
}
