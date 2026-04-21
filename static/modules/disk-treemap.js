// Non-modal disk-usage treemap dialog — 3-panel layout (models | system | free)

import { esc, formatBytes } from './utils.js';

const DIR_PAD = 2;  // px inset on each side within a dir box

let dialogEl = null;
let dialogCleanup = null;

// ── Public API ─────────────────────────────────────────────────────────────

export async function openDiskTreemap() {
  if (dialogEl) { dialogEl.style.zIndex = '401'; return; }

  let data;
  try {
    const resp = await fetch('/api/disk-usage');
    if (!resp.ok) throw new Error('fetch failed');
    data = await resp.json();
  } catch (e) { return; }

  dialogEl = document.createElement('div');
  dialogEl.className = 'disk-treemap-dialog';
  dialogEl.innerHTML = `
    <div class="dtm-header">
      <span class="dtm-title">Disk usage</span>
      <span class="dtm-summary"></span>
      <button class="dtm-close" title="Close">×</button>
    </div>
    <div class="dtm-body"></div>
    <div class="dtm-statusbar">
      <span class="dtm-hover-path"></span>
      <span class="dtm-controls">
        <label class="dtm-check"><input type="checkbox" id="dtm-show-system" checked> System</label>
        <label class="dtm-check"><input type="checkbox" id="dtm-show-free" checked> Free</label>
      </span>
      <span class="dtm-legend"></span>
    </div>
  `;

  dialogEl.querySelector('.dtm-summary').textContent =
    formatBytes(data.usedBytes) + ' used / ' + formatBytes(data.totalBytes) +
    ' (' + formatBytes(data.freeBytes) + ' free, ' + formatBytes(data.modelsDirBytes) + ' used in models dir)';

  dialogEl.querySelector('.dtm-close').addEventListener('click', closeDiskTreemap);

  const onDocClick = () => removeMenu();
  document.addEventListener('click', onDocClick);
  const detachDrag = setupDrag(dialogEl);
  document.body.appendChild(dialogEl);

  const body = dialogEl.querySelector('.dtm-body');
  const hoverPathEl = dialogEl.querySelector('.dtm-hover-path');
  const legendEl = dialogEl.querySelector('.dtm-legend');
  const state = {
    data,
    showSystem: true,
    showFree: true,
    dirElMap: new Map(),
    hoverPathEl,
  };

  function render() {
    removeMenu();
    body.innerHTML = '';
    state.dirElMap.clear();
    const W = body.clientWidth, H = body.clientHeight;
    if (W > 0 && H > 0) {
      const tree = buildTree(state.data);
      buildLegend(legendEl, tree);
      renderPanels(body, tree, state, W, H);
    }
  }

  state.render = render;
  render();
  const ro = new ResizeObserver(render);
  ro.observe(body);

  dialogEl.querySelector('#dtm-show-system').addEventListener('change', e => {
    state.showSystem = e.target.checked;
    render();
  });
  dialogEl.querySelector('#dtm-show-free').addEventListener('change', e => {
    state.showFree = e.target.checked;
    render();
  });

  dialogCleanup = () => {
    ro.disconnect();
    detachDrag();
    document.removeEventListener('click', onDocClick);
  };
}

export function closeDiskTreemap() {
  if (!dialogEl) return;
  if (dialogCleanup) { dialogCleanup(); dialogCleanup = null; }
  removeMenu();
  dialogEl.remove();
  dialogEl = null;
}

// ── Tree building ──────────────────────────────────────────────────────────

