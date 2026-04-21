// README display

import { esc } from './utils.js';

export async function fetchAndRenderReadme(repoId) {
  const content = document.getElementById('readme-content');
  const label = document.getElementById('readme-repo-label');
  label.textContent = repoId;
  content.innerHTML = '<p class="msg-loading">Loading…</p>';
  try {
    const resp = await fetch('/api/readme?id=' + encodeURIComponent(repoId));
    if (resp.status === 404) {
      content.innerHTML = '<p class="msg-empty">No README found for this repo.</p>';
      return;
    }
    if (!resp.ok) throw new Error(await resp.text());
    content.innerHTML = await resp.text();
  } catch (e) {
    content.innerHTML = `<p class="msg-error">Failed to load README: ${esc(e.message)}</p>`;
  }
}
