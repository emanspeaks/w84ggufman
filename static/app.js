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
import { pollStatus, setupStatusPolling, llamaSwapEnabled, atopwebURL, llamaServerURL } from './modules/status-polling.js';
import { cancelDownload } from './modules/download.js';
import { openFullConfigModal, openW84ConfigModal } from './modules/config-modal.js';
import { openDiskTreemap } from './modules/disk-treemap.js';

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

// Action button listeners
document.getElementById('refresh-btn').addEventListener('click', () => {
  fetchLocalModels();
  pollStatus();
});

document.getElementById('open-server-btn').addEventListener('click', () => {
  document.getElementById('status-menu').classList.remove('open');
  if (llamaServerURL) window.open(llamaServerURL, '_blank', 'noopener');
});

document.getElementById('edit-config-btn').addEventListener('click', () => {
  openFullConfigModal(llamaSwapEnabled).catch(console.error);
});

document.getElementById('edit-templates-btn').addEventListener('click', () => {
  openW84ConfigModal().catch(console.error);
});

document.getElementById('cancel-dl-btn').addEventListener('click', cancelDownload);

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
