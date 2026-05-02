// Model presets display — mirrors llama-swap's ModelsPanel in plain JS.
//
// Data source (primary): SSE /api/llamaswap/models/stream (modelStatus updates)
// Fallback:             GET /api/llamaswap/models  (snapshot)
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
const PRESETS_FETCH_TIMEOUT_MS = 12000;
const POLLING_FALLBACK_DELAY_MS = 2500;
let presetsFetchInFlight = false;
let presetsFetchAbort = null;
let modelStatusEventSource = null;
let fallbackArmTimer = null;
let presetsUIBound = false;
let presetsFetchedOnce = false;
let lastDetailsHydrateAt = 0;
const DETAILS_HYDRATE_COOLDOWN_MS = 30000;
const modelDetailsCache = new Map();

function makeTimeoutError(timeoutMs) {
  return new DOMException(
    `request timed out after ${Math.ceil(timeoutMs / 1000)}s`,
    'TimeoutError'
  );
}

function presetsTimeoutMessage(action) {
  return `${action} timed out. This page may have gone stale after being open for a long time. Refresh and try again.`;
}

function mergeModelDetails(models) {
  return models.map((model) => {
    const cached = modelDetailsCache.get(model.id) || {};
    const merged = { ...model };

    if (!merged.name && cached.name) merged.name = cached.name;
    if (!merged.description && cached.description) merged.description = cached.description;
    if ((!Array.isArray(merged.aliases) || merged.aliases.length === 0) && Array.isArray(cached.aliases) && cached.aliases.length > 0) {
      merged.aliases = cached.aliases;
    }
    if ((merged.unlisted == null) && (cached.unlisted != null)) merged.unlisted = cached.unlisted;

    const nextCached = { ...cached };
    if (merged.name) nextCached.name = merged.name;
    if (merged.description) nextCached.description = merged.description;
    if (Array.isArray(merged.aliases) && merged.aliases.length > 0) nextCached.aliases = merged.aliases;
    if (merged.unlisted != null) nextCached.unlisted = merged.unlisted;
    modelDetailsCache.set(model.id, nextCached);

    return merged;
  });
}

function shouldHydrateModelDetails(models) {
  return models.some((model) => !model.name && !model.description && (!Array.isArray(model.aliases) || model.aliases.length === 0));
}

async function fetchWithTimeout(url, opts = {}, timeoutMs = PRESETS_FETCH_TIMEOUT_MS) {
  const ctrl = new AbortController();
  const t = setTimeout(() => ctrl.abort(makeTimeoutError(timeoutMs)), timeoutMs);
  try {
    const merged = { ...opts, signal: ctrl.signal };
    return await fetch(url, merged);
  } finally {
    clearTimeout(t);
  }
}

export async function fetchPresets() {
  if (presetsFetchInFlight) return;
  clearErr('presets-error');
  presetsFetchInFlight = true;
  const ctrl = new AbortController();
  presetsFetchAbort = ctrl;
  const t = setTimeout(() => ctrl.abort(makeTimeoutError(PRESETS_FETCH_TIMEOUT_MS)), PRESETS_FETCH_TIMEOUT_MS);
  try {
    const resp = await fetch('/api/llamaswap/models', { signal: ctrl.signal });
    if (resp.status === 503) {
      document.getElementById('presets-list').innerHTML =
        '<p class="msg-empty">llama-swap is not configured — presets are only available when running in llama-swap mode.</p>';
      stopPresetsPolling();
      return;
    }
    if (!resp.ok) throw new Error(await resp.text());
    const models = mergeModelDetails(await resp.json());
    models.sort((a, b) => ((a.name || a.id) + a.id).localeCompare((b.name || b.id) + b.id, undefined, { numeric: true }));
    renderPresets(models);
    presetsFetchedOnce = true;
  } catch (e) {
    if (e.name === 'TimeoutError') {
      showErr('presets-error', presetsTimeoutMessage('Refreshing model status'));
    } else if (e.name !== 'AbortError') {
      showErr('presets-error', 'Failed to load presets: ' + e.message);
    }
  } finally {
    clearTimeout(t);
    if (presetsFetchAbort === ctrl) presetsFetchAbort = null;
    presetsFetchInFlight = false;
  }
}

export function startPresetsPolling() {
  stopPresetsPolling();
  startModelStatusStream();
}

