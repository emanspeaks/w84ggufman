'use strict';

let downloadInProgress = false;
let activeEventSource = null;
let warnDownloadBytes = 0;
let warnVramBytes = 0;
let diskFreeBytes = 0;
let llamaSwapEnabled = false;
let llamaServiceLabel = 'llama-server';
let _refreshDlBtn = null; // set by renderRepoInfo so setDownloadState can re-evaluate

// ── Theme ──────────────────────────────────────────────────────────────────

(function initTheme() {
  if (localStorage.getItem('theme') === 'light')
    document.documentElement.classList.add('light');
})();

document.getElementById('theme-toggle').addEventListener('click', () => {
  const isLight = document.documentElement.classList.toggle('light');
  localStorage.setItem('theme', isLight ? 'light' : 'dark');
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
    card.className = 'model-card clickable';
    card.dataset.aliases = (m.loadedAliases || []).join(',');

    let titleText, titleClass = 'model-name';
    if (m.sourceUnknown) {
      titleText = m.path.split('/').pop() || m.path;
      titleClass = 'model-name model-name-unknown';
    } else if (m.isLocal) {
      titleText = m.repoId || m.path.split('/').pop() || m.path;
    } else if (m.repoId) {
      titleText = m.repoId;
    } else {
      titleText = m.path.split('/').pop() || m.path;
      titleClass = 'model-name model-name-unknown';
    }

    const badges = [];
    if (m.isLocal) badges.push(`<span class="badge badge-local">local</span>`);
    if (m.sourceUnknown) badges.push(`<span class="badge badge-warn-preset">unknown source</span>`);
    if (m.inConfig) badges.push(`<span class="badge badge-inconfig" title="Referenced in models.ini or config.yaml">In&nbsp;config</span>`);

    const loadedAliases = m.loadedAliases || [];
    const loadedHtml = loadedAliases.length > 0
      ? loadedAliases.map(a => `<span class="badge badge-loaded" title="Loaded preset alias from config file">${esc(a)}</span>`).join(' ')
      : `<span class="badge badge-unloaded">Unloaded</span>`;

    card.innerHTML = `
      <div class="model-meta">
        <span class="${titleClass}">${esc(titleText)}${badges.length ? ' ' + badges.join(' ') : ''}</span>
        <span class="model-detail">
          ${esc(formatBytes(m.sizeBytes))} &middot; ${m.files.length} file${m.files.length !== 1 ? 's' : ''}
        </span>
        <span class="model-loaded-row">${loadedHtml}</span>
      </div>
      <div class="model-actions">
        <button class="btn-danger delete-btn">Delete</button>
      </div>
    `;

    card.addEventListener('click', (e) => {
      if (e.target.closest('button')) return;
      if (m.repoId && !m.isLocal) {
        setRepoInput(m.repoId);
        browseRepo();
      } else {
        browseLocalPath(m.path, m.repoId || m.path.split('/').pop());
      }
    });

    card.querySelector('.delete-btn').addEventListener('click', () => deleteRepo(m.repoId, m.path));
    list.appendChild(card);
  }
}

async function deleteRepo(repoId, path) {
  const label = repoId || path;
  if (!confirm(`Delete "${label}"?\n\nThis will remove all files and cannot be undone.`)) return;
  clearErr('local-error');
  try {
    const resp = await fetch('/api/local?id=' + encodeURIComponent(repoId || path), { method: 'DELETE' });
    if (resp.status === 404) { showErr('local-error', 'Repo not found.'); return; }
    if (!resp.ok) throw new Error(await resp.text());
    setStatusBar('Ready', 'Deleted ' + label, false);
    fetchLocalModels();
  } catch (e) {
    showErr('local-error', 'Delete failed: ' + e.message);
  }
}

// ── Repo browser ───────────────────────────────────────────────────────────

function setRepoInput(value) {
  const input = document.getElementById('repo-input');
  input.value = value;
  updateHFLink(value);
}

