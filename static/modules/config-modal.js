// Config editor panels (Monaco-based)

import { setStatusBar } from './status-bar.js';
import { esc } from './utils.js';
import { fetchLocalModels } from './local-models.js';
import { pollStatus } from './status-polling.js';
import { isPhoneViewport, readChromeOffsets, computeMaximizedBounds } from './ui-viewport.mjs';

let panelZ = 1100;

// Tracks open panels by endpoint so injectModelEntry can merge into a live editor.
const openPanels = new Map(); // endpoint -> { editor, panel }

function isIOSDevice() {
  return /iPad|iPhone|iPod/.test(navigator.userAgent) ||
    (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
}

// Test override for editor backend selection.
// Priority: URL param > window flag > localStorage.
// Supported values: monaco | cm6 | textarea.
function getEditorBackendOverride() {
  let urlVal = '';
  try {
    const qp = new URLSearchParams(window.location.search);
    urlVal = (qp.get('editorBackend') || qp.get('forceEditorBackend') || '').trim().toLowerCase();
  } catch (_) {}

  let winVal = '';
  try {
    const raw = window.__W84_EDITOR_BACKEND__;
    winVal = typeof raw === 'string' ? raw.trim().toLowerCase() : '';
  } catch (_) {}

  let lsVal = '';
  try {
    lsVal = (localStorage.getItem('w84.editorBackend') || '').trim().toLowerCase();
  } catch (_) {}

  const val = urlVal || winVal || lsVal;
  if (val === 'monaco' || val === 'cm6' || val === 'textarea') return val;
  return '';
}

function applyPanelMaxBounds(panel) {
  const bounds = computeMaximizedBounds(window.innerWidth, window.innerHeight, readChromeOffsets(), 220);
  panel.style.left = bounds.left + 'px';
  panel.style.top = bounds.top + 'px';
  panel.style.width = bounds.width + 'px';
  panel.style.height = bounds.height + 'px';
}

function createTextareaEditor(cmWrap, initialValue) {
  const ta = document.createElement('textarea');
  ta.className = 'tpl-textarea';
  ta.value = initialValue;
  ta.spellcheck = false;
  ta.style.height = '100%';
  ta.style.minHeight = '100%';
  ta.style.resize = 'none';
  ta.style.border = 'none';
  ta.style.borderRadius = '0';
  cmWrap.appendChild(ta);

  function lineColumnToOffset(text, lineNumber, column) {
    const lines = text.split('\n');
    let idx = 0;
    const clampedLine = Math.max(1, Math.min(lineNumber, lines.length));
    for (let i = 1; i < clampedLine; i++) idx += lines[i - 1].length + 1;
    const lineLen = lines[clampedLine - 1]?.length || 0;
    const clampedCol = Math.max(1, Math.min(column, lineLen + 1));
    return idx + (clampedCol - 1);
  }

  return {
    editor: {
      getValue() { return ta.value; },
      setValue(v) { ta.value = v; },
      setPosition(pos) {
        const i = lineColumnToOffset(ta.value, pos.lineNumber, pos.column);
        ta.setSelectionRange(i, i);
      },
      revealLineInCenter(lineNumber) {
        const lines = ta.value.split('\n');
        const target = Math.max(1, Math.min(lineNumber, lines.length));
        const ratio = target / Math.max(1, lines.length);
        ta.scrollTop = Math.max(0, (ta.scrollHeight - ta.clientHeight) * ratio - ta.clientHeight / 2);
      },
      focus() { ta.focus({ preventScroll: true }); },
      dispose() {},
    },
    inputEl: ta,
    dispose() {},
  };
}

async function createCodeMirror6Editor(cmWrap, initialValue, mode) {
  const [
    state,
    view,
    commands,
    language,
    langYaml,
    legacyProps,
    themeOneDark,
  ] = await Promise.all([
    import('https://esm.sh/@codemirror/state@6.5.2'),
    import('https://esm.sh/@codemirror/view@6.33.0'),
    import('https://esm.sh/@codemirror/commands@6.6.2'),
    import('https://esm.sh/@codemirror/language@6.10.2'),
    import('https://esm.sh/@codemirror/lang-yaml@6.1.1'),
    import('https://esm.sh/@codemirror/legacy-modes@6.4.2/mode/properties'),
    import('https://esm.sh/@codemirror/theme-one-dark@6.1.2'),
  ]);

  const langExt = mode === 'yaml'
    ? langYaml.yaml()
    : mode === 'properties'
      ? language.StreamLanguage.define(legacyProps.properties)
      : [];

  const isLight = document.documentElement.classList.contains('light');
  const extensions = [
    view.lineNumbers(),
    commands.history(),
    language.syntaxHighlighting(language.defaultHighlightStyle),
    view.keymap.of([
      ...commands.defaultKeymap,
      ...commands.historyKeymap,
      commands.indentWithTab,
    ]),
    view.EditorView.lineWrapping,
    langExt,
    view.EditorView.theme({
      '&': {
        height: '100%',
        fontSize: '13px',
        fontFamily: "'Menlo','Consolas','Monaco',monospace",
      },
      '.cm-scroller': { overflow: 'auto' },
      '.cm-content': { minHeight: '100%' },
      '.cm-focused': { outline: 'none' },
    }),
  ];
  if (!isLight) {
    extensions.push(themeOneDark.oneDark);
  }

  const cmState = state.EditorState.create({
    doc: initialValue,
    extensions,
  });

  const cmView = new view.EditorView({
    state: cmState,
    parent: cmWrap,
  });

  function lineColumnToOffset(text, lineNumber, column) {
    const lines = text.split('\n');
    let idx = 0;
    const clampedLine = Math.max(1, Math.min(lineNumber, lines.length));
    for (let i = 1; i < clampedLine; i++) idx += lines[i - 1].length + 1;
    const lineLen = lines[clampedLine - 1]?.length || 0;
    const clampedCol = Math.max(1, Math.min(column, lineLen + 1));
    return idx + (clampedCol - 1);
  }

  return {
    editor: {
      getValue() {
        return cmView.state.doc.toString();
      },
      setValue(v) {
        cmView.dispatch({
          changes: { from: 0, to: cmView.state.doc.length, insert: v },
        });
      },
      setPosition(pos) {
        const offset = lineColumnToOffset(cmView.state.doc.toString(), pos.lineNumber, pos.column);
        cmView.dispatch({ selection: { anchor: offset } });
      },
      revealLineInCenter(lineNumber) {
        const line = cmView.state.doc.line(Math.max(1, Math.min(lineNumber, cmView.state.doc.lines)));
        cmView.dispatch({ selection: { anchor: line.from } });
        cmView.scrollPosIntoView(line.from);
      },
      focus() {
        cmView.focus();
      },
      dispose() {
        cmView.destroy();
      },
    },
    inputEl: cmView.dom,
    dispose() {
      cmView.destroy();
    },
  };
}

function monacoTheme() {
  return document.documentElement.classList.contains('light') ? 'vs' : 'vs-dark';
}

// navigator.clipboard requires a secure context (HTTPS or localhost). When the
// page is served over plain HTTP from a non-localhost host (e.g. behind a
// non-TLS proxy) we fall back to the legacy textarea + execCommand path.
async function copyToClipboard(text) {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch (_) { /* fall through */ }
  }
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.setAttribute('readonly', '');
  ta.style.cssText = 'position:fixed;top:0;left:0;width:1px;height:1px;opacity:0;';
  document.body.appendChild(ta);
  const prevActive = document.activeElement;
  ta.focus();
  ta.select();
  let ok = false;
  try { ok = document.execCommand('copy'); } catch (_) { ok = false; }
  document.body.removeChild(ta);
  if (prevActive && typeof prevActive.focus === 'function') prevActive.focus();
  return ok;
}

