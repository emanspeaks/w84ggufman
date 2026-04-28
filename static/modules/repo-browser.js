// Repo input and browsing logic

import { clearErr, showErr, esc, formatBytes, copyTextToClipboard, joinPath } from './utils.js';
import { setStatusBar } from './status-bar.js';
import { renderSidecarTree, isPresentFile, commonPrefix, quantDisplayName, quantBitDepth } from './quant-grid.js';
import { setRefreshDlBtnExport, startDownload } from './download.js';
import { diskFreeBytes, ramTotalBytes, llamaSwapEnabled, modelsDir } from './status-polling.js';
import { openFullConfigModal, injectModelEntry } from './config-modal.js';
import { fetchLocalModels } from './local-models.js';

export let currentRepoContext = null;

function findMmprojSidecar(sidecars) {
  for (const s of sidecars) {
    const base = String(s.filename || '').split('/').pop().toLowerCase();
    if (base.startsWith('mmproj-')) return s.filename;
  }
  return '';
}

function findVaeSidecar(sidecars) {
  for (const s of sidecars) {
    const base = String(s.filename || '').split('/').pop().toLowerCase();
    if (base === 'ae.safetensors' || base.includes('vae')) return s.filename;
  }
  return '';
}

function absoluteModelPath(filename) {
  const rel = String(filename || '');
  if (!rel) return '';
  if (currentRepoContext && currentRepoContext.kind === 'local' && currentRepoContext.path) {
    return joinPath(currentRepoContext.path, rel);
  }
  if (currentRepoContext && currentRepoContext.kind === 'repo' && modelsDir && currentRepoContext.repoId) {
    return joinPath(joinPath(modelsDir, currentRepoContext.repoId), rel);
  }
  return '';
}

async function copyAbsolutePath(filename) {
  const absPath = absoluteModelPath(filename);
  if (!absPath) {
    setStatusBar('Error', 'Could not resolve absolute path for ' + filename, false);
    return;
  }
  const ok = await copyTextToClipboard(absPath);
  if (ok) {
    setStatusBar('Ready', 'Copied path: ' + absPath, false);
  } else {
    setStatusBar('Error', 'Copy failed — clipboard access denied', false);
  }
}

async function addLlamaSwapModelPreset(modelType, filename, sidecars) {
  if (!llamaSwapEnabled) {
    setStatusBar('Error', 'llama-swap is not enabled', false);
    return;
  }
  if (!currentRepoContext) return;
  const repoId = currentRepoContext.kind === 'local'
    ? currentRepoContext.path
    : currentRepoContext.repoId;
  const mmprojFile = modelType === 'llm' ? findMmprojSidecar(sidecars) : '';
  const vaeFile = modelType === 'sd' ? findVaeSidecar(sidecars) : '';
  try {
    const resp = await fetch('/api/llamaswap/model', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ repoId, filename, mmprojFile, vaeFile, modelType }),
    });
    if (!resp.ok) throw new Error(await resp.text());
    const data = await resp.json();
    const name = data.name;
    const entryBlock = data.entryBlock;
    const resolvedType = data.modelType || modelType;
    setStatusBar('Ready', 'Added ' + name + ' to config.yaml', false);
    fetchLocalModels();
    const injected = name && entryBlock
      ? injectModelEntry('/api/llamaswap/config', entryBlock, resolvedType, name)
      : false;
    if (!injected) {
      openFullConfigModal(true, name).catch(e => {
        console.error(e);
        setStatusBar('Error', 'Failed to open config editor: ' + e.message, false);
      });
    }
  } catch (e) {
    setStatusBar('Error', 'Failed to add model: ' + e.message, false);
  }
}

