// ─────────────────────────────────────────────────────────────────────────────
// w84ggufman - Main Application Module
// ─────────────────────────────────────────────────────────────────────────────
// This file coordinates app initialization by importing and setting up all
// functional modules. Each module (theme, status bar, repo browser, etc.) is
// independent and exposes setup functions.

import { initTheme, setupThemeToggle } from './modules/theme.js';
import { setupStatusBar } from './modules/status-bar.js';
import { setupStatusMenu } from './modules/status-menu.js';
import { setupRestartButtons } from './modules/service-restart.js';
import { setupRepoBrowser } from './modules/repo-browser.js';
import { fetchLocalModels } from './modules/local-models.js';
import { pollStatus, setupStatusPolling, llamaSwapEnabled, atopwebURL, llamaServerURL, llamaServerLandingPage } from './modules/status-polling.js';
import { cancelDownload } from './modules/download.js';
import { openFullConfigModal, openW84ConfigModal } from './modules/config-modal.js';
import { openDiskTreemap } from './modules/disk-treemap.js';

function syncChromeLayoutVars() {
  const root = document.documentElement;
  const banner = document.getElementById('version-banner');
  const header = document.querySelector('header');
  const statusBar = document.getElementById('status-bar');

  const bannerVisible = !!banner && getComputedStyle(banner).display !== 'none';
  const bannerHeight = bannerVisible ? banner.offsetHeight : 0;
  const headerHeight = header ? header.offsetHeight : 0;
  const statusHeight = statusBar ? statusBar.offsetHeight : 0;

  root.style.setProperty('--banner-height', bannerHeight + 'px');
  root.style.setProperty('--header-height', headerHeight + 'px');
  root.style.setProperty('--top-chrome-height', (bannerHeight + headerHeight) + 'px');
  root.style.setProperty('--bottom-chrome-height', statusHeight + 'px');
  document.body.style.paddingBottom = statusHeight + 'px';
}

function setupChromeLayoutSync() {
  syncChromeLayoutVars();

  const banner = document.getElementById('version-banner');
  const header = document.querySelector('header');
  const statusBar = document.getElementById('status-bar');
  const ro = new ResizeObserver(syncChromeLayoutVars);
  if (banner) ro.observe(banner);
  if (header) ro.observe(header);
  if (statusBar) ro.observe(statusBar);

  const mo = new MutationObserver(syncChromeLayoutVars);
  if (banner) mo.observe(banner, { attributes: true, attributeFilter: ['style', 'class'] });

  window.addEventListener('resize', syncChromeLayoutVars, { passive: true });
  window.addEventListener('orientationchange', syncChromeLayoutVars, { passive: true });
}

// ─────────────────────────────────────────────────────────────────────────────
// INITIALIZATION
// ─────────────────────────────────────────────────────────────────────────────

// Theme setup (must run before any display)
initTheme();
setupThemeToggle();

// UI component setup
setupStatusBar();
setupStatusMenu();
setupRepoBrowser();
setupRestartButtons();
setupChromeLayoutSync();

// Action button listeners
document.getElementById('refresh-btn').addEventListener('click', () => {
  fetchLocalModels();
  pollStatus();
});

document.getElementById('open-server-btn').addEventListener('click', () => {
  document.getElementById('status-menu').classList.remove('open');
  if (llamaServerURL) window.open(llamaServerURL + llamaServerLandingPage, '_blank', 'noopener');
});

document.getElementById('edit-config-btn').addEventListener('click', () => {
  openFullConfigModal(llamaSwapEnabled).catch(console.error);
});

document.getElementById('edit-templates-btn').addEventListener('click', () => {
  openW84ConfigModal().catch(console.error);
});

document.getElementById('cancel-dl-btn').addEventListener('click', cancelDownload);

document.getElementById('updates-badge').addEventListener('click', async () => {
  if (!confirm('Updates are available for one or more model repos.\n\nWould you like to update all outdated repos now? (Individual repos can be updated from the Select Models pane.)')) return;
  try {
    const resp = await fetch('/api/updates/apply', { method: 'POST' });
    if (!resp.ok) throw new Error(await resp.text());
    const { queued } = await resp.json();
    import('./modules/status-bar.js').then(m => m.setStatusBar('Ready', `Queued updates for ${queued} repo${queued !== 1 ? 's' : ''}`, false));
    fetchLocalModels();
    pollStatus();
  } catch (e) {
    import('./modules/status-bar.js').then(m => m.setStatusBar('Error', 'Failed to queue updates: ' + e.message, false));
  }
});

document.getElementById('disk-info').addEventListener('click', () => {
  openDiskTreemap().catch(console.error);
});

document.getElementById('ram-info').addEventListener('click', () => {
  if (atopwebURL) window.open(atopwebURL, '_blank', 'noopener');
});

// Initial data fetch
fetchLocalModels();
pollStatus();
setupStatusPolling();