export function stopPresetsPolling() {
  stopModelStatusStream();
  disarmPollingFallback();
  if (pollTimer !== null) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
  if (presetsFetchAbort) {
    presetsFetchAbort.abort();
    presetsFetchAbort = null;
  }
  presetsFetchInFlight = false;
  stopAllLogStreams();
}

function ensurePollingFallback() {
  if (pollTimer !== null) return;
  fetchPresets();
  pollTimer = setInterval(fetchPresets, POLL_INTERVAL_MS);
}

function armPollingFallback() {
  if (pollTimer !== null || fallbackArmTimer !== null) return;
  fallbackArmTimer = setTimeout(() => {
    fallbackArmTimer = null;
    ensurePollingFallback();
  }, POLLING_FALLBACK_DELAY_MS);
}

function disarmPollingFallback() {
  if (fallbackArmTimer !== null) {
    clearTimeout(fallbackArmTimer);
    fallbackArmTimer = null;
  }
}

function stopPollingFallback() {
  disarmPollingFallback();
  if (pollTimer !== null) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

function handleModelStatusData(data) {
  let models;
  try {
    models = JSON.parse(data);
  } catch {
    return;
  }
  if (!Array.isArray(models)) return;
  models = mergeModelDetails(models);
  clearErr('presets-error');
  models.sort((a, b) => ((a.name || a.id) + a.id).localeCompare((b.name || b.id) + b.id, undefined, { numeric: true }));
  renderPresets(models);
  if (shouldHydrateModelDetails(models) && Date.now() - lastDetailsHydrateAt >= DETAILS_HYDRATE_COOLDOWN_MS) {
    lastDetailsHydrateAt = Date.now();
    fetchPresets();
  }
}

function startModelStatusStream() {
  if (typeof EventSource === 'undefined') {
    ensurePollingFallback();
    return;
  }
  stopModelStatusStream();
  const es = new EventSource('/api/llamaswap/models/stream');
  modelStatusEventSource = es;

  es.addEventListener('open', () => {
    stopPollingFallback();
    if (!presetsFetchedOnce) fetchPresets();
  });

  es.addEventListener('modelStatus', (ev) => {
    stopPollingFallback();
    handleModelStatusData(ev.data || '');
  });

  es.onerror = () => {
    // EventSource auto-reconnects; only start polling if disconnect persists.
    armPollingFallback();
  };
}

function stopModelStatusStream() {
  if (modelStatusEventSource) {
    modelStatusEventSource.close();
    modelStatusEventSource = null;
  }
}

// ── One-time setup (called from app.js on DOMContentLoaded) ──────────────────
export function setupPresets() {
  if (presetsUIBound) return;
  presetsUIBound = true;

  setupPresetsResize();
  setupLogPaneControls();
  updateLogPaneVisibility();
  // Fetch backend settings (e.g. presetLogLines) asynchronously.
  fetch('/api/llamaswap/settings')
    .then(r => r.ok ? r.json() : null)
    .then(data => {
      if (data?.presetLogLines > 0) {
        maxLogLines = data.presetLogLines;
        const inp = document.getElementById('log-lines-input');
        if (inp) inp.value = maxLogLines;
      }
    })
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

function applyScrollbackLimit() {
  const inp = document.getElementById('log-lines-input');
  const v = parseInt(inp?.value, 10);
  if (v >= 10 && v <= 9999) {
    maxLogLines = v;
    for (const buf of modelBuffers.values()) {
      if (buf.length > maxLogLines) buf.splice(0, buf.length - maxLogLines);
    }
    renderLogPane();
  }
}

function setWatchAll(checked) {
  document.querySelectorAll('.watch-checkbox').forEach(cb => {
    if (cb.checked !== checked) {
      cb.checked = checked;
      cb.dispatchEvent(new Event('change', { bubbles: true }));
    }
  });
}

// ── Log pane visibility ───────────────────────────────────────────────────────
function updateLogPaneVisibility() {
  const hidden = watchedModels.size === 0;
  document.getElementById('presets-bottom-pane')?.classList.toggle('log-pane-hidden', hidden);
  document.getElementById('presets-resize-handle')?.classList.toggle('log-pane-hidden', hidden);
}

// ── Log pane controls ─────────────────────────────────────────────────────────
function setupLogPaneControls() {
  document.getElementById('log-lines-apply-btn')?.addEventListener('click', () => applyScrollbackLimit());
  document.getElementById('log-lines-input')?.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') applyScrollbackLimit();
  });

  document.getElementById('clear-logs-btn')?.addEventListener('click', () => {
    modelBuffers.clear();
    renderLogPane();
  });

  document.getElementById('log-wrap-btn')?.addEventListener('click', (e) => {
    const out = document.getElementById('presets-log-output');
    const btn = e.currentTarget;
    btn.classList.toggle('active', !!out?.classList.toggle('log-wrap'));
  });

  document.getElementById('log-fontsize-btn')?.addEventListener('click', () => {
    const out = document.getElementById('presets-log-output');
    if (!out) return;
    out.classList.remove(...LOG_FONT_SIZES.filter(Boolean));
    logFontSizeIdx = (logFontSizeIdx + 1) % LOG_FONT_SIZES.length;
    if (LOG_FONT_SIZES[logFontSizeIdx]) out.classList.add(LOG_FONT_SIZES[logFontSizeIdx]);
  });

  document.getElementById('log-inplace-btn')?.addEventListener('click', (e) => {
    logInPlace = !logInPlace;
    e.currentTarget.classList.toggle('active', logInPlace);
  });

  document.getElementById('log-filter-btn')?.addEventListener('click', (e) => {
    const row = document.getElementById('log-filter-row');
    const nowVisible = row?.style.display === 'none';
    if (row) row.style.display = nowVisible ? '' : 'none';
    e.currentTarget.classList.toggle('active', nowVisible);
    if (!nowVisible) {
      const inp = document.getElementById('log-filter-input');
      if (inp) { inp.value = ''; inp.classList.remove('filter-invalid'); }
      renderLogPane();
    } else {
      document.getElementById('log-filter-input')?.focus();
    }
  });

  document.getElementById('log-filter-input')?.addEventListener('input', () => renderLogPane());

  document.getElementById('log-filter-clear')?.addEventListener('click', () => {
    const inp = document.getElementById('log-filter-input');
    if (inp) { inp.value = ''; inp.classList.remove('filter-invalid'); }
    renderLogPane();
  });

  // Pause auto-scroll when user scrolls up.
  document.getElementById('presets-log-output')?.addEventListener('scroll', () => {
    const el = document.getElementById('presets-log-output');
    if (el) logAutoScroll = (el.scrollHeight - el.scrollTop - el.clientHeight) < 20;
  });
}

