// Config editor panels

import { setStatusBar } from './status-bar.js';
import { esc } from './utils.js';
import { fetchLocalModels } from './local-models.js';
import { pollStatus } from './status-polling.js';

let panelZ = 1100;

// Tracks open panels by endpoint so addModelToConfig can inject into an existing editor.
const openPanels = new Map(); // endpoint -> { cm, panel }

// Inject a model entry into an already-open editor for the given endpoint.
// Returns true if a panel was found and updated; false if none is open.
export function injectModelEntry(endpoint, entryBlock, modelType, name) {
  const p = openPanels.get(endpoint);
  if (!p) return false;
  const updated = upsertEntryInYaml(p.cm.getValue(), entryBlock, modelType);
  p.cm.setValue(updated);
  const nameEsc = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const m = updated.match(new RegExp(`^  ${nameEsc}\\s*:`, 'm'));
  if (m) {
    const lineNum = updated.slice(0, m.index).split('\n').length - 1;
    p.cm.setCursor(lineNum, 0);
    setTimeout(() => p.cm.scrollIntoView({ line: lineNum, ch: 0 }, 100), 50);
  }
  p.panel.style.zIndex = ++panelZ;
  p.cm.focus();
  return true;
}

// Mirror of the Go upsertModelEntry logic: replace an existing named block or
// insert the new entry at the top (LLM) or bottom (SD) of the models: section.
function upsertEntryInYaml(yaml, entryBlock, modelType) {
  const headerMatch = entryBlock.match(/^  (\S+):/);
  if (!headerMatch) return yaml;
  const name = headerMatch[1];
  const nameEsc = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

  const lines = yaml.split('\n');
  const isStructural = l => { const t = l.trim(); return t !== '' && !t.startsWith('#'); };
  const countIndent = l => l.match(/^ */)[0].length;

  let modelsStart = -1, modelsEnd = lines.length;
  let nameStart = -1, nameEnd = -1;
  for (let i = 0; i < lines.length; i++) {
    if (!isStructural(lines[i])) continue;
    const ind = countIndent(lines[i]);
    if (ind === 0 && /^models\s*:/.test(lines[i])) { modelsStart = i; continue; }
    if (modelsStart >= 0 && ind === 0) { modelsEnd = i; break; }
    if (modelsStart >= 0 && nameStart < 0 && ind === 2 && new RegExp(`^  ${nameEsc}\\s*:`).test(lines[i])) { nameStart = i; continue; }
    if (nameStart >= 0 && nameEnd < 0 && ind <= 2) { nameEnd = i; }
  }
  if (nameStart >= 0 && nameEnd < 0) nameEnd = modelsEnd;

  // Normalise entry: strip trailing blank lines, add one blank separator
  let eLines = entryBlock.split('\n');
  while (eLines.length > 0 && eLines[eLines.length - 1] === '') eLines.pop();
  eLines.push('');

  if (nameStart >= 0) {
    return [...lines.slice(0, nameStart), ...eLines, ...lines.slice(nameEnd)].join('\n');
  }
  if (modelsStart < 0) {
    const out = [...lines];
    if (out.length > 0 && out[out.length - 1] !== '') out.push('');
    out.push('models:');
    out.push(...eLines);
    return out.join('\n');
  }
  let insertAt = modelsStart + 1;
  if (modelType === 'sd') {
    insertAt = modelsEnd;
    while (insertAt > modelsStart + 1 && lines[insertAt - 1] === '') insertAt--;
  }
  return [...lines.slice(0, insertAt), ...eLines, ...lines.slice(insertAt)].join('\n');
}

