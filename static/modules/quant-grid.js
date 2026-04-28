// Quant grid helpers and sidecar tree rendering

import { formatBytes, esc } from './utils.js';

export function commonPrefix(arr) {
  if (!arr.length) return '';
  return arr.slice(1).reduce((p, s) => {
    while (!s.startsWith(p)) p = p.slice(0, -1);
    return p;
  }, arr[0]);
}

export function quantDisplayName(filename, prefix) {
  const base = filename.replace(/\.gguf$/i, '');
  const tail = base.startsWith(prefix) ? base.slice(prefix.length).replace(/^[-_]/, '') : base;
  return tail || base;
}

export function quantBitDepth(displayName) {
  const m = displayName.replace(/^UD-/i, '').match(/(?:^|[-_.])(?:IQ|TQ|BF|MXFP|NVFP|[QF])(\d+)/i);
  return m ? parseInt(m[1]) : 999;
}

export function buildSidecarTree(sidecars) {
  const root = { dirs: new Map(), files: [] };
  sidecars.forEach((s, idx) => {
    const parts = String(s.filename || '').split('/').filter(Boolean);
    if (!parts.length) return;
    let node = root;
    for (let i = 0; i < parts.length - 1; i++) {
      const part = parts[i];
      if (!node.dirs.has(part)) node.dirs.set(part, { dirs: new Map(), files: [] });
      node = node.dirs.get(part);
    }
    node.files.push({ idx, name: parts[parts.length - 1], entry: s });
  });
  return root;
}

export function renderSidecarTree(wrap, sidecars, presentFiles, sidecarCbs, refreshDlBtn, onFileContextMenu) {
  const tree = buildSidecarTree(sidecars);
  const dirToggles = [];

  function renderNode(node, prefix) {
    const frag = document.createDocumentFragment();
    const childDirs = [...node.dirs.entries()].sort((a, b) => a[0].localeCompare(b[0]));
    const childFiles = [...node.files].sort((a, b) => a.entry.filename.localeCompare(b.entry.filename));
    const totalChildren = childDirs.length + childFiles.length;
    let childIndex = 0;
    const descendantFileCbs = [];

    for (const [dirName, dirNode] of childDirs) {
      const isLast = childIndex === totalChildren - 1;
      const branch = prefix + (isLast ? '└─ ' : '├─ ');

      const row = document.createElement('div');
      row.className = 'sidecar-row sidecar-tree-row sidecar-dir-row';

      const prefixSpan = document.createElement('span');
      prefixSpan.className = 'sidecar-tree-prefix';
      prefixSpan.textContent = branch;
      row.appendChild(prefixSpan);

      const dirCb = document.createElement('input');
      dirCb.type = 'checkbox';
      dirCb.className = 'sidecar-dir-cb';
      row.appendChild(dirCb);

      const dirSpan = document.createElement('span');
      dirSpan.className = 'sidecar-name sidecar-dir-name';
      dirSpan.textContent = dirName + '/';
      row.appendChild(dirSpan);

      const nextPrefix = prefix + (isLast ? '   ' : '│  ');
      const renderedChild = renderNode(dirNode, nextPrefix);
      const childFileCbs = renderedChild.fileCbs;
      descendantFileCbs.push(...childFileCbs);

      dirCb.addEventListener('change', () => {
        childFileCbs.forEach(cb => { cb.checked = dirCb.checked; });
        updateDirStates();
        refreshDlBtn();
      });

      dirToggles.push({ cb: dirCb, fileCbs: childFileCbs });
      frag.appendChild(row);
      frag.appendChild(renderedChild.fragment);
      childIndex++;
    }

    for (const f of childFiles) {
      const isLast = childIndex === totalChildren - 1;
      const branch = prefix + (isLast ? '└─ ' : '├─ ');
      const isPresent = isPresentFile(f.entry.filename, presentFiles);

      const row = document.createElement('div');
      row.className = 'sidecar-row sidecar-tree-row sidecar-file-row';

      const prefixSpan = document.createElement('span');
      prefixSpan.className = 'sidecar-tree-prefix';
      prefixSpan.textContent = branch;
      row.appendChild(prefixSpan);

      const cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.className = 'sidecar-cb';
      cb.dataset.idx = f.idx;
      cb.checked = isPresent;
      sidecarCbs.push(cb);
      descendantFileCbs.push(cb);
      row.appendChild(cb);

      const nameSpan = document.createElement('span');
      nameSpan.className = 'sidecar-name';
      nameSpan.textContent = f.name;
      row.appendChild(nameSpan);

      if (typeof onFileContextMenu === 'function') {
        row.addEventListener('contextmenu', (e) => onFileContextMenu(e, f.entry.filename));
      }

      if (f.entry.size != null) {
        const sizeSpan = document.createElement('span');
        sizeSpan.className = 'sidecar-size';
        sizeSpan.textContent = formatBytes(f.entry.size) + (isPresent ? ' ✓' : '');
        row.appendChild(sizeSpan);
      }

      frag.appendChild(row);
      childIndex++;
    }

    return { fragment: frag, fileCbs: descendantFileCbs };
  }

  function updateDirStates() {
    dirToggles.forEach(({ cb, fileCbs }) => {
      if (!fileCbs.length) {
        cb.checked = false;
        cb.indeterminate = false;
        return;
      }
      let checked = 0;
      fileCbs.forEach(fcb => { if (fcb.checked) checked++; });
      cb.checked = checked === fileCbs.length;
      cb.indeterminate = checked > 0 && checked < fileCbs.length;
    });
  }

  const rendered = renderNode(tree, '');
  wrap.appendChild(rendered.fragment);

  sidecarCbs.forEach(cb => {
    cb.addEventListener('change', () => {
      updateDirStates();
      refreshDlBtn();
    });
  });

  updateDirStates();
  return { updateDirStates };
}

export function isPresentFile(filename, presentFiles) {
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
