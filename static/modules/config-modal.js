// Config editor panels

import { setStatusBar } from './status-bar.js';
import { esc } from './utils.js';
import { fetchLocalModels } from './local-models.js';
import { pollStatus } from './status-polling.js';

let panelZ = 1100;

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

  // Draggable header
  const header = panel.querySelector('.editor-panel-header');
  header.addEventListener('mousedown', e => {
    if (e.target.tagName === 'BUTTON') return;
    e.preventDefault();
    const ox = e.clientX - panel.offsetLeft;
    const oy = e.clientY - panel.offsetTop;
    function onMove(ev) {
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
    extraKeys: { 'Ctrl-S': () => save(), 'Cmd-S': () => save() },
  });
  cm.setSize(null, '100%');

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
    document.body.removeChild(panel);
  }

  async function save() {
    const saveBtn = panel.querySelector('.editor-save');
    saveBtn.disabled = true;
    saveBtn.textContent = 'Saving…';
    try {
      const resp = await fetch(endpoint, {
        method: 'PUT',
        headers: { 'Content-Type': 'text/plain' },
        body: cm.getValue(),
      });
      if (!resp.ok) throw new Error(await resp.text());
      closePanel();
      setStatusBar('Ready', successMsg, false);
      fetchLocalModels();
      pollStatus();
    } catch (e) {
      setStatusBar('Error', 'Save failed: ' + e.message, false);
      saveBtn.disabled = false;
      saveBtn.textContent = 'Save';
    }
  }

  panel.querySelector('.editor-cancel').addEventListener('click', closePanel);
  panel.querySelector('.editor-close').addEventListener('click', closePanel);
  panel.querySelector('.editor-save').addEventListener('click', save);

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