async function openRawEditPanel({ title, subtitle, endpoint, successMsg, selectName, hintHtml, mode }) {
  let body = '';
  try {
    const resp = await fetch(endpoint);
    if (resp.ok) body = await resp.text();
  } catch (_) {}

  const panel = document.createElement('div');
  panel.className = 'editor-panel';
  panel.style.zIndex = ++panelZ;

  panel.innerHTML = `
    <div class="editor-panel-header">
      <div class="editor-panel-title">
        ${esc(title)}${subtitle ? '<small>' + esc(subtitle) + '</small>' : ''}
      </div>
      <div class="editor-panel-actions">
        <button class="btn-secondary editor-cancel">Cancel</button>
        <button class="btn-secondary editor-apply">Apply</button>
        <button class="btn-primary editor-save">Save</button>
        <button class="editor-close" title="Close">✕</button>
      </div>
    </div>
    <div class="editor-cm-wrap"></div>
    ${hintHtml ? `<div class="editor-hint">${hintHtml}</div>` : ''}
  `;

  document.body.appendChild(panel);

  // Position: centered with stagger for multiple open panels
  const stagger = (panelZ - 1101) % 5;
  const pw = Math.min(720, window.innerWidth * 0.95);
  panel.style.left = Math.max(0, (window.innerWidth - pw) / 2 + stagger * 24) + 'px';
  panel.style.top = (60 + stagger * 24) + 'px';

  // Bring to front on any click within panel
  panel.addEventListener('mousedown', () => { panel.style.zIndex = ++panelZ; }, true);

  // Draggable + maximizable header
  const header = panel.querySelector('.editor-panel-header');
  let maximized = false;
  let savedPos = null;

  function toggleMaximize() {
    maximized = !maximized;
    if (maximized) {
      savedPos = { left: panel.style.left, top: panel.style.top,
                   width: panel.style.width, height: panel.style.height };
      panel.style.left = '0'; panel.style.top = '0';
      panel.style.width = '100%'; panel.style.height = '100%';
      panel.classList.add('maximized');
    } else {
      panel.style.left = savedPos.left; panel.style.top = savedPos.top;
      panel.style.width = savedPos.width || ''; panel.style.height = savedPos.height || '';
      panel.classList.remove('maximized');
    }
    cm.refresh();
  }

  header.addEventListener('dblclick', e => {
    if (e.target.tagName === 'BUTTON') return;
    toggleMaximize();
  });

  header.addEventListener('mousedown', e => {
    if (e.target.tagName === 'BUTTON') return;
    e.preventDefault();
    let ox = e.clientX - panel.offsetLeft;
    let oy = e.clientY - panel.offsetTop;
    let moved = false;
    function onMove(ev) {
      if (!moved) {
        moved = true;
        if (maximized) {
          maximized = false;
          panel.classList.remove('maximized');
          panel.style.width = ''; panel.style.height = '';
          panel.style.left = Math.max(0, ev.clientX - 360) + 'px';
          panel.style.top = Math.max(0, ev.clientY - 20) + 'px';
          ox = ev.clientX - panel.offsetLeft;
          oy = ev.clientY - panel.offsetTop;
          cm.refresh();
        }
      }
      panel.style.left = (ev.clientX - ox) + 'px';
      panel.style.top = (ev.clientY - oy) + 'px';
    }
    function onUp() {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
    }
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  });

  // CodeMirror
  const isLight = document.documentElement.classList.contains('light');
  const cmWrap = panel.querySelector('.editor-cm-wrap');
  const cm = window.CodeMirror(cmWrap, {
    value: body,
    mode: mode || 'yaml',
    theme: isLight ? 'eclipse' : 'dracula',
    lineNumbers: true,
    indentUnit: 2,
    tabSize: 2,
    indentWithTabs: false,
    lineWrapping: true,
    extraKeys: { 'Ctrl-S': () => doSave(false), 'Cmd-S': () => doSave(false) },
  });
  cm.setSize(null, '100%');
  openPanels.set(endpoint, { cm, panel });

  // Refresh CodeMirror on panel resize
  const ro = new ResizeObserver(() => cm.refresh());
  ro.observe(panel);

  // Sync CodeMirror theme when app theme toggles
  const mo = new MutationObserver(() => {
    cm.setOption('theme', document.documentElement.classList.contains('light') ? 'eclipse' : 'dracula');
  });
  mo.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });

  function closePanel() {
    ro.disconnect();
    mo.disconnect();
    openPanels.delete(endpoint);
    document.body.removeChild(panel);
  }

  async function doSave(closeAfter) {
    const saveBtn = panel.querySelector('.editor-save');
    const applyBtn = panel.querySelector('.editor-apply');
    saveBtn.disabled = true;
    applyBtn.disabled = true;
    const origText = closeAfter ? 'Save' : 'Apply';
    const activeBtn = closeAfter ? saveBtn : applyBtn;
    activeBtn.textContent = 'Saving…';
    try {
      const resp = await fetch(endpoint, {
        method: 'PUT',
        headers: { 'Content-Type': 'text/plain' },
        body: cm.getValue(),
      });
      if (!resp.ok) throw new Error(await resp.text());
      setStatusBar('Ready', successMsg, false);
      fetchLocalModels();
      pollStatus();
      if (closeAfter) {
        closePanel();
      } else {
        activeBtn.textContent = origText;
        saveBtn.disabled = false;
        applyBtn.disabled = false;
      }
    } catch (e) {
      setStatusBar('Error', 'Save failed: ' + e.message, false);
      saveBtn.disabled = false;
      applyBtn.disabled = false;
      activeBtn.textContent = origText;
    }
  }

  panel.querySelector('.editor-cancel').addEventListener('click', closePanel);
  panel.querySelector('.editor-close').addEventListener('click', closePanel);
  panel.querySelector('.editor-apply').addEventListener('click', () => doSave(false));
  panel.querySelector('.editor-save').addEventListener('click', () => doSave(true));

  // Scroll to model section if selectName given
  if (selectName) {
    const re = new RegExp('^\\s{2}' + selectName.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + ':\\s*$', 'm');
    const m = body.match(re);
    if (m) {
      const lineNum = body.slice(0, m.index).split('\n').length - 1;
      cm.setCursor(lineNum, 0);
      setTimeout(() => cm.scrollIntoView({ line: lineNum, ch: 0 }, 100), 50);
    }
  }

  cm.focus();
}

