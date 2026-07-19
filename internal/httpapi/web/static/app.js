// app.js — SPA entry point: a hash router over five tabs, each a page module
// exporting render(container) → { start(), stop() }. Only the visible tab runs
// its poll loop; switching tabs stops the old one and starts the new. The daemon
// serves only "/" (no SPA fallback), so tab state lives in the URL hash.
import { getHealthz } from './api.js';
import { el, clear } from './util.js';

import { render as downloads } from './pages/downloads.js';
import { render as search } from './pages/search.js';
import { render as status } from './pages/status.js';
import { render as companion } from './pages/companion.js';
import { render as settings } from './pages/settings.js';

const TABS = [
  { id: 'downloads', label: 'Downloads', render: downloads },
  { id: 'search', label: 'Search', render: search },
  { id: 'status', label: 'Status', render: status },
  { id: 'companion', label: 'Companion', render: companion },
  { id: 'settings', label: 'Settings', render: settings },
];

const view = document.getElementById('view');
const nav = document.getElementById('tabs');
let active = null; // { id, handle }

function currentTabId() {
  const id = (location.hash || '').replace(/^#/, '');
  return TABS.some((t) => t.id === id) ? id : TABS[0].id;
}

async function activate(id) {
  const tab = TABS.find((t) => t.id === id) || TABS[0];
  // Stop the previous tab's poll loop before switching.
  if (active && active.handle) { try { active.handle.stop(); } catch { /* ignore */ } }

  for (const btn of nav.children) btn.classList.toggle('active', btn.dataset.id === tab.id);

  clear(view);
  const handle = tab.render(view);
  active = { id: tab.id, handle };
  try { await handle.start(); } catch (e) { view.appendChild(el('p', { class: 'error' }, ['Tab failed: ' + e.message])); }
}

function buildTabs() {
  clear(nav);
  for (const t of TABS) {
    nav.appendChild(el('button', {
      class: 'tab', dataset: { id: t.id },
      onclick: () => { if (currentTabId() !== t.id) location.hash = t.id; else activate(t.id); },
    }, [t.label]));
  }
}

async function showVersion() {
  const badge = document.getElementById('version');
  try { const h = await getHealthz(); badge.textContent = h.version || ''; }
  catch { badge.textContent = 'offline'; badge.classList.add('offline'); }
}

window.addEventListener('hashchange', () => activate(currentTabId()));

buildTabs();
showVersion();
activate(currentTabId());