function buildTree(data) {
  const dirMap = new Map();
  const modelsRoot = {
    name: pathBasename(data.modelsDir), path: '', size: 0,
    kind: 'modelsRoot', children: [],
  };
  dirMap.set('', modelsRoot);

  function ensureDir(relPath) {
    if (dirMap.has(relPath)) return dirMap.get(relPath);
    const parts = relPath.split('/');
    const parentPath = parts.slice(0, -1).join('/');
    const parent = ensureDir(parentPath);
    const node = {
      name: parts[parts.length - 1], path: relPath, size: 0,
      kind: 'dir', children: [],
    };
    parent.children.push(node);
    dirMap.set(relPath, node);
    return node;
  }

  for (const f of data.files) {
    if (!f.size || f.size <= 0) continue;
    const parts = f.path.split('/');
    const dir = ensureDir(parts.slice(0, -1).join('/'));
    dir.children.push({
      name: parts[parts.length - 1], path: f.path, size: f.size,
      kind: 'file', children: [],
    });
  }

  function calcSize(node) {
    if (node.kind === 'file') return node.size;
    node.size = node.children.reduce((s, c) => s + calcSize(c), 0);
    return node.size;
  }
  calcSize(modelsRoot);

  function sortTree(node) {
    node.children.sort((a, b) => b.size - a.size);
    node.children.forEach(c => sortTree(c));
  }
  sortTree(modelsRoot);

  return modelsRoot;
}

function pathBasename(p) {
  if (!p) return 'models';
  const s = p.replace(/[/\\]+$/, '');
  return s.slice(Math.max(s.lastIndexOf('/'), s.lastIndexOf('\\')) + 1) || s;
}

// ── Panel layout ───────────────────────────────────────────────────────────

function renderPanels(container, tree, state, W, H) {
  const d = state.data;
  let visibleBytes = d.modelsDirBytes;
  if (state.showSystem && d.systemBytes > 0) visibleBytes += d.systemBytes;
  if (state.showFree   && d.freeBytes   > 0) visibleBytes += d.freeBytes;
  if (visibleBytes <= 0) return;

  let x = 0;

  // Models panel: squarified treemap
  if (d.modelsDirBytes > 0) {
    const modelsW = Math.round((d.modelsDirBytes / visibleBytes) * W);
    if (modelsW > 0) layoutChildren(container, tree.children, x, 0, modelsW, H, state);
    x += modelsW;
  }

  // System panel: solid block
  if (state.showSystem && d.systemBytes > 0) {
    const isLast = !(state.showFree && d.freeBytes > 0);
    const sysW = isLast ? W - x : Math.round((d.systemBytes / visibleBytes) * W);
    if (sysW > 0) renderSolidBlock(container, 'system', 'System files', d.systemBytes, x, 0, sysW, H, state);
    x += sysW;
  }

  // Free panel: solid block, always last to absorb rounding remainder
  if (state.showFree && d.freeBytes > 0 && x < W) {
    renderSolidBlock(container, 'free', 'Free space', d.freeBytes, x, 0, W - x, H, state);
  }
}

function renderSolidBlock(container, kind, name, size, x, y, w, h, state) {
  const el = makeDiv(x, y, w, h, 'dtm-block dtm-block-' + kind);
  if (w >= 40 && h >= 16) el.innerHTML =
    `<span class="dtm-label">${esc(name)}</span><span class="dtm-size">${esc(formatBytes(size))}</span>`;
  el.addEventListener('mouseenter', () => { state.hoverPathEl.textContent = name + '  —  ' + formatBytes(size); });
  el.addEventListener('mouseleave', () => { state.hoverPathEl.textContent = ''; });
  container.appendChild(el);
}

// ── Layout + render ────────────────────────────────────────────────────────

function layoutChildren(container, children, x, y, w, h, state) {
  const items = children.filter(c => c.size > 0);
  if (!items.length || w < 2 || h < 2) return;
  const total = items.reduce((s, c) => s + c.size, 0);
  if (total <= 0) return;
  binaryLayout(container, items, 0, items.length, total, x, y, x + w, y + h, state);
}

