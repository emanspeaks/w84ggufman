'use strict';

let downloadInProgress = false;
let activeEventSource = null;
let warnDownloadBytes = 0;
let warnVramBytes = 0;
let diskFreeBytes = 0;

// ── Theme ──────────────────────────────────────────────────────────────────

(function initTheme() {
  const stored = localStorage.getItem('theme');
  const isLight = stored === 'light';
  if (isLight) document.documentElement.classList.add('light');
  document.getElementById('theme-toggle').textContent = isLight ? '🌙' : '☀';
})();

document.getElementById('theme-toggle').addEventListener('click', () => {
  const isLight = document.documentElement.classList.toggle('light');
  localStorage.setItem('theme', isLight ? 'light' : 'dark');
  document.getElementById('theme-toggle').textContent = isLight ? '🌙' : '☀';
});

// ── Utilities ──────────────────────────────────────────────────────────────

function esc(str) {
  return String(str)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;')
    .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function formatBytes(n) {
  if (n == null) return 'unknown';
  if (n < 1024) return n + ' B';
  if (n < 1048576) return (n / 1024).toFixed(1) + ' KiB';
  if (n < 1073741824) return (n / 1048576).toFixed(1) + ' MiB';
  return (n / 1073741824).toFixed(2) + ' GiB';
}

function formatETA(secs) {
  if (secs < 60) return secs + 's';
  const m = Math.floor(secs / 60), s = secs % 60;
  return m + 'm ' + (s < 10 ? '0' : '') + s + 's';
}

function showErr(id, msg) {
  const el = document.getElementById(id);
  el.textContent = msg;
  el.style.display = msg ? 'block' : 'none';
}

function clearErr(id) { showErr(id, ''); }

// ── Status bar ─────────────────────────────────────────────────────────────

function appendLogLine(line) {
  const log = document.getElementById('status-log');
  const atBottom = log.scrollHeight - log.scrollTop <= log.clientHeight + 4;
  const span = document.createElement('span');
  span.className = 'log-line';
  span.textContent = line;
  log.appendChild(span);
  if (atBottom) log.scrollTop = log.scrollHeight;
}

function setStatusBar(label, text, active) {
  const bar = document.getElementById('status-bar');
  document.getElementById('status-bar-label').textContent = label;
  document.getElementById('status-bar-text').textContent = text;
  bar.classList.toggle('active', active);
  if (text) appendLogLine(text);
}

function toggleStatusBar() {
  const bar = document.getElementById('status-bar');
  bar.classList.toggle('expanded');
  if (bar.classList.contains('expanded')) {
    const log = document.getElementById('status-log');
    log.scrollTop = log.scrollHeight;
  }
}

document.getElementById('status-bar-main').addEventListener('click', toggleStatusBar);

// ── Log resize ─────────────────────────────────────────────────────────────

(function initResize() {
  const handle = document.getElementById('status-resize-handle');
  let startY = 0, startHeight = 200;

  handle.addEventListener('mousedown', (e) => {
    e.preventDefault();
    startY = e.clientY;
    startHeight = parseInt(getComputedStyle(document.documentElement)
      .getPropertyValue('--log-height'), 10) || 200;
    handle.classList.add('dragging');

    function onMove(e) {
      const height = Math.max(80, Math.min(600, startHeight + (startY - e.clientY)));
      document.documentElement.style.setProperty('--log-height', height + 'px');
    }
    function onUp() {
      handle.classList.remove('dragging');
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
    }
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  });
})();

// Keep body padding-bottom equal to the status bar height so content is
// never obscured by the fixed bar, whether collapsed, expanded, or resized.
(function() {
  const bar = document.getElementById('status-bar');
  new ResizeObserver(() => {
    document.body.style.paddingBottom = bar.offsetHeight + 'px';
  }).observe(bar);
})();

// ── Local models ───────────────────────────────────────────────────────────

async function fetchLocalModels() {
  clearErr('local-error');
  try {
    const resp = await fetch('/api/local');
    if (!resp.ok) throw new Error(await resp.text());
    const models = await resp.json();
    renderLocalModels(models);
  } catch (e) {
    showErr('local-error', 'Failed to load local models: ' + e.message);
    document.getElementById('local-list').innerHTML = '';
  }
}

function renderLocalModels(models) {
  const list = document.getElementById('local-list');
  if (!models.length) {
    list.innerHTML = '<p class="msg-empty">No models found in models directory.</p>';
    return;
  }
  list.innerHTML = '';
  for (const m of models) {
    const card = document.createElement('div');
    card.className = 'model-card' + (m.repoId ? ' clickable' : '');
    card.dataset.modelName = m.name;
    const visionBadge = m.isVision ? ' <span class="badge badge-vision">Vision</span>' : '';
    const presetWarn = m.isVision && !m.inPreset
      ? ' <span class="badge badge-warn-preset" title="Not in models.ini — add to preset so llama-server loads it">Preset missing</span>'
      : '';
    const repoLine = m.repoId
      ? `<span class="model-repo-id">${esc(m.repoId)}</span>`
      : `<span class="model-repo-id model-repo-unknown">(source unknown)</span>`;
    card.innerHTML = `
      <div class="model-meta">
        <span class="model-name">${esc(m.name)}${visionBadge}${presetWarn}</span>
        ${repoLine}
        <span class="model-detail">
          ${esc(formatBytes(m.sizeBytes))} &middot; ${m.files.length} file${m.files.length !== 1 ? 's' : ''}
          &nbsp;<span class="badge loaded-badge ${m.loaded ? 'badge-loaded' : 'badge-unloaded'}" title="Loaded = currently active in llama-server">${m.loaded ? 'Loaded' : 'Unloaded'}</span>
        </span>
      </div>
      <div class="model-actions">
        <button class="btn-secondary config-btn">Config…</button>
        <button class="btn-danger delete-btn">Delete</button>
      </div>
    `;
    if (m.repoId) {
      card.addEventListener('click', (e) => {
        if (e.target.closest('button')) return;
        document.getElementById('repo-input').value = m.repoId;
        browseRepo();
      });
    }
    card.querySelector('.config-btn').addEventListener('click', () => openConfigModal(m.name));
    card.querySelector('.delete-btn').addEventListener('click', () => deleteModel(m.name));
    list.appendChild(card);
  }
}

async function deleteModel(name) {
  if (!confirm(`Delete model "${name}"?\n\nThis will remove all files and cannot be undone.`)) return;
  clearErr('local-error');
  try {
    const resp = await fetch('/api/local/' + encodeURIComponent(name), { method: 'DELETE' });
    if (resp.status === 404) { showErr('local-error', 'Model not found.'); return; }
    if (!resp.ok) throw new Error(await resp.text());
    setStatusBar('Ready', 'Model deleted, restarting service…', false);
    fetchLocalModels();
  } catch (e) {
    showErr('local-error', 'Delete failed: ' + e.message);
  }
}

// ── Repo browser ───────────────────────────────────────────────────────────

async function browseRepo() {
  const repoId = document.getElementById('repo-input').value.trim();
  if (!repoId) return;
  clearErr('repo-error');
  const results = document.getElementById('repo-results');
  results.innerHTML = '<p class="msg-loading">Fetching file list…</p>';
  fetchAndRenderReadme(repoId);
  try {
    const resp = await fetch('/api/repo?id=' + encodeURIComponent(repoId));
    if (!resp.ok) throw new Error(await resp.text());
    const info = await resp.json();
    renderRepoInfo(repoId, info);
  } catch (e) {
    results.innerHTML = '';
    showErr('repo-error', 'Failed to fetch repo: ' + e.message);
  }
}

// ── Quant grid helpers ──────────────────────────────────────────────────────

function commonPrefix(arr) {
  if (!arr.length) return '';
  return arr.slice(1).reduce((p, s) => {
    while (!s.startsWith(p)) p = p.slice(0, -1);
    return p;
  }, arr[0]);
}

// Extract the display label for a quant tile (the part after the model prefix).
function quantDisplayName(filename, prefix) {
  const base = filename.replace(/\.gguf$/i, '');
  const tail = base.startsWith(prefix) ? base.slice(prefix.length).replace(/^[-_]/, '') : base;
  return tail || base;
}

// Bit depth is the first digit run after a known quant family prefix.
// Known families: IQ, TQ, BF, MXFP, NVFP, Q, F (longest-first for alternation).
// Strips leading UD- variant prefix, then matches at start-of-string (when the
// common prefix has been stripped) or after a separator (when the full filename
// is used as the display name because there was no common prefix to strip).
function quantBitDepth(displayName) {
  const m = displayName.replace(/^UD-/i, '').match(/(?:^|[-_.])(?:IQ|TQ|BF|MXFP|NVFP|[QF])(\d+)/i);
  return m ? parseInt(m[1]) : 999;
}

function renderRepoInfo(repoId, info) {
  const results = document.getElementById('repo-results');
  const files = info.models || [];
  const sidecars = info.sidecars || [];
  if (!files.length && !sidecars.length) {
    results.innerHTML = '<p class="msg-empty">No GGUF files found in this repo.</p>';
    return;
  }
  results.innerHTML = '';

  // Tags row
  const tagItems = [];
  if (info.pipelineTag) tagItems.push(`<span class="badge badge-pipeline">${esc(info.pipelineTag)}</span>`);
  for (const t of (info.tags || [])) {
    if (t !== info.pipelineTag) tagItems.push(`<span class="badge badge-tag">${esc(t)}</span>`);
  }
  if (tagItems.length) {
    const row = document.createElement('div');
    row.className = 'tags-row';
    row.innerHTML = tagItems.join('');
    results.appendChild(row);
  }

  if (info.isVision) {
    const hdr = document.createElement('div');
    hdr.style.cssText = 'padding:4px 0 8px;font-size:0.78rem;';
    if (sidecars.length > 0) {
      hdr.innerHTML = '<span class="badge badge-vision">Vision</span> &nbsp;<span style="color:#7dd3fc">Multimodal projector included — select companion files below.</span>';
    } else {
      hdr.innerHTML = '<span class="badge badge-vision">Vision</span> &nbsp;<span style="color:#fbbf24">No companion files in this repo — check the model card for the mmproj source.</span>';
    }
    results.appendChild(hdr);
  }

  if (files.length) {
    // Compute display prefix: common prefix of all base filenames (without .gguf).
    const bases = files.map(f => f.filename.replace(/\.gguf$/i, ''));
    const prefix = commonPrefix(bases).replace(/[-_]+$/, '');

    // Group files by bit-depth for the grid rows.
    const byBit = new Map();
    for (const f of files) {
      const label = f.displayName || quantDisplayName(f.filename, prefix);
      const bits  = quantBitDepth(label);
      if (!byBit.has(bits)) byBit.set(bits, []);
      byBit.get(bits).push({ f, label });
    }
    const sortedBits = [...byBit.keys()].sort((a, b) => a - b);

    // Track which filenames are selected.
    const selected = new Set();

    const grid = document.createElement('div');
    grid.className = 'quant-grid';

    for (const bits of sortedBits) {
      const group = byBit.get(bits);
      const row = document.createElement('div');
      row.className = 'quant-row';

      const bitLabel = document.createElement('span');
      bitLabel.className = 'quant-bit-label';
      bitLabel.textContent = bits === 999 ? '?' : bits + '-bit';
      row.appendChild(bitLabel);

      const tilesWrap = document.createElement('div');
      tilesWrap.className = 'quant-tiles';

      for (const { f, label } of group) {
        const tooBig = diskFreeBytes > 0 && f.size != null && f.size > diskFreeBytes;
        const vramWarn = warnVramBytes > 0 && f.size != null && f.size > warnVramBytes;
        const tile = document.createElement('span');
        tile.className = 'quant-tile' + (tooBig ? ' toobig' : '') + (vramWarn ? ' vramwarn' : '');
        tile.title = f.filename;
        tile.innerHTML =
          `<span class="quant-tile-name">${esc(label)}</span>` +
          `<span class="quant-tile-size">${vramWarn ? '⚠ ' : ''}${esc(formatBytes(f.size))}</span>`;

        tile.addEventListener('click', () => {
          if (selected.has(f.filename)) {
            selected.delete(f.filename);
            tile.classList.remove('selected');
          } else {
            selected.add(f.filename);
            tile.classList.add('selected');
          }
          refreshDlBtn();
        });
        tilesWrap.appendChild(tile);
      }
      row.appendChild(tilesWrap);
      grid.appendChild(row);
    }
    results.appendChild(grid);

    // Companion files section (shared across all quants).
    let companionEl = null;
    if (sidecars.length > 0) {
      companionEl = document.createElement('div');
      companionEl.className = 'companion-section';
      const items = sidecars.map((s, i) =>
        `<div class="sidecar-row">
          <input type="checkbox" class="sidecar-cb" data-idx="${i}" checked>
          <span class="sidecar-name">${esc(s.filename)}</span>
          ${s.size != null ? `<span class="sidecar-size">${esc(formatBytes(s.size))}</span>` : ''}
        </div>`
      ).join('');
      companionEl.innerHTML = `<span class="sidecars-label">Additional files:</span><div class="sidecars-wrap">${items}</div>`;
      results.appendChild(companionEl);
    }

    // Single download button row at the bottom.
    const dlRow = document.createElement('div');
    dlRow.className = 'dl-action-row';
    const hint = document.createElement('span');
    hint.className = 'dl-selection-hint';
    hint.textContent = 'Select one or more quants above';
    const dlBtn = document.createElement('button');
    dlBtn.className = 'btn-primary';
    dlBtn.disabled = true;
    dlBtn.textContent = 'Download';

    function refreshDlBtn() {
      const n = selected.size;
      dlBtn.disabled = downloadInProgress || n === 0;
      hint.textContent = n === 0 ? 'Select one or more quants above'
        : n === 1 ? '1 quant selected'
        : n + ' quants selected';
      dlBtn.textContent = n > 1 ? `Download (${n})` : 'Download';
    }

    dlBtn.addEventListener('click', () => {
      const selFiles = files.filter(f => selected.has(f.filename));
      const checkedSidecars = companionEl
        ? [...companionEl.querySelectorAll('.sidecar-cb:checked')].map(cb => sidecars[+cb.dataset.idx].filename)
        : [];
      const sidecarSize = companionEl
        ? [...companionEl.querySelectorAll('.sidecar-cb:checked')].reduce((s, cb) => s + (sidecars[+cb.dataset.idx].size || 0), 0)
        : 0;
      const totalSize = selFiles.reduce((s, f) => s + (f.size || 0), 0) + sidecarSize;
      startDownload(repoId, selFiles.map(f => f.filename), checkedSidecars, totalSize);
    });

    dlRow.appendChild(hint);
    dlRow.appendChild(dlBtn);
    results.appendChild(dlRow);
  }
}

// ── Download ───────────────────────────────────────────────────────────────

async function startDownload(repoId, filenames, sidecarFiles, totalSize, force) {
  if (downloadInProgress) return;
  if (!Array.isArray(filenames)) filenames = [filenames]; // compat
  if (warnDownloadBytes > 0 && totalSize != null && totalSize > warnDownloadBytes) {
    const gb = (totalSize / 1073741824).toFixed(2);
    if (!confirm(`This download is ${gb} GiB. Continue?`)) return;
  }
  if (warnVramBytes > 0 && totalSize != null && totalSize > warnVramBytes) {
    const gib = (totalSize / (1024 ** 3)).toFixed(2);
    if (!confirm(`This model (${gib} GiB) may exceed your VRAM limit. Continue anyway?`)) return;
  }
  const body = { repoId, filenames, totalBytes: totalSize || 0 };
  if (sidecarFiles && sidecarFiles.length) body.sidecarFiles = sidecarFiles;
  if (force) body.force = true;
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
  if (resp.status === 409) {
    let conflict;
    try { conflict = await resp.json(); } catch (_) { conflict = {}; }
    if (conflict.conflict === 'exists') {
      const repoLine = conflict.existingRepoId ? `\nOriginally from: ${conflict.existingRepoId}` : '';
      if (!confirm(`Model "${conflict.modelName}" already exists.${repoLine}\n\nReplace it with a fresh download?`)) return;
      return startDownload(repoId, filenames, sidecarFiles, totalSize, true);
    }
    setStatusBar('Error', 'Download conflict: ' + (conflict.message || resp.statusText), false);
    return;
  }
  if (!resp.ok) {
    setStatusBar('Error', 'Failed to start download: ' + (await resp.text()), false);
    return;
  }
  setDownloadState(true);
  setStatusBar('Download', 'Starting…', true);
  openSSE();
}

async function cancelDownload() {
  await fetch('/api/download/cancel', { method: 'POST' });
}

function openSSE() {
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

  es.addEventListener('status', (e) => {
    const msg = JSON.parse(e.data);
    es.close();
    activeEventSource = null;
    document.getElementById('dl-progress-fill').style.width = '0%';
    if (msg.status === 'done') {
      setStatusBar('Ready', 'Download complete', false);
      setDownloadState(false);
      fetchLocalModels();
      pollStatus();
    } else if (msg.status === 'idle') {
      setStatusBar('Ready', '', false);
      setDownloadState(false);
    }
  });

  es.onerror = () => {
    es.close();
    activeEventSource = null;
    document.getElementById('dl-progress-fill').style.width = '0%';
    setStatusBar('Error', 'Connection lost', false);
    // Do not clear downloadInProgress — pollStatus will reconnect SSE if the
    // download is still running on the server.
  };
}

function setDownloadState(inProgress) {
  downloadInProgress = inProgress;
  document.querySelectorAll('.dl-btn').forEach(btn => { btn.disabled = inProgress; });
  document.getElementById('cancel-dl-btn').style.display = inProgress ? 'inline-block' : 'none';
}

// ── Status pill menu ───────────────────────────────────────────────────────

function toggleStatusMenu(e) {
  e.stopPropagation();
  document.getElementById('status-menu').classList.toggle('open');
}

document.addEventListener('click', () => {
  document.getElementById('status-menu').classList.remove('open');
});

document.getElementById('status-indicator').addEventListener('click', toggleStatusMenu);

// ── Restart service ────────────────────────────────────────────────────────

async function restartService() {
  document.getElementById('status-menu').classList.remove('open');
  if (!confirm('Restart llama-server service?\n\nThe server will be briefly unavailable.')) return;
  const btn = document.getElementById('restart-btn');
  btn.disabled = true;
  setStatusBar('Restart', 'Restarting llama-server…', true);
  try {
    const resp = await fetch('/api/restart', { method: 'POST' });
    if (!resp.ok) throw new Error(await resp.text());
    setStatusBar('Ready', 'Service restarted successfully', false);
    pollStatus();
  } catch (e) {
    setStatusBar('Error', 'Restart failed: ' + e.message, false);
  } finally {
    btn.disabled = false;
  }
}

async function restartSelf() {
  document.getElementById('status-menu').classList.remove('open');
  if (!confirm('Restart w84ggufman?\n\nThe page will reload automatically once it comes back up.')) return;
  const btn = document.getElementById('restart-self-btn');
  btn.disabled = true;
  setStatusBar('Restart', 'Restarting w84ggufman…', true);
  try {
    await fetch('/api/restart-self', { method: 'POST' });
  } catch (_) { /* process dying causes network error — that's expected */ }
  // Wait for the server to go down then come back up.
  await new Promise(r => setTimeout(r, 1200));
  for (let i = 0; i < 30; i++) {
    try {
      const r = await fetch('/api/status');
      if (r.ok) { location.reload(); return; }
    } catch (_) {}
    await new Promise(r => setTimeout(r, 1000));
  }
  setStatusBar('Error', 'w84ggufman did not come back — check the service logs', false);
  btn.disabled = false;
}

// ── Config modal ───────────────────────────────────────────────────────────

async function openConfigModal(name) {
  let body = '';
  try {
    const resp = await fetch('/api/preset/raw/' + encodeURIComponent(name));
    if (resp.ok) body = await resp.text();
  } catch (_) {}

  const backdrop = document.createElement('div');
  backdrop.className = 'modal-backdrop';
  backdrop.innerHTML = `
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="modal-title">
      <div class="modal-title" id="modal-title">
        Config
        <small>${esc(name)}</small>
      </div>
      <textarea spellcheck="false" placeholder="; llama-server preset settings for this model\nmodel = /path/to/model.gguf\nctx-size = 65536">${esc(body)}</textarea>
      <div class="modal-actions">
        <button class="btn-secondary" id="modal-cancel">Cancel</button>
        <button class="btn-primary" id="modal-save">Save</button>
      </div>
    </div>
  `;

  const ta = backdrop.querySelector('textarea');

  function closeModal() {
    document.body.removeChild(backdrop);
    document.removeEventListener('keydown', onKey);
  }

  function onKey(e) {
    if (e.key === 'Escape') closeModal();
  }

  backdrop.querySelector('#modal-cancel').addEventListener('click', closeModal);

  backdrop.querySelector('#modal-save').addEventListener('click', async () => {
    const saveBtn = backdrop.querySelector('#modal-save');
    saveBtn.disabled = true;
    saveBtn.textContent = 'Saving…';
    try {
      const resp = await fetch('/api/preset/raw/' + encodeURIComponent(name), {
        method: 'PUT',
        headers: { 'Content-Type': 'text/plain' },
        body: ta.value,
      });
      if (!resp.ok) throw new Error(await resp.text());
      closeModal();
      setStatusBar('Ready', 'Config saved for ' + name, false);
    } catch (e) {
      setStatusBar('Error', 'Save failed: ' + e.message, false);
      saveBtn.disabled = false;
      saveBtn.textContent = 'Save';
    }
  });

  // Close on backdrop click (outside the modal box).
  backdrop.addEventListener('click', (e) => { if (e.target === backdrop) closeModal(); });
  document.addEventListener('keydown', onKey);

  document.body.appendChild(backdrop);
  ta.focus();
  ta.setSelectionRange(0, 0);
}

// ── README ─────────────────────────────────────────────────────────────────

async function fetchAndRenderReadme(repoId) {
  const content = document.getElementById('readme-content');
  const label = document.getElementById('readme-repo-label');
  label.textContent = repoId;
  content.innerHTML = '<p class="msg-loading">Loading…</p>';
  try {
    const resp = await fetch('/api/readme?id=' + encodeURIComponent(repoId));
    if (resp.status === 404) {
      content.innerHTML = '<p class="msg-empty">No README found for this repo.</p>';
      return;
    }
    if (!resp.ok) throw new Error(await resp.text());
    content.innerHTML = await resp.text();
  } catch (e) {
    content.innerHTML = `<p class="msg-error">Failed to load README: ${esc(e.message)}</p>`;
  }
}

// ── Status polling ─────────────────────────────────────────────────────────

async function pollStatus() {
  try {
    const resp = await fetch('/api/status');
    if (!resp.ok) return;
    const s = await resp.json();
    const el = document.getElementById('status-indicator');
    el.textContent = s.llamaReachable ? 'llama-server: online' : 'llama-server: offline';
    el.className = 'status-indicator ' + (s.llamaReachable ? 'status-online' : 'status-offline');
    if (s.version) {
      const ver = document.getElementById('app-version');
      if (!ver.textContent) ver.textContent = s.version;
    }
    if (s.disk && s.disk.totalBytes > 0) {
      diskFreeBytes = s.disk.freeBytes;
      const pct = Math.round(s.disk.usedBytes / s.disk.totalBytes * 100);
      const fill = document.getElementById('disk-bar-fill');
      fill.style.width = pct + '%';
      fill.className = 'disk-bar-fill ' + (pct >= 90 ? 'crit' : pct >= 75 ? 'warn' : 'ok');
      document.getElementById('disk-text').textContent = formatBytes(s.disk.freeBytes) + ' free';
      document.getElementById('disk-info').style.display = 'flex';
    }

    if (s.vramBytes > 0) {
      const el = document.getElementById('vram-total-text');
      el.textContent = 'VRAM: ' + formatBytes(s.vramBytes);
      el.style.display = '';
    }
    if (s.warnDownloadBytes) warnDownloadBytes = s.warnDownloadBytes;
    if (s.warnVramBytes) warnVramBytes = s.warnVramBytes;
    // Update loaded badges on existing cards without re-rendering
    if (s.loadedModels) {
      const loadedSet = new Set(s.loadedModels);
      document.querySelectorAll('[data-model-name]').forEach(card => {
        const badge = card.querySelector('.loaded-badge');
        if (!badge) return;
        const isLoaded = loadedSet.has(card.dataset.modelName);
        badge.className = 'badge loaded-badge ' + (isLoaded ? 'badge-loaded' : 'badge-unloaded');
        badge.textContent = isLoaded ? 'Loaded' : 'Unloaded';
      });
    }
    if (s.downloadInProgress && !activeEventSource) {
      setDownloadState(true);
      openSSE();
    } else if (!s.downloadInProgress && downloadInProgress && !activeEventSource) {
      setDownloadState(false);
    }
  } catch (_) { /* network error, ignore */ }
}

// ── Init ───────────────────────────────────────────────────────────────────

document.getElementById('refresh-btn').addEventListener('click', () => { fetchLocalModels(); pollStatus(); });
document.getElementById('browse-btn').addEventListener('click', browseRepo);
document.getElementById('restart-btn').addEventListener('click', restartService);
document.getElementById('restart-self-btn').addEventListener('click', restartSelf);
document.getElementById('cancel-dl-btn').addEventListener('click', cancelDownload);
document.getElementById('status-menu').addEventListener('click', (e) => e.stopPropagation());
document.getElementById('repo-input').addEventListener('keydown', (e) => {
  if (e.key === 'Enter') browseRepo();
});

fetchLocalModels();
pollStatus();
setInterval(pollStatus, 5000);
