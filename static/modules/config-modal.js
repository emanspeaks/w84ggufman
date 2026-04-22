// Config modal dialogs

import { setStatusBar } from './status-bar.js';
import { esc } from './utils.js';
import { fetchLocalModels } from './local-models.js';
import { pollStatus } from './status-polling.js';

export async function openRawEditModal({ title, subtitle, endpoint, placeholder, successMsg, selectName, hintHtml }) {
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
      ${hintHtml || ''}
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
      pollStatus();
    } catch (e) {
      setStatusBar('Error', 'Save failed: ' + e.message, false);
      saveBtn.disabled = false;
      saveBtn.textContent = 'Save';
    }
  });

  document.body.appendChild(backdrop);
  ta.focus();
  let cursorPos = 0;
  if (selectName) {
    const re = new RegExp('^\\s{2}' + selectName.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + ':\\s*$', 'm');
    const m = ta.value.match(re);
    if (m) cursorPos = m.index;
  }
  ta.setSelectionRange(cursorPos, cursorPos);
  if (cursorPos > 0) {
    const lineHeight = parseInt(getComputedStyle(ta).lineHeight, 10) || 20;
    const linesBefore = ta.value.slice(0, cursorPos).split('\n').length - 1;
    ta.scrollTop = Math.max(0, linesBefore * lineHeight - ta.clientHeight / 3);
  }
}

export async function openW84ConfigModal() {
  document.getElementById('status-menu').classList.remove('open');
  await openRawEditModal({
    title: 'Edit W84 Config',
    subtitle: '.w84ggufman.yaml',
    endpoint: '/api/llamaswap/w84config',
    placeholder: '',
    successMsg: '.w84ggufman.yaml saved',
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
  await openRawEditModal({
    title: 'Edit ' + filename, subtitle: null,
    endpoint, placeholder: '',
    successMsg: filename + ' saved',
    selectName,
  });
}