// Recursive binary-partition treemap — divides items into two size-balanced
// halves and cuts along the longer axis, producing normalized aspect ratios.
function binaryLayout(container, nodes, i, j, valueSum, x0, y0, x1, y1, state) {
  if (i >= j) return;
  const w = x1 - x0, h = y1 - y0;
  if (w < 2 || h < 2) return;

  if (j - i === 1) {
    renderNode(container, nodes[i], x0, y0, w, h, state);
    return;
  }

  // Find the split index k where the left group's total size is closest to half.
  const halfValue = valueSum / 2;
  let k = i + 1;
  let acc = nodes[i].size;
  while (k < j - 1 && acc + nodes[k].size <= halfValue) {
    acc += nodes[k].size;
    k++;
  }
  // Include one more item if it brings us closer to the target half.
  if (k < j - 1 && Math.abs(acc + nodes[k].size - halfValue) < Math.abs(acc - halfValue)) {
    acc += nodes[k].size;
    k++;
  }

  const leftValue = acc;
  const rightValue = valueSum - leftValue;

  if (w >= h) {
    const xk = x0 + w * leftValue / valueSum;
    binaryLayout(container, nodes, i, k, leftValue, x0, y0, xk, y1, state);
    binaryLayout(container, nodes, k, j, rightValue, xk, y0, x1, y1, state);
  } else {
    const yk = y0 + h * leftValue / valueSum;
    binaryLayout(container, nodes, i, k, leftValue, x0, y0, x1, yk, state);
    binaryLayout(container, nodes, k, j, rightValue, x0, yk, x1, y1, state);
  }
}

function renderNode(container, node, x, y, w, h, state) {
  if (w < 2 || h < 2) return;
  const { kind } = node;

  if (kind === 'file') {
    const el = makeDiv(x, y, w, h, 'dtm-block dtm-block-file');
    el.style.background = fileColor(node.name);
    if (w >= 54 && h >= 22) el.innerHTML =
      `<span class="dtm-label">${esc(node.name)}</span><span class="dtm-size">${esc(formatBytes(node.size))}</span>`;
    el.addEventListener('mouseenter', () => onFileEnter(node, state));
    el.addEventListener('mouseleave', () => onFileLeave(node, state));
    el.addEventListener('click',       e => { e.stopPropagation(); showMenu(e, node, state); });
    el.addEventListener('contextmenu', e => { e.preventDefault(); e.stopPropagation(); showMenu(e, node, state); });
    container.appendChild(el);
    return;
  }

  // dir (or modelsRoot, though that is never rendered directly): bordered box
  const el = document.createElement('div');
  el.className = 'dtm-dir';
  el.style.cssText = `position:absolute;left:${x}px;top:${y}px;width:${w}px;height:${h}px;box-sizing:border-box;`;
  el.dataset.path = node.path;
  state.dirElMap.set(node.path, el);
  el.addEventListener('mouseenter', () => { state.hoverPathEl.textContent = node.name + '  —  ' + formatBytes(node.size); });
  el.addEventListener('mouseleave', () => { state.hoverPathEl.textContent = ''; });
  container.appendChild(el);

  const innerX = x + DIR_PAD;
  const innerY = y + DIR_PAD;
  const innerW = w - DIR_PAD * 2;
  const innerH = h - DIR_PAD * 2;
  layoutChildren(container, node.children, innerX, innerY, innerW, innerH, state);
}

function makeDiv(x, y, w, h, cls) {
  const el = document.createElement('div');
  el.className = cls;
  el.style.cssText = `left:${x}px;top:${y}px;width:${w}px;height:${h}px;`;
  return el;
}

// ── Colors ─────────────────────────────────────────────────────────────────
// Hue derived from file extension so files of the same type share a color family.

function hueOf(name) {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return h % 360;
}

function extOf(name) {
  const i = name.lastIndexOf('.');
  return i > 0 ? name.slice(i + 1).toLowerCase() : '';
}

function extColor(ext) {
  if (!ext) return 'hsl(210,50%,38%)';
  return `hsl(${hueOf(ext)},55%,40%)`;
}

function fileColor(name) {
  return extColor(extOf(name));
}

// ── Hover + ancestor highlight ─────────────────────────────────────────────

function onFileEnter(node, state) {
  state.hoverPathEl.textContent = node.path + '  —  ' + formatBytes(node.size);
  const parts = node.path.split('/');
  parts.pop();
  let cur = '';
  for (const seg of parts) {
    cur = cur ? cur + '/' + seg : seg;
    state.dirElMap.get(cur)?.classList.add('dtm-dir--hover');
  }
}

function onFileLeave(node, state) {
  state.hoverPathEl.textContent = '';
  state.dirElMap.forEach(el => el.classList.remove('dtm-dir--hover'));
}

