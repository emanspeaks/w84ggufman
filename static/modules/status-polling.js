// Status polling

import { formatBytes } from './utils.js';
import { setLlamaServiceLabel } from './service-restart.js';
import { downloadInProgress, setDownloadState, openSSE, setWarnThresholds } from './download.js';

export let diskFreeBytes = 0;
export let ramTotalBytes = 0;
export let llamaSwapEnabled = false;
export let atopwebURL = '';
export let llamaServerURL = '';
export let llamaServerLandingPage = '/';
export let modelsDir = '';
let statusConnectionReady = false;

function notifyLlamaServerURLChanged() {
  window.dispatchEvent(new CustomEvent('w84:llama-server-url-changed'));
}

function notifyStatusConnection(connected, reason = '') {
  if (statusConnectionReady === connected && !reason) return;
  statusConnectionReady = connected;
  window.dispatchEvent(new CustomEvent('w84:status-connection', {
    detail: { connected, reason }
  }));
}

// Last successfully received GPU / RAM values. Null = never received.
// Kept across polls so a single probe failure doesn't flash the row away.
let lastRamUsedBytes = null;
let lastGpuPct = null;
let initialVersion = null;

function bytesToGiB(bytes) {
  return bytes / (1024 ** 3);
}

function formatGiBValue(bytes) {
  const gib = bytesToGiB(bytes);
  // Keep compact whole numbers for large values; one decimal for smaller values.
  return gib >= 100 ? String(Math.round(gib)) : gib.toFixed(1);
}

function setHeaderConnectionState(connected) {
  const header = document.querySelector('header');
  if (!header) return;
  header.classList.toggle('server-disconnected', !connected);
}