// ── Log streaming ─────────────────────────────────────────────────────────────
const activeStreams = new Map();  // modelId → AbortController
const modelBuffers = new Map();   // modelId → [{seq, ts, line}]
let globalSeq = 0;
let maxLogLines = 200;            // lines stored per model; synced with #log-lines-input
let logAutoScroll = true;
let logFontSizeIdx = 0;           // index into LOG_FONT_SIZES
const LOG_FONT_SIZES = ['', 'log-size-sm', 'log-size-xs', 'log-size-xxs']; // '' = default (largest)
let logInPlace = true;            // \r lines overwrite last entry (progress bars)

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
        feedChunk(modelId, `[stream error: HTTP ${resp.status}]\n`);
        return;
      }
      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        feedChunk(modelId, decoder.decode(value, { stream: true }));
      }
      finalizeLiveLine(modelId);
    } catch (e) {
      if (e.name !== 'AbortError') feedChunk(modelId, `[stream closed: ${e.message}]\n`);
    }
  })();
}

function stopLogStream(modelId) {
  const ctrl = activeStreams.get(modelId);
  if (ctrl) { ctrl.abort(); activeStreams.delete(modelId); }
  finalizeLiveLine(modelId);
}

function stopAllLogStreams() {
  for (const ctrl of activeStreams.values()) ctrl.abort();
  activeStreams.clear();
}

// ── Real-time chunk-based log feeding ────────────────────────────────────────
// Each model has a "live" (in-progress) entry at the tail of its buffer.
// Characters append to it in real time.  \n finalizes the entry and opens a
// new one.  \r resets the live entry's content (progress-bar carriage return).
// When logInPlace is off, \r is treated the same as \n.

let renderScheduled = false;
function scheduleRender() {
  if (!renderScheduled) {
    renderScheduled = true;
    requestAnimationFrame(() => { renderScheduled = false; renderLogPane(); });
  }
}

