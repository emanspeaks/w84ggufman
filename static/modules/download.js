// Download handling and SSE streaming

import { appendLogLine, setStatusBar, renderQueuePanel } from './status-bar.js';
import { formatBytes, formatETA } from './utils.js';

export let downloadInProgress = false;
export let activeEventSource = null;
export let warnDownloadBytes = 0;
export let warnRamBytes = 0;
let queueEntries = [];
let latestProgress = null;

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

function updateAggregateStatus() {
  const fill = document.getElementById('dl-progress-fill');
  if (!fill) return;

  const active = queueEntries.find(entry => entry.state === 'active');
  const activeBytes = Math.max(0, latestProgress?.dlBytes || active?.dlBytes || 0);

  let totalBytes = 0;
  let doneBytes = 0;
  queueEntries.forEach(entry => {
    if (entry.totalBytes > 0) {
      totalBytes += entry.totalBytes;
      if (entry.state === 'active') {
        doneBytes += Math.min(activeBytes, entry.totalBytes);
      }
    }
  });

  let pct = 0;
  let text = 'Queued';
  if (totalBytes > 0) {
    pct = Math.max(0, Math.min(100, Math.round((doneBytes / totalBytes) * 100)));
    text = `${pct}% (${formatBytes(doneBytes)} / ${formatBytes(totalBytes)})`;
  } else if (latestProgress) {
    if (latestProgress.pct >= 0) {
      pct = latestProgress.pct;
      text = latestProgress.pct + '%';
    } else {
      text = formatBytes(latestProgress.dlBytes || 0);
    }
  }

  if (latestProgress?.speed > 0) text += ' - ' + formatBytes(latestProgress.speed) + '/s';
  if (latestProgress?.eta > 0) text += ' (' + formatETA(latestProgress.eta) + ')';

  fill.style.width = pct + '%';
  document.getElementById('status-bar-label').textContent = 'Download';
  document.getElementById('status-bar-text').textContent = text;
}

function refreshQueuePanel() {
  const activeBytes = Math.max(0, latestProgress?.dlBytes || 0);
  const entries = queueEntries.map(entry => {
    const out = { ...entry };
    if (out.state === 'active') {
      out.dlBytes = activeBytes;
      if (out.totalBytes > 0) {
        out.pct = Math.max(0, Math.min(100, Math.round((activeBytes / out.totalBytes) * 100)));
      }
    }
    return out;
  });
  renderQueuePanel(entries);
}

export function openSSE() {
  if (activeEventSource) {
    activeEventSource.close();
    activeEventSource = null;
  }

  const es = new EventSource('/api/download/status');
  activeEventSource = es;
  queueEntries = [];
  latestProgress = null;
  const bar = document.getElementById('status-bar');
  if (bar) bar.classList.add('active');
  document.getElementById('status-bar-label').textContent = 'Download';
  document.getElementById('status-bar-text').textContent = 'Starting...';

  es.addEventListener('line', (e) => {
    appendLogLine(JSON.parse(e.data));
  });

  es.addEventListener('progress', (e) => {
    latestProgress = JSON.parse(e.data);
    refreshQueuePanel();
    updateAggregateStatus();
  });

  es.addEventListener('queue', (e) => {
    queueEntries = JSON.parse(e.data);
    refreshQueuePanel();
    updateAggregateStatus();
  });

  es.addEventListener('status', (e) => {
    const msg = JSON.parse(e.data);
    es.close();
    activeEventSource = null;
    document.getElementById('dl-progress-fill').style.width = '0%';
    queueEntries = [];
    latestProgress = null;
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
    queueEntries = [];
    latestProgress = null;
    setStatusBar('Error', 'Connection lost', false);
  };
}

// Stub for refreshDlBtn - will be set by repo-browser
export function refreshDlBtn() {
  export_refreshDlBtn?.();
}
