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
import { pollStatus, setupStatusPolling } from './modules/status-polling.js';
import { cancelDownload } from './modules/download.js';
import { openFullConfigModal, openTemplatesModal } from './modules/config-modal.js';

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

// Setup event listeners for action buttons
document.getElementById('refresh-btn').addEventListener('click', () => {
  fetchLocalModels();
  pollStatus();
});

document.getElementById('edit-config-btn').addEventListener('click', () => {
  openFullConfigModal();
});

document.getElementById('edit-templates-btn').addEventListener('click', () => {
  openTemplatesModal();
});

document.getElementById('cancel-dl-btn').addEventListener('click', () => {
  cancelDownload();
});

// Initial data fetch
fetchLocalModels();
pollStatus();
setupStatusPolling();
// ─────────────────────────────────────────────────────────────────────────────
// w84ggufman - Main Application Module
// ─────────────────────────────────────────────────────────────────────────────
// This file coordinates app initialization by importing and setting up all
// functional modules. Each module (theme, status bar, repo browser, etc.) is
// independent and exposes setup functions.

import { initTheme, setupThemeToggle } from './modules/theme.js';
import { setupStatusBar, setStatusBar, appendLogLine, toggleStatusBar } from './modules/status-bar.js';
import { setupStatusMenu } from './modules/status-menu.js';
import { setupRestartButtons } from './modules/service-restart.js';
import { setupRepoBrowser, browseRepo, browseLocalPath } from './modules/repo-browser.js';
import { fetchLocalModels, renderLocalModels, deleteRepo } from './modules/local-models.js';
import { pollStatus, setupStatusPolling } from './modules/status-polling.js';
import { fetchAndRenderReadme } from './modules/readme.js';
import { cancelDownload } from './modules/download.js';
import { openFullConfigModal, openTemplatesModal } from './modules/config-modal.js';

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

// Setup event listeners for action buttons
document.getElementById('refresh-btn').addEventListener('click', () => {
  fetchLocalModels();
  pollStatus();
});

document.getElementById('edit-config-btn').addEventListener('click', () => {
  openFullConfigModal();
});

document.getElementById('edit-templates-btn').addEventListener('click', () => {
  openTemplatesModal();
});

document.getElementById('cancel-dl-btn').addEventListener('click', () => {
  cancelDownload();
});

// Initial data fetch
fetchLocalModels();
pollStatus();
setupStatusPolling();
'use strict';

// Import all modules
import { cancelDownload } from './modules/download.js';
import { initTheme, setupThemeToggle } from './modules/theme.js';
import { setStatusBar, setupStatusBar } from './modules/status-bar.js';
import { fetchLocalModels } from './modules/local-models.js';
import { setupRepoBrowser } from './modules/repo-browser.js';
import { setupStatusMenu } from './modules/status-menu.js';
import { setupRestartButtons } from './modules/service-restart.js';
import { openFullConfigModal, openTemplatesModal } from './modules/config-modal.js';
import { pollStatus, setupStatusPolling } from './modules/status-polling.js';


// ─────────────────────────────────────────────────────────────────────────────
// INITIALIZATION
// ─────────────────────────────────────────────────────────────────────────────

// Theme: must run before display
(function() {
  initTheme();
  setupThemeToggle();
})();

// UI setup
setupStatusBar();
setupStatusMenu();
setupRepoBrowser();
setupRestartButtons();

// Action button listeners
document.getElementById('refresh-btn').addEventListener('click', () => {
  fetchLocalModels();
  pollStatus();
});

document.getElementById('edit-config-btn').addEventListener('click', () => {
  openFullConfigModal(false).catch(console.error);
});

document.getElementById('edit-templates-btn').addEventListener('click', () => {
  openTemplatesModal().catch(console.error);
});

document.getElementById('cancel-dl-btn').addEventListener('click', cancelDownload);

// Initial data load
fetchLocalModels();
pollStatus();
setupStatusPolling();