export async function pollStatus() {
  try {
    const resp = await fetch('/api/status');
    if (!resp.ok) {
      setHeaderConnectionState(false);
      notifyStatusConnection(false, `status endpoint returned HTTP ${resp.status}`);
      return;
    }
    notifyStatusConnection(true);
    const s = await resp.json();
    setHeaderConnectionState(!!s.llamaReachable);
    const el = document.getElementById('status-indicator');
    if (s.llamaServiceLabel) {
      setLlamaServiceLabel(s.llamaServiceLabel);
      document.getElementById('restart-btn').textContent = 'Restart ' + s.llamaServiceLabel;
      document.getElementById('open-server-btn').textContent = 'Open ' + s.llamaServiceLabel + '…';
    }
    if (s.llamaServerURL != null) {
      const resolvedURL = resolveConfigURL(s.llamaServerURL);
      if (resolvedURL !== llamaServerURL) {
        llamaServerURL = resolvedURL;
        notifyLlamaServerURLChanged();
      }
    }
    if (s.llamaServerLandingPage != null) {
      llamaServerLandingPage = s.llamaServerLandingPage;
    }
    if (s.modelsDir != null) {
      modelsDir = s.modelsDir;
    }
    if (s.llamaSwapEnabled != null) {
      llamaSwapEnabled = s.llamaSwapEnabled;
      document.getElementById('edit-templates-btn').style.display = llamaSwapEnabled ? '' : 'none';
    }
    el.textContent = (s.llamaServiceLabel || 'llama-server') + ': ' + (s.llamaReachable ? 'online' : 'offline');
    el.className = 'status-indicator ' + (s.llamaReachable ? 'status-online' : 'status-offline');
    if (s.version) {
      const ver = document.getElementById('app-version');
      if (!ver.textContent) ver.textContent = s.version;
      if (initialVersion === null) {
        initialVersion = s.version;
      } else if (s.version !== initialVersion) {
        const banner = document.getElementById('version-banner');
        document.getElementById('version-banner-msg').textContent =
          `w84ggufman updated on server: ${initialVersion} → ${s.version}. Refresh the page to run the new version.`;
        banner.style.display = 'flex';
      }
    }
    if (s.atopwebURL != null) {
      atopwebURL = resolveConfigURL(s.atopwebURL);
      document.getElementById('ram-info').classList.toggle('clickable', !!s.atopwebURL);
    }
    if (s.disk && s.disk.totalBytes > 0) {
      diskFreeBytes = s.disk.freeBytes;
      const pct = Math.round(s.disk.usedBytes / s.disk.totalBytes * 100);
      const fill = document.getElementById('disk-bar-fill');
      fill.style.width = pct + '%';
      fill.className = 'resource-bar-fill ' + (pct >= 90 ? 'crit' : pct >= 75 ? 'warn' : 'ok');
      document.getElementById('disk-text').textContent = formatBytes(s.disk.freeBytes) + ' free';
      document.getElementById('disk-info').style.display = 'flex';
    }
    if (s.ramTotalBytes > 0) {
      ramTotalBytes = s.ramTotalBytes;
      const fill = document.getElementById('ram-bar-fill');
      const text = document.getElementById('ram-text');
      const totalGiB = formatGiBValue(s.ramTotalBytes);
      if (s.ramKnown) lastRamUsedBytes = s.ramUsedBytes;
      if (lastRamUsedBytes !== null) {
        const pct = Math.round(lastRamUsedBytes / s.ramTotalBytes * 100);
        fill.style.width = pct + '%';
        fill.className = 'resource-bar-fill ' + (pct >= 90 ? 'crit' : pct >= 75 ? 'warn' : 'ok');
        const usedGiB = formatGiBValue(lastRamUsedBytes);
        text.textContent = `RAM: ${pct}% (${usedGiB} / ${totalGiB} GiB)`;
      } else {
        fill.style.width = '0%';
        fill.className = 'resource-bar-fill ok';
        text.textContent = `RAM: ?% (? / ${totalGiB} GiB)`;
      }
      const gpuRow = document.getElementById('gpu-row');
      if (s.gpuPctKnown) lastGpuPct = Math.round(s.gpuPct);
      if (lastGpuPct !== null) {
        const gpuFill = document.getElementById('gpu-bar-fill');
        gpuFill.style.width = lastGpuPct + '%';
        gpuFill.className = 'resource-bar-fill ' + (lastGpuPct >= 90 ? 'crit' : lastGpuPct >= 75 ? 'warn' : 'ok');
        document.getElementById('gpu-text').textContent = 'GPU: ' + lastGpuPct + '%';
        gpuRow.style.display = 'flex';
      } else {
        gpuRow.style.display = 'none';
      }
      document.getElementById('ram-info').style.display = 'flex';
    }
    setWarnThresholds(s.warnDownloadBytes, s.warnRamBytes);
    // Update loaded state on model card alias pills without re-rendering
    if (s.loadedModels) {
      const loadedSet = new Set(s.loadedModels);
      document.querySelectorAll('.model-loaded-row .badge[data-alias]').forEach(pill => {
        if (pill.dataset.missing) return;
        const active = loadedSet.has(pill.dataset.alias);
        pill.classList.toggle('badge-active', active);
        pill.title = active ? 'Currently loaded' : 'Configured but not loaded';
      });
    }
    if (s.downloadInProgress && !document.getElementById('dl-progress-fill')) {
      setDownloadState(true);
      openSSE();
    } else if (!s.downloadInProgress && downloadInProgress) {
      setDownloadState(false);
    }
    const badge = document.getElementById('updates-badge');
    if (badge) {
      const count = s.updatesAvailable || 0;
      badge.style.display = count > 0 ? '' : 'none';
      badge.textContent = count === 1 ? 'Update available' : `${count} updates available`;
    }
  } catch (_) {
    setHeaderConnectionState(false);
    notifyStatusConnection(false, 'status request failed');
  }
}

export function setupStatusPolling() {
  document.getElementById('version-banner-refresh').addEventListener('click', () => location.reload());
  document.getElementById('version-banner-dismiss').addEventListener('click', () => {
    document.getElementById('version-banner').style.display = 'none';
  });
  setInterval(pollStatus, 5000);
}

// If the configured URL points at localhost, substitute the browser's hostname
// so that remote clients reach the right machine. Non-localhost URLs are used as-is.
function resolveConfigURL(url) {
  if (!url) return '';
  try {
    const u = new URL(url);
    if (u.hostname !== 'localhost' && u.hostname !== '127.0.0.1') return url;
    return `${u.protocol}//${window.location.hostname}${u.port ? ':' + u.port : ''}${u.pathname === '/' ? '' : u.pathname}`;
  } catch (_) {
    return url;
  }
}
