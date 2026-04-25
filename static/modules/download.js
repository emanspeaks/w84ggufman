// Download handling and SSE streaming

import { setStatusBar, renderQueuePanel } from './status-bar.js';
import { formatBytes, formatETA } from './utils.js';

export let downloadInProgress = false;
export let activeEventSource = null;
export let warnDownloadBytes = 0;
export let warnRamBytes = 0;

export function setDownloadState(inProgress) {
  downloadInProgress = inProgress;
  document.getElementById('cancel-dl-btn').style.display = inProgress ? 'inline-block' : 'none';
  export_refreshDlBtn?.();
}

export function setWarnThresholds(downloadBytes, ramBytes) {
  if (downloadBytes != null) warnDownloadBytes = downloadBytes;
  if (ramBytes != null) warnRamBytes = ramBytes;
}

let export_refreshDlBtn = null;

export function setRefreshDlBtnExport(fn) {
  export_refreshDlBtn = fn;
}

export async function startDownload(repoId, filenames, sidecarFiles, totalSize) {
  if (!Array.isArray(filenames)) filenames = [filenames];
  if (warnDownloadBytes > 0 && totalSize != null && totalSize > warnDownloadBytes) {
    const gb = (totalSize / 1073741824).toFixed(2);
    if (!confirm(`This download is ${gb} GiB. Continue?`)) return;
  }
  if (warnRamBytes > 0 && totalSize != null && totalSize > warnRamBytes) {
    const gib = (totalSize / (1024 ** 3)).toFixed(2);
    if (!confirm(`This model (${gib} GiB) may exceed your RAM limit. Continue anyway?`)) return;
  }
  const body = { repoId, filenames, totalBytes: totalSize || 0 };
  if (sidecarFiles && sidecarFiles.length) body.sidecarFiles = sidecarFiles;
  let resp;
  try {
    resp = await fetch('/api/download', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  } catch (e) {
    setStatusBar('Error', 'Failed to start download: ' + e.message, false);
    return;
  }
  if (!resp.ok) {
    setStatusBar('Error', 'Failed to start download: ' + (await resp.text()), false);
    return;
  }
  const data = await resp.json();
  if (data.queued) {
    setStatusBar('Queued', filenames[0], true);
    // Ensure SSE is open so queue events flow to the panel
    if (!activeEventSource) openSSE();
    return;
  }
  setDownloadState(true);
  setStatusBar('Download', 'Starting…', true);
  openSSE();
}

export async function cancelDownload() {
  await fetch('/api/download/cancel', { method: 'POST' });
}

export function openSSE() {
  if (activeEventSource) {
    activeEventSource.close();
    activeEventSource = null;
  }

  const es = new EventSource('/api/download/status');
  activeEventSource = es;

  es.addEventListener('line', (e) => {
    setStatusBar('Download', JSON.parse(e.data), true);
  });

  es.addEventListener('progress', (e) => {
    const p = JSON.parse(e.data);
    const fill = document.getElementById('dl-progress-fill');
    if (p.pct >= 0) fill.style.width = p.pct + '%';
    let text = p.pct >= 0 ? p.pct + '%' : formatBytes(p.dlBytes);
    if (p.speed > 0) text += ' — ' + formatBytes(p.speed) + '/s';
    if (p.eta > 0) text += ' (' + formatETA(p.eta) + ')';
    document.getElementById('status-bar-label').textContent = 'Download';
    document.getElementById('status-bar-text').textContent = text;
  });

  es.addEventListener('queue', (e) => {
    renderQueuePanel(JSON.parse(e.data));
  });

  es.addEventListener('status', (e) => {
    const msg = JSON.parse(e.data);
    es.close();
    activeEventSource = null;
    document.getElementById('dl-progress-fill').style.width = '0%';
    if (msg.status === 'done') {
      renderQueuePanel([]);
      setStatusBar('Ready', 'Download complete', false);
      setDownloadState(false);
      import('./local-models.js').then(mod => mod.fetchLocalModels());
      import('./repo-browser.js').then(mod => mod.refreshCurrentRepoView());
      import('./status-polling.js').then(mod => mod.pollStatus());
    } else if (msg.status === 'idle') {
      renderQueuePanel([]);
      setStatusBar('Ready', '', false);
      setDownloadState(false);
    }
  });

  es.onerror = () => {
    es.close();
    activeEventSource = null;
    document.getElementById('dl-progress-fill').style.width = '0%';
    setStatusBar('Error', 'Connection lost', false);
  };
}

// Stub for refreshDlBtn - will be set by repo-browser
export function refreshDlBtn() {
  export_refreshDlBtn?.();
}