function showQuantContextMenu(event, filename, sidecars) {
  event.preventDefault();
  event.stopPropagation();
  document.querySelectorAll('.quant-ctx-menu').forEach(m => m.remove());
  const menu = document.createElement('div');
  menu.className = 'quant-ctx-menu';
  menu.style.cssText = 'position:fixed;z-index:1000;background:#1e293b;border:1px solid #334155;border-radius:6px;box-shadow:0 4px 12px rgba(0,0,0,0.4);padding:4px;min-width:200px;';

  const mkItem = (label, onClick) => {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.textContent = label;
    btn.style.cssText = 'display:block;width:100%;text-align:left;background:transparent;border:none;color:#f1f5f9;padding:6px 12px;font-size:0.85rem;cursor:pointer;border-radius:4px;font-family:inherit;';
    btn.addEventListener('mouseenter', () => { btn.style.background = '#334155'; });
    btn.addEventListener('mouseleave', () => { btn.style.background = 'transparent'; });
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      menu.remove();
      onClick();
    });
    return btn;
  };

  menu.appendChild(mkItem('Copy path to file', () => {
    copyAbsolutePath(filename).catch(console.error);
  }));

  if (llamaSwapEnabled) {
    menu.appendChild(mkItem('Add LLM Model Preset…', () => addLlamaSwapModelPreset('llm', filename, sidecars)));
    menu.appendChild(mkItem('Add SD Model Preset…', () => addLlamaSwapModelPreset('sd', filename, sidecars)));
  }

  document.body.appendChild(menu);

  const rect = menu.getBoundingClientRect();
  const maxX = window.innerWidth - rect.width - 4;
  const maxY = window.innerHeight - rect.height - 4;
  menu.style.left = Math.min(event.clientX, maxX) + 'px';
  menu.style.top = Math.min(event.clientY, maxY) + 'px';

  const closer = (e) => {
    if (!menu.contains(e.target)) {
      menu.remove();
      document.removeEventListener('mousedown', closer);
      document.removeEventListener('keydown', keyCloser);
    }
  };
  const keyCloser = (e) => {
    if (e.key === 'Escape') {
      menu.remove();
      document.removeEventListener('mousedown', closer);
      document.removeEventListener('keydown', keyCloser);
    }
  };
  setTimeout(() => {
    document.addEventListener('mousedown', closer);
    document.addEventListener('keydown', keyCloser);
  }, 0);
}

function showSidecarContextMenu(event, filename) {
  event.preventDefault();
  event.stopPropagation();
  document.querySelectorAll('.quant-ctx-menu').forEach(m => m.remove());
  const menu = document.createElement('div');
  menu.className = 'quant-ctx-menu';
  menu.style.cssText = 'position:fixed;z-index:1000;background:#1e293b;border:1px solid #334155;border-radius:6px;box-shadow:0 4px 12px rgba(0,0,0,0.4);padding:4px;min-width:200px;';

  const mkItem = (label, onClick) => {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.textContent = label;
    btn.style.cssText = 'display:block;width:100%;text-align:left;background:transparent;border:none;color:#f1f5f9;padding:6px 12px;font-size:0.85rem;cursor:pointer;border-radius:4px;font-family:inherit;';
    btn.addEventListener('mouseenter', () => { btn.style.background = '#334155'; });
    btn.addEventListener('mouseleave', () => { btn.style.background = 'transparent'; });
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      menu.remove();
      onClick();
    });
    return btn;
  };

  menu.appendChild(mkItem('Copy path to file', () => {
    copyAbsolutePath(filename).catch(console.error);
  }));

  document.body.appendChild(menu);

  const rect = menu.getBoundingClientRect();
  const maxX = window.innerWidth - rect.width - 4;
  const maxY = window.innerHeight - rect.height - 4;
  menu.style.left = Math.min(event.clientX, maxX) + 'px';
  menu.style.top = Math.min(event.clientY, maxY) + 'px';

  const closer = (e) => {
    if (!menu.contains(e.target)) {
      menu.remove();
      document.removeEventListener('mousedown', closer);
      document.removeEventListener('keydown', keyCloser);
    }
  };
  const keyCloser = (e) => {
    if (e.key === 'Escape') {
      menu.remove();
      document.removeEventListener('mousedown', closer);
      document.removeEventListener('keydown', keyCloser);
    }
  };
  setTimeout(() => {
    document.addEventListener('mousedown', closer);
    document.addEventListener('keydown', keyCloser);
  }, 0);
}

export function setRepoInput(value) {
  const input = document.getElementById('repo-input');
  input.value = value;
  updateHFLink(value);
}

export function updateHFLink(repoId) {
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

export function setupRepoBrowser() {
  document.getElementById('repo-input').addEventListener('input', () => {
    updateHFLink(document.getElementById('repo-input').value.trim());
  });

  document.getElementById('browse-btn').addEventListener('click', browseRepo);
  document.getElementById('repo-input').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') browseRepo();
  });
}

export async function browseRepo() {
  const repoId = document.getElementById('repo-input').value.trim();
  if (!repoId) return;
  currentRepoContext = { kind: 'repo', repoId };
  clearErr('repo-error');
  const results = document.getElementById('repo-results');
  results.innerHTML = '<p class="msg-loading">Fetching file list…</p>';
  import('./readme.js').then(mod => mod.fetchAndRenderReadme(repoId));
  try {
    const repoResp = await fetch('/api/repo?id=' + encodeURIComponent(repoId));
    if (!repoResp.ok) throw new Error(await repoResp.text());
    const info = await repoResp.json();
    renderRepoInfo(repoId, info);
  } catch (e) {
    results.innerHTML = '';
    showErr('repo-error', 'Failed to fetch repo: ' + e.message);
  }
}