function ensureLiveLine(buf) {
  if (buf.length === 0 || buf[buf.length - 1].finalized) {
    buf.push({ seq: globalSeq++, ts: '', tsMs: 0, line: '', finalized: false });
  }
  return buf[buf.length - 1];
}

function finalizeLiveLine(modelId) {
  const buf = modelBuffers.get(modelId);
  if (!buf || buf.length === 0) return;
  const live = buf[buf.length - 1];
  if (live.finalized) return;
  if (!live.line.trim()) { buf.pop(); }
  else { live.finalized = true; }
  scheduleRender();
}

function stripNonSgrAnsi(s) {
  // Remove CSI sequences whose final byte is NOT 'm' (SGR/colour):
  // \x1b[K (erase EOL), cursor movement, etc.  SGR colour codes are kept.
  return s.replace(/\x1b\[[\d;]*[A-Za-ln-z@\[\\\]^_`{|}~]/g, '');
}

function feedChunk(modelId, rawChunk) {
  const chunk = stripNonSgrAnsi(rawChunk);
  if (!chunk) return;
  let buf = modelBuffers.get(modelId);
  if (!buf) { buf = []; modelBuffers.set(modelId, buf); }

  const stamp = (entry) => {
    const ms = Date.now();
    entry.tsMs = ms;
    entry.ts = new Date(ms).toLocaleTimeString('en-US', { hour12: false });
  };

  // Split on \n and \r, keeping the delimiters so we can handle each type.
  const parts = chunk.split(/(\n|\r)/);
  for (const part of parts) {
    if (part === '\n') {
      // Finalize the live line.
      const live = ensureLiveLine(buf);
      stamp(live);
      live.finalized = true;
      if (!live.line.trim()) buf.pop(); // discard empty lines
      if (buf.length > maxLogLines) buf.splice(0, buf.length - maxLogLines);
    } else if (part === '\r') {
      if (logInPlace) {
        // Carriage return: reset the live line's content but keep its slot.
        const live = ensureLiveLine(buf);
        live.line = '';
        stamp(live);
      } else {
        // Treat \r as \n when in-place is off.
        const live = ensureLiveLine(buf);
        stamp(live);
        live.finalized = true;
        if (!live.line.trim()) buf.pop();
        if (buf.length > maxLogLines) buf.splice(0, buf.length - maxLogLines);
      }
    } else if (part) {
      const live = ensureLiveLine(buf);
      live.line += part;
      stamp(live);
    }
  }
  scheduleRender();
}

function renderLogPane() {
  const out = document.getElementById('presets-log-output');
  if (!out) return;

  // Merge all watched-model buffers, sorted by most recent update time.
  const all = [];
  for (const [modelId, buf] of modelBuffers) {
    for (const e of buf) all.push({ modelId, seq: e.seq, ts: e.ts, tsMs: e.tsMs || 0, line: e.line });
  }
  all.sort((a, b) => (a.tsMs - b.tsMs) || (a.seq - b.seq));

  // Apply regex filter.
  const filterVal = document.getElementById('log-filter-input')?.value || '';
  let rows = all;
  if (filterVal) {
    try {
      const re = new RegExp(filterVal, 'i');
      rows = all.filter(e => re.test(e.line));
      document.getElementById('log-filter-input')?.classList.remove('filter-invalid');
    } catch {
      document.getElementById('log-filter-input')?.classList.add('filter-invalid');
    }
  }

  // Render with ANSI colour support.
  out.innerHTML = rows.map(e => parseAnsiLine(`[${e.modelId} ${e.ts}]: ${e.line}`)).join('\n');
  if (logAutoScroll) out.scrollTop = out.scrollHeight;
}

// ── ANSI escape code parser ───────────────────────────────────────────────────
function openAnsiSpan(params) {
  if (!params || params === '0') return '<span>';
  const codes = params.split(';').map(Number);
  const cls = [];
  for (let i = 0; i < codes.length; i++) {
    const c = codes[i];
    if (c === 1) cls.push('ansi-bold');
    else if (c === 2) cls.push('ansi-dim');
    else if (c === 3) cls.push('ansi-italic');
    else if (c === 4) cls.push('ansi-underline');
    else if (c === 5 || c === 6) cls.push('ansi-blink');
    else if (c === 7) cls.push('ansi-reverse');
    else if (c >= 30 && c <= 37) cls.push(`ansi-fg-${c - 30}`);
    else if (c >= 40 && c <= 47) cls.push(`ansi-bg-${c - 40}`);
    else if (c >= 90 && c <= 97) cls.push(`ansi-fg-${c - 90 + 8}`);
    else if (c >= 100 && c <= 107) cls.push(`ansi-bg-${c - 100 + 8}`);
    else if (c === 38 && codes[i + 1] === 5 && i + 2 < codes.length) {
      cls.push(`ansi-fg-${codes[i + 2]}`); i += 2;
    } else if (c === 48 && codes[i + 1] === 5 && i + 2 < codes.length) {
      cls.push(`ansi-bg-${codes[i + 2]}`); i += 2;
    }
  }
  return cls.length ? `<span class="${cls.join(' ')}">` : '<span>';
}

function parseAnsiLine(line) {
  let result = '<span>';
  let i = 0;
  while (i < line.length) {
    if (line.charCodeAt(i) === 0x1b && line[i + 1] === '[') {
      // Scan to end of CSI sequence (final byte: 0x40–0x7e)
      let j = i + 2;
      while (j < line.length && !(line.charCodeAt(j) >= 0x40 && line.charCodeAt(j) <= 0x7e)) j++;
      if (line[j] === 'm') result += '</span>' + openAnsiSpan(line.slice(i + 2, j));
      // Skip all other CSI sequences (cursor movement, erase, etc.)
      i = j + 1;
    } else {
      const ch = line[i];
      if (ch === '&') result += '&amp;';
      else if (ch === '<') result += '&lt;';
      else if (ch === '>') result += '&gt;';
      else result += ch;
      i++;
    }
  }
  return result + '</span>';
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
      <button class="btn-secondary" id="watch-all-btn">Watch All</button>
      <button class="btn-secondary" id="watch-none-btn">Unwatch All</button>
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
  document.getElementById('show-unlisted-checkbox')?.addEventListener('change', (e) => {
    showUnlisted = e.target.checked;
    fetchPresets();
  });
  document.getElementById('watch-all-btn')?.addEventListener('click', () => setWatchAll(true));
  document.getElementById('watch-none-btn')?.addEventListener('click', () => setWatchAll(false));
  document.getElementById('unload-all-btn')?.addEventListener('click', unloadAllModels);
  list.querySelectorAll('.load-btn').forEach(btn => {
    btn.addEventListener('click', () => loadModel(btn.dataset.modelId));
  });
  list.querySelectorAll('.unload-btn').forEach(btn => {
    btn.addEventListener('click', () => unloadModel(btn.dataset.modelId));
  });
  list.querySelectorAll('.watch-checkbox').forEach(cb => {
    const id = cb.dataset.modelId;
    cb.addEventListener('change', (e) => {
      if (e.target.checked) {
        watchedModels.add(id);
        startLogStream(id);
      } else {
        watchedModels.delete(id);
        stopLogStream(id);
        modelBuffers.delete(id);
        renderLogPane();
      }
      updateLogPaneVisibility();
    });
    // Auto-resume stream if this model was already watched (e.g. after mode switch)
    if (watchedModels.has(id) && !activeStreams.has(id)) startLogStream(id);
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
  fetchWithTimeout(`/api/llamaswap/models/load/${encodeURIComponent(modelId)}`, { method: 'POST' }, 15000)
    .catch(e => showErr('presets-error', e.name === 'TimeoutError'
      ? presetsTimeoutMessage('Loading model')
      : 'Failed to load model: ' + e.message));
  setTimeout(fetchPresets, 800);
}

async function unloadModel(modelId) {
  try {
    const resp = await fetchWithTimeout(`/api/llamaswap/models/unload/${encodeURIComponent(modelId)}`, { method: 'POST' }, 15000);
    if (!resp.ok) throw new Error(await resp.text());
    fetchPresets();
  } catch (e) {
    showErr('presets-error', e.name === 'TimeoutError'
      ? presetsTimeoutMessage('Unloading model')
      : 'Failed to unload model: ' + e.message);
  }
}

async function unloadAllModels() {
  try {
    const resp = await fetchWithTimeout('/api/llamaswap/models/unload', { method: 'POST' }, 15000);
    if (!resp.ok) throw new Error(await resp.text());
    fetchPresets();
  } catch (e) {
    showErr('presets-error', e.name === 'TimeoutError'
      ? presetsTimeoutMessage('Unloading all models')
      : 'Failed to unload all models: ' + e.message);
  }
}