// "yaml" / "properties" → Monaco language IDs
function monacoLanguage(mode) {
  if (mode === 'properties') return 'ini';
  return mode || 'yaml';
}

// Inject a model entry into an already-open editor for the given endpoint.
// Returns true if the entry is now present in a live panel; false if no panel
// is open, the panel has been detached, or the entryBlock can't be parsed
// (caller should fall back to opening a fresh editor).
export function injectModelEntry(endpoint, entryBlock, modelType, name) {
  if (!entryBlock || !name) return false;
  const headerMatch = entryBlock.match(/^  (\S+):/);
  if (!headerMatch) return false;
  const p = openPanels.get(endpoint);
  if (!p) return false;
  if (!p.panel.isConnected) {
    openPanels.delete(endpoint);
    return false;
  }
  const original = p.editor.getValue();
  const updated = upsertEntryInYaml(original, entryBlock, modelType);
  if (updated !== original) {
    p.editor.setValue(updated);
  }
  const nameEsc = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const m = updated.match(new RegExp(`^  ${nameEsc}\\s*:`, 'm'));
  if (m) {
    const lineNum = updated.slice(0, m.index).split('\n').length; // Monaco is 1-based
    p.editor.setPosition({ lineNumber: lineNum, column: 1 });
    setTimeout(() => p.editor.revealLineInCenter(lineNum), 50);
  }
  p.panel.style.zIndex = ++panelZ;
  p.editor.focus();
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
  const editorBackendOverride = getEditorBackendOverride();
  const useCodeMirrorFallback = editorBackendOverride
    ? (editorBackendOverride === 'cm6' || editorBackendOverride === 'textarea')
    : isIOSDevice();
  // Kick off editor load and file fetch in parallel when Monaco is used.
  const monacoP = useCodeMirrorFallback ? Promise.resolve(null) : window.monacoReady;
  let body = '';
  try {
    const resp = await fetch(endpoint);
    if (resp.ok) body = await resp.text();
  } catch (_) {}
  const monaco = await monacoP;

  const panel = document.createElement('div');
  panel.className = 'editor-panel';
  panel.style.zIndex = ++panelZ;

  panel.innerHTML = `
    <div class="editor-panel-header">
      <div class="editor-panel-title">
        ${esc(title)}${subtitle ? '<small>' + esc(subtitle) + '</small>' : ''}
      </div>
      <div class="editor-panel-actions">
        <button class="btn-secondary editor-copy" title="Copy editor contents to clipboard">Copy</button>
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

  const phoneViewport = isPhoneViewport();
  if (phoneViewport) panel.classList.add('mobile-dialog-locked');

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
    if (phoneViewport) return;
    maximized = !maximized;
    if (maximized) {
      savedPos = { left: panel.style.left, top: panel.style.top,
                   width: panel.style.width, height: panel.style.height };
      applyPanelMaxBounds(panel);
      panel.classList.add('maximized');
    } else {
      panel.style.left = savedPos.left; panel.style.top = savedPos.top;
      panel.style.width = savedPos.width || ''; panel.style.height = savedPos.height || '';
      panel.classList.remove('maximized');
    }
    // automaticLayout handles relayout on its own; no manual call needed.
  }

  function onViewportChange() {
    if (maximized) applyPanelMaxBounds(panel);
  }
  window.addEventListener('resize', onViewportChange, { passive: true });
  window.addEventListener('orientationchange', onViewportChange, { passive: true });

  header.addEventListener('dblclick', e => {
    if (phoneViewport) return;
    if (e.target.tagName === 'BUTTON') return;
    toggleMaximize();
  });

  header.addEventListener('mousedown', e => {
    if (phoneViewport) return;
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

  // Editor
  const cmWrap = panel.querySelector('.editor-cm-wrap');
  let editor;
  let editorInputEl = null;
  let disposeEditorExtras = () => {};
  if (useCodeMirrorFallback) {
    if (editorBackendOverride === 'textarea') {
      const fallback = createTextareaEditor(cmWrap, body);
      editor = fallback.editor;
      editorInputEl = fallback.inputEl;
      disposeEditorExtras = fallback.dispose;
    } else {
      try {
        const cm6 = await createCodeMirror6Editor(cmWrap, body, mode);
        editor = cm6.editor;
        editorInputEl = cm6.inputEl;
        disposeEditorExtras = cm6.dispose;
      } catch (_) {
        const fallback = createTextareaEditor(cmWrap, body);
        editor = fallback.editor;
        editorInputEl = fallback.inputEl;
        disposeEditorExtras = fallback.dispose;
      }
    }
  } else {
    editor = monaco.editor.create(cmWrap, {
      value: body,
      language: monacoLanguage(mode),
      theme: monacoTheme(),
      automaticLayout: true,
      tabSize: 2,
      insertSpaces: true,
      wordWrap: 'on',
      minimap: { enabled: false },
      fontSize: 13,
      fontFamily: "'Menlo','Consolas','Monaco',monospace",
      scrollBeyondLastLine: false,
      renderWhitespace: 'selection',
      guides: { indentation: true, highlightActiveIndentation: true, bracketPairs: true },
      bracketPairColorization: { enabled: true },
    });
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => doSave(false));

    // Sync theme when app theme toggles. setTheme is global — affects all editors.
    const mo = new MutationObserver(() => monaco.editor.setTheme(monacoTheme()));
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
    disposeEditorExtras = () => mo.disconnect();
  }
  openPanels.set(endpoint, { editor, panel });

  // On iOS, ensure focus is moved from user interaction to help open software keyboard.
  if (editorInputEl) {
    editorInputEl.addEventListener('keydown', (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
        e.preventDefault();
        doSave(false);
      }
    });
    cmWrap.addEventListener('touchend', () => editor.focus(), { passive: true });
  }

  function closePanel() {
    disposeEditorExtras();
    window.removeEventListener('resize', onViewportChange);
    window.removeEventListener('orientationchange', onViewportChange);
    openPanels.delete(endpoint);
    editor.dispose();
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
        body: editor.getValue(),
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
  panel.querySelector('.editor-copy').addEventListener('click', async (e) => {
    const btn = e.currentTarget;
    const orig = btn.textContent;
    const ok = await copyToClipboard(editor.getValue());
    btn.textContent = ok ? 'Copied!' : 'Copy failed';
    if (ok) {
      setStatusBar('Ready', 'Copied editor contents to clipboard', false);
    } else {
      setStatusBar('Error', 'Copy failed — clipboard access denied', false);
    }
    setTimeout(() => { btn.textContent = orig; }, 1500);
  });

  // Scroll to model section if selectName given
  if (selectName) {
    const re = new RegExp('^\\s{2}' + selectName.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + ':\\s*$', 'm');
    const m = body.match(re);
    if (m) {
      const lineNum = body.slice(0, m.index).split('\n').length; // 1-based
      editor.setPosition({ lineNumber: lineNum, column: 1 });
      setTimeout(() => editor.revealLineInCenter(lineNum), 50);
    }
  }

  if (phoneViewport) {
    maximized = true;
    panel.classList.add('maximized');
    applyPanelMaxBounds(panel);
  }

  editor.focus();
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