export async function browseLocalPath(path, label) {
  currentRepoContext = { kind: 'local', path, label: label || path };
  clearErr('repo-error');
  setRepoInput(label || path);
  const results = document.getElementById('repo-results');
  results.innerHTML = '<p class="msg-loading">Loading local files…</p>';
  try {
    const filesResp = await fetch('/api/local-files?id=' + encodeURIComponent(path));
    if (!filesResp.ok) throw new Error(await filesResp.text());
    const info = await filesResp.json();
    renderRepoInfo(path, info);
  } catch (e) {
    results.innerHTML = '';
    showErr('repo-error', 'Failed to load: ' + e.message);
  }
}

async function refreshCurrentRepoView() {
  if (!currentRepoContext) return;
  if (currentRepoContext.kind === 'local') {
    await browseLocalPath(currentRepoContext.path, currentRepoContext.label);
    return;
  }
  setRepoInput(currentRepoContext.repoId);
  await browseRepo();
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

  if (info.hasUpdate && info.repoId) {
    const updateBar = document.createElement('div');
    updateBar.style.cssText = 'display:flex;align-items:center;gap:0.75rem;padding:6px 0 10px;';
    const updateBtn = document.createElement('button');
    updateBtn.className = 'update-repo-btn';
    updateBtn.textContent = 'Update this repo';
    updateBtn.addEventListener('click', async () => {
      updateBtn.disabled = true;
      updateBtn.textContent = 'Queuing…';
      try {
        const resp = await fetch('/api/updates/apply', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ repoId: info.repoId }),
        });
        if (!resp.ok) throw new Error(await resp.text());
        setStatusBar('Ready', 'Update queued for ' + info.repoId, false);
        import('./local-models.js').then(m => m.fetchLocalModels());
      } catch (e) {
        setStatusBar('Error', 'Update failed: ' + e.message, false);
        updateBtn.disabled = false;
        updateBtn.textContent = 'Update this repo';
      }
    });
    const label = document.createElement('span');
    label.className = 'update-notice';
    label.textContent = 'A newer version of this repo is available on HuggingFace.';
    updateBar.appendChild(updateBtn);
    updateBar.appendChild(label);
    results.appendChild(updateBar);
  }

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

  // Quant files section
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
        const tooBig  = diskFreeBytes > 0 && f.size != null && f.size > diskFreeBytes;
        const ramWarn = ramTotalBytes > 0 && f.size != null && f.size > ramTotalBytes;

        const tile = document.createElement('label');
        tile.className = 'quant-tile' +
          (tooBig   ? ' toobig'  : '') +
          (ramWarn  ? ' ramwarn' : '') +
          (isPresent ? ' present' : '');
        tile.title = f.filename;
        tile.addEventListener('contextmenu', (e) => showQuantContextMenu(e, f.filename, sidecars));

        const cb = document.createElement('input');
        cb.type = 'checkbox';
        cb.className = 'quant-cb';
        cb.dataset.filename = f.filename;
        cb.dataset.size = f.size || 0;
        cb.checked = isPresent;
        cb.addEventListener('change', () => refreshDlBtn());
        quantCbs.push(cb);

        tile.appendChild(cb);
        const nameSpan = document.createElement('span');
        nameSpan.className = 'quant-tile-name';
        nameSpan.textContent = label;
        const sizeSpan = document.createElement('span');
        sizeSpan.className = 'quant-tile-size';
        sizeSpan.textContent = (ramWarn ? '⚠ ' : '') + formatBytes(f.size) + (isPresent ? ' ✓' : '');
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

  // Sidecars section
  let sidecarTree = null;
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
    sidecarTree = renderSidecarTree(
      wrap,
      sidecars,
      presentFiles,
      sidecarCbs,
      () => refreshDlBtn(),
      (event, filename) => showSidecarContextMenu(event, filename),
    );
    companionEl.appendChild(wrap);

    selAllS.addEventListener('click',   () => {
      sidecarCbs.forEach(cb => { cb.checked = true; });
      sidecarTree.updateDirStates();
      refreshDlBtn();
    });
    deselAllS.addEventListener('click', () => {
      sidecarCbs.forEach(cb => { cb.checked = false; });
      sidecarTree.updateDirStates();
      refreshDlBtn();
    });

    results.appendChild(companionEl);
  }

  // Rogue files section
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
      cb.addEventListener('change', () => refreshDlBtn());
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

  // Download action row
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
    dlBtn.disabled = !hasChanges;
    const parts = [];
    if (toDl.length + toDlSidecars.length > 0)
      parts.push((toDl.length + toDlSidecars.length) + ' to download');
    if (toDel.length > 0)
      parts.push(toDel.length + ' to delete');
    hint.textContent = parts.length ? parts.join(', ') : 'No changes';
  }
  setRefreshDlBtnExport(refreshDlBtn);

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
        import('./local-models.js').then(mod => mod.fetchLocalModels());
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

export { refreshCurrentRepoView };
