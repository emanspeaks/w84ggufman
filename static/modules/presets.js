// Model presets display — mirrors llama-swap's ModelsPanel in plain JS.
//
// Data source: GET /api/llamaswap/models  (SSE-based, returns []Model)
// Log streams:  GET /api/llamaswap/logs/stream/{model_id}
//
// Model states: "ready" | "starting" | "stopping" | "stopped" | "shutdown" | "unknown"
// Load  model:  POST /api/llamaswap/models/load/{id}
// Unload one:   POST /api/llamaswap/models/unload/{id}
// Unload all:   POST /api/llamaswap/models/unload

import { esc, clearErr, showErr } from './utils.js';
import { llamaServerURL } from './status-polling.js';

// ── Persistent UI state ───────────────────────────────────────────────────────
let showUnlisted = true;
const watchedModels = new Set(); // model IDs whose log checkbox is checked

// ── Polling ───────────────────────────────────────────────────────────────────
let pollTimer = null;
const POLL_INTERVAL_MS = 5000;

export async function fetchPresets() {
  clearErr('presets-error');
  try {
    const resp = await fetch('/api/llamaswap/models');
    if (resp.status === 503) {
      document.getElementById('presets-list').innerHTML =
        '<p class="msg-empty">llama-swap is not configured — presets are only available when running in llama-swap mode.</p>';
      stopPresetsPolling();
      return;
    }
    if (!resp.ok) throw new Error(await resp.text());
    const models = await resp.json();
    models.sort((a, b) => ((a.name || a.id) + a.id).localeCompare((b.name || b.id) + b.id, undefined, { numeric: true }));
    renderPresets(models);
  } catch (e) {
    showErr('presets-error', 'Failed to load presets: ' + e.message);
  }
}

export function startPresetsPolling() {
  stopPresetsPolling();
  pollTimer = setInterval(fetchPresets, POLL_INTERVAL_MS);
}

export function stopPresetsPolling() {
  if (pollTimer !== null) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
  stopAllLogStreams();
}

// ── One-time setup (called from app.js on DOMContentLoaded) ──────────────────
export function setupPresets() {
  setupPresetsResize();
  setupLogPaneControls();
  // Fetch backend settings (e.g. presetLogLines) asynchronously.
  fetch('/api/llamaswap/settings')
    .then(r => r.ok ? r.json() : null)
    .then(data => { if (data?.presetLogLines > 0) maxLogLines = data.presetLogLines; })
    .catch(() => {});
}

// ── Resize ────────────────────────────────────────────────────────────────────
function setupPresetsResize() {
  const handle = document.getElementById('presets-resize-handle');
  const topPane = document.getElementById('presets-top-pane');
  if (!handle || !topPane) return;

  let dragging = false, startY = 0, startTopH = 0;

  handle.addEventListener('mousedown', (e) => {
    dragging = true;
    startY = e.clientY;
    startTopH = topPane.getBoundingClientRect().height;
    handle.classList.add('dragging');
    document.body.style.cursor = 'row-resize';
    document.body.style.userSelect = 'none';
    e.preventDefault();
  });

  document.addEventListener('mousemove', (e) => {
    if (!dragging) return;
    const newH = Math.max(80, startTopH + (e.clientY - startY));
    topPane.style.flex = 'none';
    topPane.style.height = newH + 'px';
  });

  document.addEventListener('mouseup', () => {
    if (!dragging) return;
    dragging = false;
    handle.classList.remove('dragging');
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
  });
}

// ── Log pane controls ─────────────────────────────────────────────────────────
function setupLogPaneControls() {
  document.getElementById('clear-logs-btn')?.addEventListener('click', () => {
    logBuffer = [];
    renderLogPane();
  });

  document.getElementById('log-wrap-checkbox')?.addEventListener('change', (e) => {
    document.getElementById('presets-log-output')?.classList.toggle('log-wrap', e.target.checked);
  });

  // Pause auto-scroll when user scrolls up.
  document.getElementById('presets-log-output')?.addEventListener('scroll', () => {
    const el = document.getElementById('presets-log-output');
    if (el) logAutoScroll = (el.scrollHeight - el.scrollTop - el.clientHeight) < 20;
  });
}

