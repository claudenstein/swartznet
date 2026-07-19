// companion.js — the Companion tab: publisher status, the follow list, and the
// follow / unfollow / re-publish actions. A 503 means the companion subsystem is
// off (the whole tab shows disabled); a 429 on refresh is the normal throttle,
// shown as informational, not an error.
import * as api from '../api.js';
import { el, clear, shortHex, timeAgo, unixAgo, isHex64, section, badge, toast } from '../util.js';

const POLL_MS = 3000;

export function render(container) {
  const view = el('div', { class: 'companion' }, ['Loading…']);
  let timer = null, busy = false, disabled = false, stopped = false;

  const pubkeyInput = el('input', { type: 'text', placeholder: 'publisher pubkey (64 hex)', class: 'grow mono' });
  const labelInput = el('input', { type: 'text', placeholder: 'label (optional)', class: 'label-in' });

  async function onFollow() {
    const pk = pubkeyInput.value.trim();
    if (!isHex64(pk)) { toast('pubkey must be 64 hex characters', 'err'); return; }
    try {
      await api.followPublisher(pk, labelInput.value.trim());
      pubkeyInput.value = ''; labelInput.value = '';
      toast('following ' + shortHex(pk, 12), 'ok');
      await refresh();
    } catch (e) { toast(e.message, 'err'); }
  }

  async function onUnfollow(pk) {
    try { await api.unfollowPublisher(pk); toast('unfollowed', 'ok'); await refresh(); }
    catch (e) { toast(e.message, 'err'); }
  }

  async function onRefresh() {
    try { await api.refreshCompanion(); toast('re-published', 'ok'); await refresh(); }
    catch (e) {
      if (e.status === 429) toast(e.message || 'refresh throttled (too soon)', '');
      else toast(e.message, 'err');
    }
  }

  function publisherCard(p) {
    const started = p.pubkey_hex && p.pubkey_hex.length > 0;
    const body = el('div', {}, [
      el('div', { class: 'kv' }, [el('span', { class: 'k' }, ['publisher']),
        el('span', { class: 'v mono' }, [started ? shortHex(p.pubkey_hex, 16) : 'not started'])]),
    ]);
    if (started) {
      body.append(
        rowKV('published', p.published_count || 0),
        rowKV('last refresh', p.last_refresh ? timeAgo(p.last_refresh) : 'never'),
        p.last_infohash ? rowKV('last infohash', shortHex(p.last_infohash, 16)) : null,
        p.last_error ? el('p', { class: 'error' }, [p.last_error]) : null,
      );
    }
    const actions = el('div', { class: 'row-controls' }, [
      el('button', { class: 'btn', onclick: onRefresh, disabled: !started }, ['Re-publish now']),
    ]);
    return section('Publisher', el('div', {}, [body, actions]));
  }

  function rowKV(k, v) {
    return el('div', { class: 'kv' }, [el('span', { class: 'k' }, [k]), el('span', { class: 'v' }, [String(v)])]);
  }

  function followCard(subs) {
    const add = el('div', { class: 'add-row' }, [pubkeyInput, labelInput, el('button', { class: 'btn primary', onclick: onFollow }, ['Follow'])]);
    const rows = (subs || []).map((s) => {
      const badges = [];
      if (s.torrents_imported) badges.push(badge(s.torrents_imported + ' torrents'));
      if (s.content_imported) badges.push(badge(s.content_imported + ' content'));
      if (s.generated_at) badges.push(badge('snap ' + unixAgo(s.generated_at)));
      return el('div', { class: 'follow' }, [
        el('div', { class: 'row-head' }, [
          el('div', { class: 'row-title mono', title: s.pubkey_hex }, [(s.label ? s.label + ' · ' : '') + shortHex(s.pubkey_hex, 16)]),
          el('button', { class: 'btn tiny', onclick: () => onUnfollow(s.pubkey_hex) }, ['Unfollow']),
        ]),
        el('div', { class: 'row-badges' }, badges),
        el('div', { class: 'row-meta' }, ['last sync: ' + (s.last_sync_at ? timeAgo(s.last_sync_at) : 'never')]),
        s.last_error ? el('p', { class: 'error' }, [s.last_error]) : null,
      ]);
    });
    return section('Followed publishers', el('div', {}, [
      add,
      rows.length ? el('div', { class: 'follow-list' }, rows) : el('p', { class: 'empty' }, ['Not following anyone. Add a 64-hex pubkey above.']),
    ]));
  }

  async function refresh() {
    if (busy) return;
    busy = true;
    try {
      const r = await api.getCompanion();
      disabled = false;
      clear(view).append(publisherCard(r.publisher || {}), followCard(r.subscriber || []));
    } catch (e) {
      if (e.status === 503) { disabled = true; clear(view).append(el('p', { class: 'muted big' }, ['Companion subsystem is disabled on this node.'])); }
      else { clear(view).append(el('p', { class: 'error' }, ['Failed: ' + e.message])); }
    } finally { busy = false; }
  }

  clear(container).append(view);
  return {
    // stopped guards against a tab switch during the initial in-flight refresh.
    async start() { await refresh(); if (stopped) return; timer = setInterval(() => { if (!disabled) refresh(); }, POLL_MS); },
    stop() { stopped = true; clearInterval(timer); timer = null; },
  };
}