export async function openW84ConfigModal() {
  document.getElementById('status-menu').classList.remove('open');
  await openRawEditPanel({
    title: 'Edit W84 Config',
    subtitle: '.w84ggufman.yaml',
    endpoint: '/api/llamaswap/w84config',
    successMsg: '.w84ggufman.yaml saved',
    mode: 'yaml',
    hintHtml: `<div class="tpl-ph-hint">
      <strong>Template placeholders</strong> — w84ggufman expands these when adding a model to config.yaml:<br>
      <code>{{MODEL_PATH}}</code> &mdash; absolute path to the model file<br>
      <code>{{MODEL_NAME}}</code> &mdash; model name / alias<br>
      <code>{{MMPROJ_LINE}}</code> &mdash; <code>--mmproj&nbsp;/path</code>, or blank (line removed if no mmproj file)<br>
      <code>{{VAE_LINE}}</code> &mdash; <code>--vae&nbsp;/path</code>, or blank (line removed if no VAE file)<br>
      <code>ttl:&nbsp;-1</code> &mdash; auto-detect TTL: 600&nbsp;s for &lt;10&nbsp;B-param models, 0&nbsp;(never unload) otherwise<br>
      <code>${'${PORT}'}</code> and other <code>${'${…}'}</code> tokens are llama-swap macros, passed through as-is.
    </div>`,
  });
}

export async function openFullConfigModal(llamaSwapEnabled, selectName) {
  document.getElementById('status-menu').classList.remove('open');
  const isSwap = llamaSwapEnabled;
  const endpoint = isSwap ? '/api/llamaswap/config' : '/api/preset/config';
  const filename = isSwap ? 'config.yaml' : 'models.ini';
  await openRawEditPanel({
    title: 'Edit ' + filename,
    subtitle: null,
    endpoint,
    successMsg: filename + ' saved',
    selectName,
    mode: isSwap ? 'yaml' : 'properties',
  });
}