function updateHFLink(repoId) {
  const btn = document.getElementById('hf-link-btn');
  if (repoId && repoId.includes('/') && !repoId.startsWith('/')) {
    btn.href = 'https://huggingface.co/' + repoId + '/tree/main';
    btn.style.opacity = '';
    btn.style.pointerEvents = '';
  } else {
    btn.href = '#';
    btn.style.opacity = '0.4';
    btn.style.pointerEvents = 'none';
  }
}

document.getElementById('repo-input').addEventListener('input', () => {
  updateHFLink(document.getElementById('repo-input').value.trim());
});

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

async function browseLocalPath(path, label) {
  clearErr('repo-error');
  setRepoInput(label || path);
  const results = document.getElementById('repo-results');
  results.innerHTML = '<p class="msg-loading">Loading local files…</p>';
  try {
    const resp = await fetch('/api/local-files?id=' + encodeURIComponent(path));
    if (!resp.ok) throw new Error(await resp.text());
    const info = await resp.json();
    renderRepoInfo(path, info);
  } catch (e) {
    results.innerHTML = '';
    showErr('repo-error', 'Failed to load: ' + e.message);
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

function quantDisplayName(filename, prefix) {
  const base = filename.replace(/\.gguf$/i, '');
  const tail = base.startsWith(prefix) ? base.slice(prefix.length).replace(/^[-_]/, '') : base;
  return tail || base;
}

function quantBitDepth(displayName) {
  const m = displayName.replace(/^UD-/i, '').match(/(?:^|[-_.])(?:IQ|TQ|BF|MXFP|NVFP|[QF])(\d+)/i);
  return m ? parseInt(m[1]) : 999;
}

// isPresentFile checks if a HF filename is already on disk.
// presentFiles contains local relative paths. Matching order:
// 1. Exact path match
// 2. Subdir quant: any present file shares the same directory prefix
// 3. Basename fallback: for files moved to a different subdir during migration
function isPresentFile(filename, presentFiles) {
  if (!filename || !presentFiles) return false;
  if (presentFiles.has(filename)) return true;
  const slash = filename.indexOf('/');
  if (slash >= 0) {
    const dir = filename.slice(0, slash + 1);
    for (const p of presentFiles) {
      if (p.startsWith(dir)) return true;
    }
    return false;
  }
  // Basename fallback for misplaced files (e.g. old Q8_0/file.gguf vs flat file.gguf).
  const base = filename.split('/').pop();
  for (const p of presentFiles) {
    if (p.split('/').pop() === base) return true;
  }
  return false;
}

function renderRepoInfo(repoId, info) {
  const results = document.getElementById('repo-results');
  const files = info.models || [];
  const sidecars = info.sidecars || [];
  const rogueFiles = info.rogueFiles || [];
  const presentFiles = new Set(info.presentFiles || []);

  if (!files.length && !sidecars.length && !rogueFiles.length) {
    results.innerHTML = '<p class="msg-empty">' +
      (info.localOnly ? 'No files found in this directory.' : 'No GGUF files found in this repo.') + '</p>';
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

  const quantCbs = [];
  const sidecarCbs = [];
  const rogueCbs = [];

  if (files.length) {
    const quantHdr = document.createElement('div');
    quantHdr.className = 'subsection-header';
    quantHdr.innerHTML =
      `<span class="subsection-label">Quant files</span>` +
      `<span class="select-btns">` +
        `<button class="btn-link">Select all</button>` +
        `<button class="btn-link">Deselect all</button>` +
      `</span>`;
    results.appendChild(quantHdr);
    const [selAllQ, deselAllQ] = quantHdr.querySelectorAll('.btn-link');

    const bases = files.map(f => f.displayName ? '' : f.filename.replace(/\.gguf$/i, ''));
    const prefix = commonPrefix(bases.filter(Boolean)).replace(/[-_]+$/, '');

    const byBit = new Map();
    for (const f of files) {
      const label = f.displayName || quantDisplayName(f.filename, prefix);
      const bits  = quantBitDepth(label);
      if (!byBit.has(bits)) byBit.set(bits, []);
      byBit.get(bits).push({ f, label });
    }
    const sortedBits = [...byBit.keys()].sort((a, b) => a - b);

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
        const isPresent = isPresentFile(f.filename, presentFiles);
        const tooBig   = diskFreeBytes > 0 && f.size != null && f.size > diskFreeBytes;
        const vramWarn = warnVramBytes > 0 && f.size != null && f.size > warnVramBytes;

        const tile = document.createElement('label');
        tile.className = 'quant-tile' +
          (tooBig   ? ' toobig'   : '') +
          (vramWarn ? ' vramwarn' : '') +
          (isPresent ? ' present' : '');
        tile.title = f.filename;

        const cb = document.createElement('input');
        cb.type = 'checkbox';
        cb.className = 'quant-cb';
        cb.dataset.filename = f.filename;
        cb.dataset.size = f.size || 0;
        cb.checked = isPresent;
        cb.addEventListener('change', refreshDlBtn);
        quantCbs.push(cb);

        tile.appendChild(cb);
        const nameSpan = document.createElement('span');
        nameSpan.className = 'quant-tile-name';
        nameSpan.textContent = label;
        const sizeSpan = document.createElement('span');
        sizeSpan.className = 'quant-tile-size';
        sizeSpan.textContent = (vramWarn ? '⚠ ' : '') + formatBytes(f.size) + (isPresent ? ' ✓' : '');
        tile.appendChild(nameSpan);
        tile.appendChild(sizeSpan);
        tilesWrap.appendChild(tile);
      }
      row.appendChild(tilesWrap);
      grid.appendChild(row);
    }
    results.appendChild(grid);

    selAllQ.addEventListener('click',   () => { quantCbs.forEach(cb => { cb.checked = true;  }); refreshDlBtn(); });
    deselAllQ.addEventListener('click', () => { quantCbs.forEach(cb => { cb.checked = false; }); refreshDlBtn(); });
  }

  if (sidecars.length > 0) {
    const companionEl = document.createElement('div');
    companionEl.className = 'companion-section';

    const sHdr = document.createElement('div');
    sHdr.className = 'subsection-header';
    sHdr.innerHTML =
      `<span class="subsection-label">Additional files</span>` +
      `<span class="select-btns">` +
        `<button class="btn-link">Select all</button>` +
        `<button class="btn-link">Deselect all</button>` +
      `</span>`;
    companionEl.appendChild(sHdr);
    const [selAllS, deselAllS] = sHdr.querySelectorAll('.btn-link');

    const wrap = document.createElement('div');
    wrap.className = 'sidecars-wrap';
    sidecars.forEach((s, i) => {
      const isPresent = isPresentFile(s.filename, presentFiles);
      const row = document.createElement('div');
      row.className = 'sidecar-row';

      const cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.className = 'sidecar-cb';
      cb.dataset.idx = i;
      cb.checked = isPresent;
      cb.addEventListener('change', refreshDlBtn);
      sidecarCbs.push(cb);

      row.appendChild(cb);
      const nameSpan = document.createElement('span');
      nameSpan.className = 'sidecar-name';
      nameSpan.textContent = s.filename;
      row.appendChild(nameSpan);
      if (s.size != null) {
        const sizeSpan = document.createElement('span');
        sizeSpan.className = 'sidecar-size';
        sizeSpan.textContent = formatBytes(s.size) + (isPresent ? ' ✓' : '');
        row.appendChild(sizeSpan);
      }
      wrap.appendChild(row);
    });
    companionEl.appendChild(wrap);

    selAllS.addEventListener('click',   () => { sidecarCbs.forEach(cb => { cb.checked = true;  }); refreshDlBtn(); });
    deselAllS.addEventListener('click', () => { sidecarCbs.forEach(cb => { cb.checked = false; }); refreshDlBtn(); });

    results.appendChild(companionEl);
  }

  // Rogue / local-only files section
  if (rogueFiles.length > 0) {
    const rogueHdr = document.createElement('div');
    rogueHdr.className = 'subsection-header';
    const rogueLabel = info.localOnly ? 'Local files' : 'Unrecognized local files';
    const rogueTitle = info.localOnly
      ? 'Files on disk (not on HuggingFace)'
      : 'These files exist locally but were not found in the HuggingFace listing';
    rogueHdr.innerHTML =
      `<span class="subsection-label" title="${esc(rogueTitle)}">${esc(rogueLabel)}</span>` +
      `<span class="select-btns">` +
        `<button class="btn-link">Select all</button>` +
        `<button class="btn-link">Deselect all</button>` +
      `</span>`;
    results.appendChild(rogueHdr);
    const [selAllR, deselAllR] = rogueHdr.querySelectorAll('.btn-link');

    const rogueWrap = document.createElement('div');
    rogueWrap.className = 'sidecars-wrap';
    rogueFiles.forEach(localPath => {
      const row = document.createElement('div');
      row.className = 'sidecar-row' + (info.localOnly ? '' : ' rogue-row');

      const cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.className = 'sidecar-cb';
      cb.dataset.localPath = localPath;
      cb.checked = true; // always present on disk
      cb.addEventListener('change', refreshDlBtn);
      rogueCbs.push(cb);

      row.appendChild(cb);
      const nameSpan = document.createElement('span');
      nameSpan.className = info.localOnly ? 'sidecar-name' : 'sidecar-name rogue-name';
      nameSpan.textContent = localPath;
      row.appendChild(nameSpan);
      rogueWrap.appendChild(row);
    });
    results.appendChild(rogueWrap);

    selAllR.addEventListener('click',   () => { rogueCbs.forEach(cb => { cb.checked = true;  }); refreshDlBtn(); });
    deselAllR.addEventListener('click', () => { rogueCbs.forEach(cb => { cb.checked = false; }); refreshDlBtn(); });
  }

  // Download / Save action row
  const dlRow = document.createElement('div');
  dlRow.className = 'dl-action-row';
  const hint = document.createElement('span');
  hint.className = 'dl-selection-hint';
  const dlBtn = document.createElement('button');
  dlBtn.className = 'btn-primary';
  dlBtn.textContent = 'Download / Save';
  dlBtn.disabled = true;

  function getActions() {
    const toDl = quantCbs
      .filter(cb => cb.checked && !isPresentFile(cb.dataset.filename, presentFiles))
      .map(cb => ({ filename: cb.dataset.filename, size: +cb.dataset.size }));
    const toDlSidecars = sidecarCbs
      .filter(cb => cb.checked && !isPresentFile(sidecars[+cb.dataset.idx].filename, presentFiles))
      .map(cb => sidecars[+cb.dataset.idx]);
    const toDel = [
      ...quantCbs
        .filter(cb => !cb.checked && isPresentFile(cb.dataset.filename, presentFiles))
        .map(cb => cb.dataset.filename),
      ...sidecarCbs
        .filter(cb => !cb.checked && isPresentFile(sidecars[+cb.dataset.idx].filename, presentFiles))
        .map(cb => sidecars[+cb.dataset.idx].filename),
      ...rogueCbs
        .filter(cb => !cb.checked)
        .map(cb => cb.dataset.localPath),
    ];
    return { toDl, toDlSidecars, toDel };
  }

  function refreshDlBtn() {
    const { toDl, toDlSidecars, toDel } = getActions();
    const hasChanges = toDl.length > 0 || toDlSidecars.length > 0 || toDel.length > 0;
    dlBtn.disabled = downloadInProgress || !hasChanges;
    const parts = [];
    if (toDl.length + toDlSidecars.length > 0)
      parts.push((toDl.length + toDlSidecars.length) + ' to download');
    if (toDel.length > 0)
      parts.push(toDel.length + ' to delete');
    hint.textContent = parts.length ? parts.join(', ') : 'No changes';
  }

  _refreshDlBtn = refreshDlBtn;

  dlBtn.addEventListener('click', async () => {
    const { toDl, toDlSidecars, toDel } = getActions();

    if (toDel.length > 0) {
      try {
        const resp = await fetch('/api/local/delete-files', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ repoId, files: toDel }),
        });
        if (!resp.ok) throw new Error(await resp.text());
        toDel.forEach(f => presentFiles.delete(f));
        setStatusBar('Ready', `Deleted ${toDel.length} file(s)`, false);
        fetchLocalModels();
      } catch (e) {
        setStatusBar('Error', 'Delete failed: ' + e.message, false);
        return;
      }
    }

    if (toDl.length > 0 || toDlSidecars.length > 0) {
      const filenames = toDl.length > 0
        ? toDl.map(f => f.filename)
        : [toDlSidecars[0].filename];
      const sFiles = toDl.length > 0
        ? toDlSidecars.map(s => s.filename)
        : toDlSidecars.slice(1).map(s => s.filename);
      const totalSize = toDl.reduce((s, f) => s + f.size, 0)
        + toDlSidecars.reduce((s, f) => s + (f.size || 0), 0);
      startDownload(repoId, filenames, sFiles, totalSize);
    } else {
      refreshDlBtn();
    }
  });

  dlRow.appendChild(hint);
  dlRow.appendChild(dlBtn);
  results.appendChild(dlRow);

  refreshDlBtn();
}