// ── Log streaming ─────────────────────────────────────────────────────────────
const activeStreams = new Map(); // modelId → AbortController
let logBuffer = [];             // [{modelId, ts, line}]
let maxLogLines = 30;           // updated by setupPresets
let logAutoScroll = true;

function startLogStream(modelId) {
  stopLogStream(modelId);
  const ctrl = new AbortController();
  activeStreams.set(modelId, ctrl);

  // Model IDs may contain slashes (e.g. "author/model"); encode each segment.
  const path = modelId.split('/').map(encodeURIComponent).join('/');

  (async () => {
    try {
      const resp = await fetch(`/api/llamaswap/logs/stream/${path}`, { signal: ctrl.signal });
      if (!resp.ok) {
        appendLogLine(modelId, `[stream error: HTTP ${resp.status}]`);
        return;
      }
      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      let partial = '';
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        partial += decoder.decode(value, { stream: true });
        const lines = partial.split('\n');
        partial = lines.pop(); // last item may be incomplete
        for (const line of lines) {
          if (line !== '') appendLogLine(modelId, line);
        }
      }
      if (partial) appendLogLine(modelId, partial);
    } catch (e) {
      if (e.name !== 'AbortError') appendLogLine(modelId, `[stream closed: ${e.message}]`);
    }
  })();
}

function stopLogStream(modelId) {
  const ctrl = activeStreams.get(modelId);
  if (ctrl) { ctrl.abort(); activeStreams.delete(modelId); }
}

function stopAllLogStreams() {
  for (const ctrl of activeStreams.values()) ctrl.abort();
  activeStreams.clear();
}

function appendLogLine(modelId, line) {
  const ts = new Date().toLocaleTimeString('en-US', { hour12: false });
  logBuffer.push({ modelId, ts, line });
  if (logBuffer.length > maxLogLines) logBuffer.splice(0, logBuffer.length - maxLogLines);
  renderLogPane();
}

function renderLogPane() {
  const out = document.getElementById('presets-log-output');
  if (!out) return;
  out.textContent = logBuffer.map(e => `[${e.modelId} ${e.ts}]: ${e.line}`).join('\n');
  if (logAutoScroll) out.scrollTop = out.scrollHeight;
}

// ── Table rendering ───────────────────────────────────────────────────────────
function renderPresets(models) {
  const list = document.getElementById('presets-list');
  if (!models || !models.length) {
    list.innerHTML = '<p class="msg-empty">No models found in llama-swap config.</p>';
    return;
  }

  const regularModels = models.filter(m => !m.peerID);
  const peerGroups = {};
  for (const m of models.filter(m => m.peerID)) {
    if (!peerGroups[m.peerID]) peerGroups[m.peerID] = [];
    peerGroups[m.peerID].push(m);
  }

  let html = `
    <div class="presets-controls">
      <label class="checkbox-label">
        <input type="checkbox" id="show-unlisted-checkbox"${showUnlisted ? ' checked' : ''}>
        Show unlisted
      </label>
      <button class="btn-secondary" id="unload-all-btn">Unload All</button>
    </div>
    <table class="presets-table">
      <thead>
        <tr>
          <th class="col-watch"></th>
          <th>Model ID</th>
          <th></th>
          <th>State</th>
        </tr>
      </thead>
      <tbody>
  `;

  for (const model of regularModels) {
    if (!showUnlisted && model.unlisted) continue;
    html += modelRow(model);
  }
  html += '</tbody></table>';

  if (Object.keys(peerGroups).length > 0) {
    const sorted = Object.entries(peerGroups).sort(([a], [b]) => a.localeCompare(b));
    for (const [peerId, peers] of sorted) {
      html += `
        <h3 class="presets-peer-heading">Peer: ${esc(peerId)}</h3>
        <table class="presets-table">
          <tbody>
      `;
      for (const m of peers) {
        html += `
          <tr class="${m.unlisted ? 'row--unlisted' : ''}">
            <td class="col-watch"><input type="checkbox" class="watch-checkbox" data-model-id="${esc(m.id)}"${watchedModels.has(m.id) ? ' checked' : ''}></td>
            <td><span class="model-id">${esc(m.id)}</span></td>
            <td></td>
            <td><span class="status status--${m.state || 'unknown'}">${esc(m.state || 'unknown')}</span></td>
          </tr>
        `;
      }
      html += '</tbody></table>';
    }
  }

  list.innerHTML = html;

  document.getElementById('show-unlisted-checkbox').addEventListener('change', (e) => {
    showUnlisted = e.target.checked;
    fetchPresets();
  });
  document.getElementById('unload-all-btn').addEventListener('click', unloadAllModels);
  list.querySelectorAll('.load-btn').forEach(btn => {
    btn.addEventListener('click', () => loadModel(btn.dataset.modelId));
  });
  list.querySelectorAll('.unload-btn').forEach(btn => {
    btn.addEventListener('click', () => unloadModel(btn.dataset.modelId));
  });
  list.querySelectorAll('.watch-checkbox').forEach(cb => {
    cb.addEventListener('change', (e) => {
      const id = e.target.dataset.modelId;
      if (e.target.checked) { watchedModels.add(id); startLogStream(id); }
      else { watchedModels.delete(id); stopLogStream(id); }
    });
  });
}

