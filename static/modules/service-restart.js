// Service restart handlers

import { setStatusBar } from './status-bar.js';
import { pollStatus } from './status-polling.js';

export let llamaServiceLabel = 'llama-server';

export function setLlamaServiceLabel(label) {
  llamaServiceLabel = label;
}

export async function restartService() {
  document.getElementById('status-menu').classList.remove('open');
  if (!confirm(`Restart ${llamaServiceLabel} service?\n\nThe server will be briefly unavailable.`)) return;
  const btn = document.getElementById('restart-btn');
  btn.disabled = true;
  setStatusBar('Restart', `Restarting ${llamaServiceLabel}…`, true);
  try {
    const resp = await fetch('/api/restart', { method: 'POST' });
    if (!resp.ok) throw new Error(await resp.text());
    setStatusBar('Ready', 'Service restarted successfully', false);
    pollStatus();
  } catch (e) {
    setStatusBar('Error', 'Restart failed: ' + e.message, false);
  } finally {
    btn.disabled = false;
  }
}

export async function restartSelf() {
  document.getElementById('status-menu').classList.remove('open');
  if (!confirm('Restart w84ggufman?\n\nThe page will reload automatically once it comes back up.')) return;
  const btn = document.getElementById('restart-self-btn');
  btn.disabled = true;
  setStatusBar('Restart', 'Restarting w84ggufman…', true);
  try {
    await fetch('/api/restart-self', { method: 'POST' });
  } catch (_) {}
  await new Promise(r => setTimeout(r, 1200));
  for (let i = 0; i < 30; i++) {
    try {
      const r = await fetch('/api/status');
      if (r.ok) { location.reload(); return; }
    } catch (_) {}
    await new Promise(r => setTimeout(r, 1000));
  }
  setStatusBar('Error', 'w84ggufman did not come back — check the service logs', false);
  btn.disabled = false;
}

export function setupRestartButtons() {
  document.getElementById('restart-btn').addEventListener('click', restartService);
  document.getElementById('restart-self-btn').addEventListener('click', restartSelf);
}