// ── Download ───────────────────────────────────────────────────────────────

async function startDownload(repoId, filenames, sidecarFiles, totalSize) {
  if (downloadInProgress) return;
  if (!Array.isArray(filenames)) filenames = [filenames];
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
  };
}

function setDownloadState(inProgress) {
  downloadInProgress = inProgress;
  document.getElementById('cancel-dl-btn').style.display = inProgress ? 'inline-block' : 'none';
  if (_refreshDlBtn) _refreshDlBtn();
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
  if (!confirm(`Restart ${llamaServiceLabel} service?\n\nThe server will be briefly unavailable.`)) return;
  const btn = document.getElementById('restart-btn');
  btn.disabled = true;
  setStatusBar('Restart', `Restarting ${llamaServiceLabel}…`, true);
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
  } catch (_) {}
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

async function openRawEditModal({ title, subtitle, endpoint, placeholder, successMsg }) {
  let body = '';
  try {
    const resp = await fetch(endpoint);
    if (resp.ok) body = await resp.text();
  } catch (_) {}

  const backdrop = document.createElement('div');
  backdrop.className = 'modal-backdrop';
  backdrop.innerHTML = `
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="modal-title">
      <div class="modal-title" id="modal-title">
        ${esc(title)}${subtitle ? ' <small>' + esc(subtitle) + '</small>' : ''}
      </div>
      <textarea spellcheck="false" placeholder="${esc(placeholder)}">${esc(body)}</textarea>
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
      const resp = await fetch(endpoint, {
        method: 'PUT',
        headers: { 'Content-Type': 'text/plain' },
        body: ta.value,
      });
      if (!resp.ok) throw new Error(await resp.text());
      closeModal();
      setStatusBar('Ready', successMsg, false);
      fetchLocalModels();
    } catch (e) {
      setStatusBar('Error', 'Save failed: ' + e.message, false);
      saveBtn.disabled = false;
      saveBtn.textContent = 'Save';
    }
  });

  backdrop.addEventListener('click', (e) => { if (e.target === backdrop) closeModal(); });
  document.addEventListener('keydown', onKey);
  document.body.appendChild(backdrop);
  ta.focus();
  ta.setSelectionRange(0, 0);
}

async function openTemplatesModal() {
  document.getElementById('status-menu').classList.remove('open');
  let tpl = { llmCmd: '', llmTtl: -1, sdCmd: '', sdTtl: 600, sdCheckEndpoint: '${sd-check-endpoint}' };
  try {
    const resp = await fetch('/api/llamaswap/templates');
    if (resp.ok) tpl = await resp.json();
  } catch (_) {}

  const backdrop = document.createElement('div');
  backdrop.className = 'modal-backdrop';
  backdrop.innerHTML = `
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="modal-title">
      <div class="modal-title" id="modal-title">Edit Command Templates
        <small>Applied when new models are added to config.yaml</small>
      </div>
      <div class="modal-body">
        <div class="tpl-section">
          <div class="tpl-section-title">LLM models</div>
          <textarea class="tpl-textarea" id="tpl-llm-cmd" spellcheck="false">${esc(tpl.llmCmd)}</textarea>
          <div class="tpl-ph-hint">Placeholders: <code>{{MODEL_PATH}}</code> &nbsp;<code>{{MODEL_NAME}}</code> &nbsp;<code>{{MMPROJ_LINE}}</code> (omitted if no mmproj) &nbsp;<code>${'${PORT}'}</code> (llama-swap runtime)</div>
          <div class="tpl-ttl-row">
            <label for="tpl-llm-ttl">Default TTL (s):</label>
            <input type="number" id="tpl-llm-ttl" value="${tpl.llmTtl}" min="-1">
            <span class="tpl-ttl-hint">−1 = auto (600 s for &lt;10 B params, 0 otherwise)</span>
          </div>
        </div>
        <div class="tpl-section">
          <div class="tpl-section-title">SD / Flux models</div>
          <textarea class="tpl-textarea" id="tpl-sd-cmd" spellcheck="false">${esc(tpl.sdCmd)}</textarea>
          <div class="tpl-ph-hint">Placeholders: <code>{{MODEL_PATH}}</code> &nbsp;<code>{{VAE_LINE}}</code> (omitted if no VAE) &nbsp;<code>${'${PORT}'}</code> (llama-swap runtime)</div>
          <div class="tpl-ttl-row">
            <label for="tpl-sd-ttl">Default TTL (s):</label>
            <input type="number" id="tpl-sd-ttl" value="${tpl.sdTtl}" min="-1">
          </div>
          <div class="tpl-ttl-row">
            <label for="tpl-sd-check">checkEndpoint:</label>
            <input type="text" id="tpl-sd-check" value="${esc(tpl.sdCheckEndpoint)}" style="flex:1;padding:4px 8px;background:#0f172a;border:1px solid #334155;border-radius:5px;color:#f1f5f9;font-size:0.825rem;outline:none;font-family:inherit" spellcheck="false">
            <span class="tpl-ttl-hint">macro or literal path (e.g. <code style="font-size:0.7rem">${'${sd-check-endpoint}'}</code>)</span>
          </div>
        </div>
      </div>
      <div class="modal-actions">
        <button class="btn-secondary" id="modal-cancel">Cancel</button>
        <button class="btn-primary" id="modal-save">Save</button>
      </div>
    </div>
  `;

  function closeModal() {
    document.body.removeChild(backdrop);
    document.removeEventListener('keydown', onKey);
  }
  function onKey(e) { if (e.key === 'Escape') closeModal(); }

  backdrop.querySelector('#modal-cancel').addEventListener('click', closeModal);
  backdrop.addEventListener('click', (e) => { if (e.target === backdrop) closeModal(); });
  document.addEventListener('keydown', onKey);

  backdrop.querySelector('#modal-save').addEventListener('click', async () => {
    const saveBtn = backdrop.querySelector('#modal-save');
    saveBtn.disabled = true;
    saveBtn.textContent = 'Saving…';
    const payload = {
      llmCmd:          backdrop.querySelector('#tpl-llm-cmd').value,
      llmTtl:          parseInt(backdrop.querySelector('#tpl-llm-ttl').value, 10) || 0,
      sdCmd:           backdrop.querySelector('#tpl-sd-cmd').value,
      sdTtl:           parseInt(backdrop.querySelector('#tpl-sd-ttl').value, 10) || 0,
      sdCheckEndpoint: backdrop.querySelector('#tpl-sd-check').value,
    };
    try {
      const resp = await fetch('/api/llamaswap/templates', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      if (!resp.ok) throw new Error(await resp.text());
      closeModal();
      setStatusBar('Ready', 'Command templates saved', false);
    } catch (e) {
      setStatusBar('Error', 'Save failed: ' + e.message, false);
      saveBtn.disabled = false;
      saveBtn.textContent = 'Save';
    }
  });

  document.body.appendChild(backdrop);
  backdrop.querySelector('#tpl-llm-cmd').focus();
  backdrop.querySelector('#tpl-llm-cmd').setSelectionRange(0, 0);
}

async function openFullConfigModal() {
  document.getElementById('status-menu').classList.remove('open');
  const isSwap = llamaSwapEnabled;
  const endpoint = isSwap ? '/api/llamaswap/config' : '/api/preset/config';
  const filename = isSwap ? 'config.yaml' : 'models.ini';
  await openRawEditModal({
    title: 'Edit ' + filename, subtitle: null,
    endpoint, placeholder: '',
    successMsg: filename + ' saved',
  });
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
    if (s.llamaServiceLabel) {
      llamaServiceLabel = s.llamaServiceLabel;
      document.getElementById('restart-btn').textContent = 'Restart ' + llamaServiceLabel;
    }
    if (s.llamaSwapEnabled != null) {
      llamaSwapEnabled = s.llamaSwapEnabled;
      document.getElementById('edit-templates-btn').style.display = llamaSwapEnabled ? '' : 'none';
    }
    el.textContent = llamaServiceLabel + ': ' + (s.llamaReachable ? 'online' : 'offline');
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
    // Update loaded aliases on model cards without re-rendering
    if (s.loadedModels) {
      const loadedSet = new Set(s.loadedModels);
      document.querySelectorAll('.model-card[data-aliases]').forEach(card => {
        const aliases = card.dataset.aliases ? card.dataset.aliases.split(',').filter(Boolean) : [];
        const loadedRow = card.querySelector('.model-loaded-row');
        if (!loadedRow) return;
        const active = aliases.filter(a => loadedSet.has(a));
        loadedRow.innerHTML = active.length > 0
          ? active.map(a => `<span class="badge badge-loaded">${esc(a)}</span>`).join(' ')
          : '<span class="badge badge-unloaded">Unloaded</span>';
      });
    }
    if (s.downloadInProgress && !activeEventSource) {
      setDownloadState(true);
      openSSE();
    } else if (!s.downloadInProgress && downloadInProgress && !activeEventSource) {
      setDownloadState(false);
    }
  } catch (_) {}
}

// ── Init ───────────────────────────────────────────────────────────────────

document.getElementById('refresh-btn').addEventListener('click', () => { fetchLocalModels(); pollStatus(); });
document.getElementById('browse-btn').addEventListener('click', browseRepo);
document.getElementById('restart-btn').addEventListener('click', restartService);
document.getElementById('edit-config-btn').addEventListener('click', openFullConfigModal);
document.getElementById('edit-templates-btn').addEventListener('click', openTemplatesModal);
document.getElementById('restart-self-btn').addEventListener('click', restartSelf);
document.getElementById('cancel-dl-btn').addEventListener('click', cancelDownload);
document.getElementById('status-menu').addEventListener('click', (e) => e.stopPropagation());
document.getElementById('repo-input').addEventListener('keydown', (e) => {
  if (e.key === 'Enter') browseRepo();
});

fetchLocalModels();
pollStatus();
setInterval(pollStatus, 5000);
