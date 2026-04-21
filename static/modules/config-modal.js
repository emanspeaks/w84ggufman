// Config modal dialogs

import { esc, setStatusBar } from './status-bar.js';
import { fetchLocalModels } from './local-models.js';

export async function openRawEditModal({ title, subtitle, endpoint, placeholder, successMsg }) {
  let body = '';
  try {
    const resp = await fetch(endpoint);
    if (resp.ok) body = await resp.text();
  } catch (_) {}

  const backdrop = document.createElement('div');
  backdrop.className = 'modal-backdrop';
  backdrop.innerHTML = `
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="modal-title">
      <div class="modal-title" id="modal-title">
        ${esc(title)}${subtitle ? ' <small>' + esc(subtitle) + '</small>' : ''}
      </div>
      <textarea spellcheck="false" placeholder="${esc(placeholder)}">${esc(body)}</textarea>
      <div class="modal-actions">
        <button class="btn-secondary" id="modal-cancel">Cancel</button>
        <button class="btn-primary" id="modal-save">Save</button>
      </div>
    </div>
  `;

  const ta = backdrop.querySelector('textarea');

  function closeModal() {
    document.body.removeChild(backdrop);
  }

  backdrop.querySelector('#modal-cancel').addEventListener('click', closeModal);

  backdrop.querySelector('#modal-save').addEventListener('click', async () => {
    const saveBtn = backdrop.querySelector('#modal-save');
    saveBtn.disabled = true;
    saveBtn.textContent = 'Saving…';
    try {
      const resp = await fetch(endpoint, {
        method: 'PUT',
        headers: { 'Content-Type': 'text/plain' },
        body: ta.value,
      });
      if (!resp.ok) throw new Error(await resp.text());
      closeModal();
      setStatusBar('Ready', successMsg, false);
      fetchLocalModels();
    } catch (e) {
      setStatusBar('Error', 'Save failed: ' + e.message, false);
      saveBtn.disabled = false;
      saveBtn.textContent = 'Save';
    }
  });

  document.body.appendChild(backdrop);
  ta.focus();
  ta.setSelectionRange(0, 0);
}

export async function openTemplatesModal() {
  document.getElementById('status-menu').classList.remove('open');
  let tpl = { llmCmd: '', llmTtl: -1, sdCmd: '', sdTtl: 600, sdCheckEndpoint: '${sd-check-endpoint}' };
  try {
    const resp = await fetch('/api/llamaswap/templates');
    if (resp.ok) tpl = await resp.json();
  } catch (_) {}

  const backdrop = document.createElement('div');
  backdrop.className = 'modal-backdrop';
  backdrop.innerHTML = `
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="modal-title">
      <div class="modal-title" id="modal-title">Edit Command Templates
        <small>Applied when new models are added to config.yaml</small>
      </div>
      <div class="modal-body">
        <div class="tpl-section">
          <div class="tpl-section-title">LLM models</div>
          <textarea class="tpl-textarea" id="tpl-llm-cmd" spellcheck="false">${esc(tpl.llmCmd)}</textarea>
          <div class="tpl-ph-hint">Placeholders: <code>{{MODEL_PATH}}</code> &nbsp;<code>{{MODEL_NAME}}</code> &nbsp;<code>{{MMPROJ_LINE}}</code> (omitted if no mmproj) &nbsp;<code>${'${PORT}'}</code> (llama-swap runtime)</div>
          <div class="tpl-ttl-row">
            <label for="tpl-llm-ttl">Default TTL (s):</label>
            <input type="number" id="tpl-llm-ttl" value="${tpl.llmTtl}" min="-1">
            <span class="tpl-ttl-hint">−1 = auto (600 s for &lt;10 B params, 0 otherwise)</span>
          </div>
        </div>
        <div class="tpl-section">
          <div class="tpl-section-title">SD / Flux models</div>
          <textarea class="tpl-textarea" id="tpl-sd-cmd" spellcheck="false">${esc(tpl.sdCmd)}</textarea>
          <div class="tpl-ph-hint">Placeholders: <code>{{MODEL_PATH}}</code> &nbsp;<code>{{VAE_LINE}}</code> (omitted if no VAE) &nbsp;<code>${'${PORT}'}</code> (llama-swap runtime)</div>
          <div class="tpl-ttl-row">
            <label for="tpl-sd-ttl">Default TTL (s):</label>
            <input type="number" id="tpl-sd-ttl" value="${tpl.sdTtl}" min="-1">
          </div>
          <div class="tpl-ttl-row">
            <label for="tpl-sd-check">checkEndpoint:</label>
            <input type="text" id="tpl-sd-check" value="${esc(tpl.sdCheckEndpoint)}" style="flex:1;padding:4px 8px;background:#0f172a;border:1px solid #334155;border-radius:5px;color:#f1f5f9;font-size:0.825rem;outline:none;font-family:inherit" spellcheck="false">
            <span class="tpl-ttl-hint">macro or literal path (e.g. <code style="font-size:0.7rem">${'${sd-check-endpoint}'}</code>)</span>
          </div>
        </div>
      </div>
      <div class="modal-actions">
        <button class="btn-secondary" id="modal-cancel">Cancel</button>
        <button class="btn-primary" id="modal-save">Save</button>
      </div>
    </div>
  `;

  function closeModal() {
    document.body.removeChild(backdrop);
  }

  backdrop.querySelector('#modal-cancel').addEventListener('click', closeModal);

  backdrop.querySelector('#modal-save').addEventListener('click', async () => {
    const saveBtn = backdrop.querySelector('#modal-save');
    saveBtn.disabled = true;
    saveBtn.textContent = 'Saving…';
    const payload = {
      llmCmd:          backdrop.querySelector('#tpl-llm-cmd').value,
      llmTtl:          parseInt(backdrop.querySelector('#tpl-llm-ttl').value, 10) || 0,
      sdCmd:           backdrop.querySelector('#tpl-sd-cmd').value,
      sdTtl:           parseInt(backdrop.querySelector('#tpl-sd-ttl').value, 10) || 0,
      sdCheckEndpoint: backdrop.querySelector('#tpl-sd-check').value,
    };
    try {
      const resp = await fetch('/api/llamaswap/templates', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      if (!resp.ok) throw new Error(await resp.text());
      closeModal();
      setStatusBar('Ready', 'Command templates saved', false);
    } catch (e) {
      setStatusBar('Error', 'Save failed: ' + e.message, false);
      saveBtn.disabled = false;
      saveBtn.textContent = 'Save';
    }
  });

  document.body.appendChild(backdrop);
  backdrop.querySelector('#tpl-llm-cmd').focus();
  backdrop.querySelector('#tpl-llm-cmd').setSelectionRange(0, 0);
}

export async function openFullConfigModal(llamaSwapEnabled) {
  document.getElementById('status-menu').classList.remove('open');
  const isSwap = llamaSwapEnabled;
  const endpoint = isSwap ? '/api/llamaswap/config' : '/api/preset/config';
  const filename = isSwap ? 'config.yaml' : 'models.ini';
  await openRawEditModal({
    title: 'Edit ' + filename, subtitle: null,
    endpoint, placeholder: '',
    successMsg: filename + ' saved',
  });
}
