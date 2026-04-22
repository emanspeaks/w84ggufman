// Status polling

import { formatBytes } from './utils.js';
import { setLlamaServiceLabel } from './service-restart.js';
import { downloadInProgress, setDownloadState, openSSE, setWarnThresholds } from './download.js';

export let diskFreeBytes = 0;
export let llamaSwapEnabled = false;
export let atopwebURL = '';

// Last successfully received GPU / VRAM values. Null = never received.
// Kept across polls so a single probe failure doesn't flash the row away.
let lastVramUsedBytes = null;
let lastGpuPct = null;

export async function pollStatus() {
  try {
    const resp = await fetch('/api/status');
    if (!resp.ok) return;
    const s = await resp.json();
    const el = document.getElementById('status-indicator');
    if (s.llamaServiceLabel) {
      setLlamaServiceLabel(s.llamaServiceLabel);
      document.getElementById('restart-btn').textContent = 'Restart ' + s.llamaServiceLabel;
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
    }
    if (s.atopwebURL != null) {
      atopwebURL = resolveAtopwebURL(s.atopwebURL);
      document.getElementById('vram-info').classList.toggle('clickable', !!s.atopwebURL);
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
    if (s.vramBytes > 0) {
      const fill = document.getElementById('vram-bar-fill');
      const text = document.getElementById('vram-text');
      if (s.vramUsedKnown) lastVramUsedBytes = s.vramUsedBytes;
      if (lastVramUsedBytes !== null) {
        const pct = Math.round(lastVramUsedBytes / s.vramBytes * 100);
        fill.style.width = pct + '%';
        fill.className = 'resource-bar-fill ' + (pct >= 90 ? 'crit' : pct >= 75 ? 'warn' : 'ok');
        text.textContent = 'VRAM: ' + formatBytes(lastVramUsedBytes) + ' / ' + formatBytes(s.vramBytes);
      } else {
        fill.style.width = '0%';
        fill.className = 'resource-bar-fill ok';
        text.textContent = 'VRAM: ? / ' + formatBytes(s.vramBytes);
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
      document.getElementById('vram-info').style.display = 'flex';
    }
    setWarnThresholds(s.warnDownloadBytes, s.warnVramBytes);
    // Update loaded state on model card alias pills without re-rendering
    if (s.loadedModels) {
      const loadedSet = new Set(s.loadedModels);
      document.querySelectorAll('.model-loaded-row .badge[data-alias]').forEach(pill => {
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
  } catch (_) {}
}

export function setupStatusPolling() {
  setInterval(pollStatus, 5000);
}

function resolveAtopwebURL(configURL) {
  if (!configURL) return '';
  try {
    const u = new URL(configURL);
    return `${u.protocol}//${window.location.hostname}${u.port ? ':' + u.port : ''}${u.pathname === '/' ? '' : u.pathname}`;
  } catch (_) {
    return configURL;
  }
}