function modelRow(model) {
  const displayName = model.name || model.id;
  const canUnload = model.state === 'ready';
  const inTransition = model.state === 'starting' || model.state === 'stopping';

  const actionBtn = model.state === 'stopped' || model.state === 'shutdown'
    ? `<button class="btn-secondary btn--sm load-btn" data-model-id="${esc(model.id)}">Load</button>`
    : `<button class="btn-secondary btn--sm unload-btn" data-model-id="${esc(model.id)}"${!canUnload ? ' disabled' : ''}>${inTransition ? '…' : 'Unload'}</button>`;

  const rowClass = ['row--' + (model.state || 'unknown'), model.unlisted ? 'row--unlisted' : ''].filter(Boolean).join(' ');

  const upstreamBase = llamaServerURL ? llamaServerURL.replace(/\/$/, '') : '';
  const upstreamHref = upstreamBase ? `${upstreamBase}/upstream/${encodeURIComponent(model.id)}/` : '';
  const upstreamLink = upstreamHref
    ? `<a class="upstream-link" href="${upstreamHref}" target="_blank" rel="noopener" title="Open upstream model page">
        <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M7 3H3a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1h9a1 1 0 0 0 1-1V9"/>
          <path d="M10 2h4v4"/><line x1="14" y1="2" x2="7" y2="9"/>
        </svg>
      </a>`
    : '';

  return `
    <tr class="${rowClass}">
      <td class="col-watch"><input type="checkbox" class="watch-checkbox" data-model-id="${esc(model.id)}"${watchedModels.has(model.id) ? ' checked' : ''}></td>
      <td>
        <div class="model-info">
          <span class="model-id">${esc(displayName)}${upstreamLink}</span>
          ${model.description ? `<p class="model-desc"><em>${esc(model.description)}</em></p>` : ''}
          ${model.aliases?.length ? `<p class="model-aliases">Aliases: ${model.aliases.map(esc).join(', ')}</p>` : ''}
        </div>
      </td>
      <td>${actionBtn}</td>
      <td><span class="status status--${model.state}">${esc(model.state)}</span></td>
    </tr>
  `;
}

// ── Model actions ─────────────────────────────────────────────────────────────
async function loadModel(modelId) {
  const btn = document.querySelector(`.load-btn[data-model-id="${modelId}"]`);
  if (btn) { btn.disabled = true; btn.textContent = '…'; }
  fetch(`/api/llamaswap/models/load/${encodeURIComponent(modelId)}`, { method: 'POST' })
    .catch(e => showErr('presets-error', 'Failed to load model: ' + e.message));
  setTimeout(fetchPresets, 800);
}

async function unloadModel(modelId) {
  try {
    const resp = await fetch(`/api/llamaswap/models/unload/${encodeURIComponent(modelId)}`, { method: 'POST' });
    if (!resp.ok) throw new Error(await resp.text());
    fetchPresets();
  } catch (e) {
    showErr('presets-error', 'Failed to unload model: ' + e.message);
  }
}

async function unloadAllModels() {
  try {
    const resp = await fetch('/api/llamaswap/models/unload', { method: 'POST' });
    if (!resp.ok) throw new Error(await resp.text());
    fetchPresets();
  } catch (e) {
    showErr('presets-error', 'Failed to unload all models: ' + e.message);
  }
}
