// Local models display and management

import { esc, formatBytes, clearErr, showErr } from './utils.js';

export async function fetchLocalModels() {
  clearErr('local-error');
  try {
    const resp = await fetch('/api/local');
    if (!resp.ok) throw new Error(await resp.text());
    const models = await resp.json();
    renderLocalModels(models);
  } catch (e) {
    showErr('local-error', 'Failed to load local models: ' + e.message);
    document.getElementById('local-list').innerHTML = '';
  }
}

export function renderLocalModels(models) {
  const list = document.getElementById('local-list');
  if (!models.length) {
    list.innerHTML = '<p class="msg-empty">No models found in models directory.</p>';
    return;
  }
  list.innerHTML = '';
  for (const m of models) {
    const card = document.createElement('div');
    card.className = 'model-card clickable';
    card.dataset.aliases = (m.configAliases || []).filter(a => a.loaded).map(a => a.name).join(',');
    card.dataset.inConfig = m.inConfig ? '1' : '0';

    let titleText, titleClass = 'model-name';
    if (m.sourceUnknown) {
      titleText = m.path.split('/').pop() || m.path;
      titleClass = 'model-name model-name-unknown';
    } else if (m.isLocal) {
      titleText = m.repoId || m.path.split('/').pop() || m.path;
    } else if (m.repoId) {
      titleText = m.repoId;
    } else {
      titleText = m.path.split('/').pop() || m.path;
      titleClass = 'model-name model-name-unknown';
    }

    const badges = [];
    if (m.isLocal) badges.push(`<span class="badge badge-local">local</span>`);
    if (m.sourceUnknown) badges.push(`<span class="badge badge-warn-preset">unknown source</span>`);

    const configAliases = m.configAliases || [];
    let loadedHtml;
    if (configAliases.length > 0) {
      loadedHtml = configAliases.map(a => {
        const groups = a.groups || [];
        let cls, label;
        if (groups.length === 0) {
          cls = 'badge-group-none';
          label = esc(a.name);
        } else if (groups.length === 1) {
          cls = 'badge-group-ok';
          label = `${esc(a.name)} [${esc(groups[0])}]`;
        } else {
          cls = 'badge-group-multi';
          label = `${esc(a.name)} [${groups.map(g => esc(g)).join(', ')}]`;
        }
        const title = a.loaded ? 'Currently loaded' : 'Configured but not loaded';
        const activeCls = a.loaded ? ' badge-active' : '';
        return `<span class="badge ${cls}${activeCls}" data-alias="${esc(a.name)}" title="${title}">${label}</span>`;
      }).join(' ');
    } else {
      loadedHtml = '<span class="badge badge-unloaded" title="Not referenced in config files and not currently loaded">Unused</span>';
    }

    card.innerHTML = `
      <div class="model-meta">
        <span class="${titleClass}">${esc(titleText)}${badges.length ? ' ' + badges.join(' ') : ''}</span>
        <span class="model-detail">
          ${m.files.length === 0 && m.inConfig ? 'not downloaded' : esc(formatBytes(m.sizeBytes)) + ' &middot; ' + m.files.length + ' file' + (m.files.length !== 1 ? 's' : '')}
        </span>
        <span class="model-loaded-row">${loadedHtml}</span>
      </div>
      <div class="model-actions">
        <button class="btn-danger delete-btn">Delete</button>
      </div>
    `;

    card.addEventListener('click', (e) => {
      if (e.target.closest('button')) return;
      if (m.repoId && !m.isLocal) {
        // Re-import to avoid circular dep
        import('./repo-browser.js').then(mod => {
          mod.setRepoInput(m.repoId);
          mod.browseRepo();
        });
      } else {
        import('./repo-browser.js').then(mod => {
          mod.browseLocalPath(m.path, m.repoId || m.path.split('/').pop());
        });
      }
    });

    card.querySelector('.delete-btn').addEventListener('click', () => deleteRepo(m.repoId, m.path));
    list.appendChild(card);
  }
}

async function deleteRepo(repoId, path) {
  const label = repoId || path;
  if (!confirm(`Delete "${label}"?\n\nThis will remove all files and cannot be undone.`)) return;
  clearErr('local-error');
  try {
    const resp = await fetch('/api/local?id=' + encodeURIComponent(repoId || path), { method: 'DELETE' });
    if (resp.status === 404) { showErr('local-error', 'Repo not found.'); return; }
    if (!resp.ok) throw new Error(await resp.text());
    import('./status-bar.js').then(mod => mod.setStatusBar('Ready', 'Deleted ' + label, false));
    fetchLocalModels();
  } catch (e) {
    showErr('local-error', 'Delete failed: ' + e.message);
  }
}