// ── Context menu ───────────────────────────────────────────────────────────

let menuEl = null;

function removeMenu() {
  if (menuEl) { menuEl.remove(); menuEl = null; }
}

function showMenu(e, node, state) {
  removeMenu();
  menuEl = document.createElement('div');
  menuEl.className = 'dtm-menu';
  const del = document.createElement('button');
  del.className = 'dtm-menu-item';
  del.textContent = 'Delete file…';
  del.addEventListener('click', async ev => {
    ev.stopPropagation();
    removeMenu();
    if (!window.confirm('Delete ' + node.name + '?\n\nThis cannot be undone.')) return;
    await deleteFile(node, state);
  });
  menuEl.appendChild(del);
  document.body.appendChild(menuEl);
  const mw = menuEl.offsetWidth, mh = menuEl.offsetHeight;
  let mx = e.clientX, my = e.clientY;
  if (mx + mw > window.innerWidth - 4)  mx = e.clientX - mw;
  if (my + mh > window.innerHeight - 4) my = e.clientY - mh;
  menuEl.style.left = mx + 'px';
  menuEl.style.top  = my + 'px';
}

async function deleteFile(node, state) {
  const parts = node.path.split('/');
  const filename = parts.pop();
  const repoId = parts.length > 0 ? parts.join('/') : '.';
  try {
    const resp = await fetch('/api/local/delete-files', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ repoId, files: [filename] }),
    });
    if (!resp.ok) throw new Error(await resp.text());
    const r2 = await fetch('/api/disk-usage');
    if (r2.ok) state.data = await r2.json();
    state.render();
  } catch (err) {
    console.error('delete failed:', err);
  }
}

// ── Legend ─────────────────────────────────────────────────────────────────

function buildLegend(el, tree) {
  el.innerHTML = '';

  const extMap = new Map();
  function walk(node) {
    if (node.kind === 'file') {
      const ext = extOf(node.name);
      extMap.set(ext, (extMap.get(ext) || 0) + node.size);
    } else {
      for (const c of node.children) walk(c);
    }
  }
  walk(tree);

  const exts = [...extMap.entries()].sort((a, b) => b[1] - a[1]).slice(0, 8);
  if (exts.length === 0) return;

  const label = document.createElement('span');
  label.className = 'dtm-legend-label';
  label.textContent = 'Color = ext:';
  el.appendChild(label);

  for (const [ext] of exts) {
    const item = document.createElement('span');
    item.className = 'dtm-legend-item';
    item.innerHTML = `<span class="dtm-legend-swatch" style="background:${extColor(ext)}"></span>${esc(ext || '(none)')}`;
    el.appendChild(item);
  }
}

// ── Drag ───────────────────────────────────────────────────────────────────

function setupDrag(dlg) {
  const header = dlg.querySelector('.dtm-header');
  let dragging = false, startX = 0, startY = 0, startL = 0, startT = 0;
  function onDown(e) {
    if (e.target.classList.contains('dtm-close')) return;
    dragging = true;
    const r = dlg.getBoundingClientRect();
    startX = e.clientX; startY = e.clientY; startL = r.left; startT = r.top;
    dlg.style.left = startL + 'px'; dlg.style.top = startT + 'px';
    dlg.style.right = 'auto'; dlg.style.bottom = 'auto';
    dlg.style.transform = 'none';  // clear CSS centering
    e.preventDefault();
  }
  function onMove(e) {
    if (!dragging) return;
    dlg.style.left = Math.max(0, Math.min(window.innerWidth  - 80, startL + e.clientX - startX)) + 'px';
    dlg.style.top  = Math.max(0, Math.min(window.innerHeight - 40, startT + e.clientY - startY)) + 'px';
  }
  function onUp() { dragging = false; }
  header.addEventListener('mousedown', onDown);
  document.addEventListener('mousemove', onMove);
  document.addEventListener('mouseup',   onUp);
  return () => {
    header.removeEventListener('mousedown', onDown);
    document.removeEventListener('mousemove', onMove);
    document.removeEventListener('mouseup',   onUp);
  };
}
