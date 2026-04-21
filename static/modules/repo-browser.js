// Repo input and browsing logic

import { clearErr, showErr, esc, formatBytes } from './utils.js';
import { setStatusBar } from './status-bar.js';
import { renderSidecarTree, buildSidecarTree, isPresentFile } from './quant-grid.js';

export let currentRepoContext = null;

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
    const resp = await fetch('/api/repo?id=' + encodeURIComponent(repoId));
    if (!resp.ok) throw new Error(await resp.text());
    const info = await resp.json();
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
    const resp = await fetch('/api/local-files?id=' + encodeURIComponent(path));
    if (!resp.ok) throw new Error(await resp.text());
    const info = await resp.json();
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

  // Quant files section (reuse download module's refreshDlBtn)
  if (files.length) {
    import('./download.js').then(dlMod => {
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
      const prefix = import('./quant-grid.js').then(qMod => qMod.commonPrefix(bases.filter(Boolean)).replace(/[-_]+$/, '')).then(p => {
        for (const f of files) {
          const label = f.displayName || (import('./quant-grid.js').then(qMod => qMod.quantDisplayName(f.filename, p)));
          const bits  = import('./quant-grid.js').then(qMod => qMod.quantBitDepth(label));
          // ... rest of quant rendering
        }
      });

      selAllQ.addEventListener('click',   () => { quantCbs.forEach(cb => { cb.checked = true;  }); dlMod.refreshDlBtn(); });
      deselAllQ.addEventListener('click', () => { quantCbs.forEach(cb => { cb.checked = false; }); dlMod.refreshDlBtn(); });
    });
  }

  // Sidecars section
  if (sidecars.length > 0) {
    import('./download.js').then(dlMod => {
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
      const sidecarTree = renderSidecarTree(wrap, sidecars, presentFiles, sidecarCbs, dlMod.refreshDlBtn);
      companionEl.appendChild(wrap);

      selAllS.addEventListener('click',   () => {
        sidecarCbs.forEach(cb => { cb.checked = true; });
        sidecarTree.updateDirStates();
        dlMod.refreshDlBtn();
      });
      deselAllS.addEventListener('click', () => {
        sidecarCbs.forEach(cb => { cb.checked = false; });
        sidecarTree.updateDirStates();
        dlMod.refreshDlBtn();
      });

      results.appendChild(companionEl);
    });
  }

  // Rogue files section
  if (rogueFiles.length > 0) {
    import('./download.js').then(dlMod => {
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
        cb.addEventListener('change', dlMod.refreshDlBtn);
        rogueCbs.push(cb);

        row.appendChild(cb);
        const nameSpan = document.createElement('span');
        nameSpan.className = info.localOnly ? 'sidecar-name' : 'sidecar-name rogue-name';
        nameSpan.textContent = localPath;
        row.appendChild(nameSpan);
        rogueWrap.appendChild(row);
      });
      results.appendChild(rogueWrap);

      selAllR.addEventListener('click',   () => { rogueCbs.forEach(cb => { cb.checked = true;  }); dlMod.refreshDlBtn(); });
      deselAllR.addEventListener('click', () => { rogueCbs.forEach(cb => { cb.checked = false; }); dlMod.refreshDlBtn(); });
    });
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
    dlBtn.disabled = false || !hasChanges; // TODO: hook downloadInProgress
    const parts = [];
    if (toDl.length + toDlSidecars.length > 0)
      parts.push((toDl.length + toDlSidecars.length) + ' to download');
    if (toDel.length > 0)
      parts.push(toDel.length + ' to delete');
    hint.textContent = parts.length ? parts.join(', ') : 'No changes';
  }

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
      import('./download.js').then(mod => mod.startDownload(repoId, filenames, sFiles, totalSize));
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
