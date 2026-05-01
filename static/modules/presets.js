// Model presets display — mirrors llama-swap's ModelsPanel.svelte in plain JS.
//
// Data source: llama-swap's GET /api/models/ endpoint (proxied via our backend
// at /api/llamaswap/models).  Each entry has: id, name, description, state,
// unlisted, aliases[], peerID.
//
// State values: "ready" | "starting" | "stopping" | "stopped" | "shutdown" | "unknown"
// Load  model: POST /api/llamaswap/models/load/{id}
// Unload one : POST /api/llamaswap/models/unload/{id}
// Unload all : POST /api/llamaswap/models/unload

import { esc, clearErr, showErr } from './utils.js';

// Preserve "show unlisted" preference across re-renders.
let showUnlisted = true;
// Polling timer handle — cleared when the panel is not visible.
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
    // Sort: regular models A–Z, peer models appended after.
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
}

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

  html += `</tbody></table>`;

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
          <tr>
            <td class="${m.unlisted ? 'text-secondary' : ''}">
              <span class="model-id">${esc(m.id)}</span>
            </td>
            <td></td>
            <td><span class="status status--${m.state}">${esc(m.state)}</span></td>
          </tr>
        `;
      }
      html += `</tbody></table>`;
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
}

function modelRow(model) {
  const displayName = model.name || model.id;
  const canUnload = model.state === 'ready';
  const inTransition = model.state === 'starting' || model.state === 'stopping';

  const actionBtn = model.state === 'stopped' || model.state === 'shutdown'
    ? `<button class="btn-secondary btn--sm load-btn" data-model-id="${esc(model.id)}">Load</button>`
    : `<button class="btn-secondary btn--sm unload-btn" data-model-id="${esc(model.id)}"${!canUnload ? ' disabled' : ''}>${inTransition ? '…' : 'Unload'}</button>`;

  const rowClass = ['row--' + (model.state || 'unknown'), model.unlisted ? 'row--unlisted' : ''].filter(Boolean).join(' ');

  return `
    <tr class="${rowClass}">
      <td>
        <div class="model-info">
          <span class="model-id">${esc(displayName)}</span>
          ${model.description ? `<p class="model-desc"><em>${esc(model.description)}</em></p>` : ''}
          ${model.aliases && model.aliases.length > 0 ? `<p class="model-aliases">Aliases: ${model.aliases.map(esc).join(', ')}</p>` : ''}
        </div>
      </td>
      <td>${actionBtn}</td>
      <td><span class="status status--${model.state}">${esc(model.state)}</span></td>
    </tr>
  `;
}

async function loadModel(modelId) {
  // Disable the button immediately so the UI doesn't feel frozen.
  const btn = document.querySelector(`.load-btn[data-model-id="${modelId}"]`);
  if (btn) { btn.disabled = true; btn.textContent = '…'; }
  // Fire-and-forget — the backend returns 202 immediately while llama-swap
  // loads the model in the background.  Polling will pick up state changes.
  fetch(`/api/llamaswap/models/load/${encodeURIComponent(modelId)}`, { method: 'POST' })
    .catch(e => showErr('presets-error', 'Failed to load model: ' + e.message));
  // Trigger a refresh after a short delay to show the "starting" state quickly.
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
