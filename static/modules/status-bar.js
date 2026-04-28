// Status bar and log resize handling

import { formatBytes } from './utils.js';

export function appendLogLine(line) {
  const log = document.getElementById('status-log');
  const atBottom = log.scrollHeight - log.scrollTop <= log.clientHeight + 4;
  const span = document.createElement('span');
  span.className = 'log-line';
  span.textContent = line;
  log.appendChild(span);
  if (atBottom) log.scrollTop = log.scrollHeight;
}

export function setStatusBar(label, text, active) {
  const bar = document.getElementById('status-bar');
  document.getElementById('status-bar-label').textContent = label;
  document.getElementById('status-bar-text').textContent = text;
  bar.classList.toggle('active', active);
  if (text) appendLogLine(text);
}

export function toggleStatusBar() {
  const bar = document.getElementById('status-bar');
  bar.classList.toggle('expanded');
  if (bar.classList.contains('expanded')) {
    const log = document.getElementById('status-log');
    log.scrollTop = log.scrollHeight;
  }
}

export function renderQueuePanel(entries) {
  const panel = document.getElementById('queue-panel');
  if (!panel) return;
  if (!entries || entries.length === 0) {
    panel.style.display = 'none';
    panel.innerHTML = '';
    return;
  }
  panel.style.display = 'block';
  panel.innerHTML = '';
  const header = document.createElement('div');
  header.className = 'queue-header';
  const activeCount = entries.filter(e => e.state === 'active').length;
  const queuedCount = entries.length - activeCount;
  header.textContent = `Downloads - ${entries.length} total (${activeCount} active, ${queuedCount} queued)`;
  panel.appendChild(header);
  entries.forEach(entry => {
    const row = document.createElement('div');
    row.className = 'queue-item';

    const top = document.createElement('div');
    top.className = 'queue-item-top';

    const label = document.createElement('span');
    label.className = 'queue-item-label';
    label.textContent = entry.label;
    top.appendChild(label);

    const state = document.createElement('span');
    state.className = 'queue-item-state ' + (entry.state === 'active' ? 'active' : 'queued');
    state.textContent = entry.state === 'active' ? 'active' : 'queued';
    top.appendChild(state);

    if (entry.totalBytes > 0) {
      const size = document.createElement('span');
      size.className = 'queue-item-size';
      size.textContent = formatBytes(entry.totalBytes);
      top.appendChild(size);
    }

    if (entry.state !== 'active') {
      const removeBtn = document.createElement('button');
      removeBtn.className = 'queue-item-remove';
      removeBtn.textContent = 'x';
      removeBtn.title = 'Remove from queue';
      removeBtn.addEventListener('click', (e) => {
        e.stopPropagation();
        fetch(`/api/queue/${entry.id}`, { method: 'DELETE' });
      });
      top.appendChild(removeBtn);
    }

    const progress = document.createElement('div');
    progress.className = 'queue-item-progress';
    const fill = document.createElement('div');
    fill.className = 'queue-item-progress-fill';

    let pct = entry.pct;
    if (typeof pct !== 'number' || pct < 0) {
      if (entry.totalBytes > 0 && entry.dlBytes > 0) {
        pct = Math.round((entry.dlBytes / entry.totalBytes) * 100);
      } else {
        pct = 0;
      }
    }
    if (pct > 100) pct = 100;
    if (pct < 0) pct = 0;
    fill.style.width = pct + '%';
    progress.appendChild(fill);

    const detail = document.createElement('div');
    detail.className = 'queue-item-detail';
    if (entry.totalBytes > 0) {
      const done = entry.state === 'active' && entry.dlBytes > 0 ? Math.min(entry.dlBytes, entry.totalBytes) : 0;
      detail.textContent = `${pct}% (${formatBytes(done)} / ${formatBytes(entry.totalBytes)})`;
    } else {
      detail.textContent = `${pct}%`;
    }

    row.appendChild(top);
    row.appendChild(progress);
    row.appendChild(detail);
    panel.appendChild(row);
  });
}

export function setupStatusBar() {
  document.getElementById('status-bar-main').addEventListener('click', toggleStatusBar);

  // Give the log scroll focus whenever the user interacts with the log area so
  // the mouse wheel scrolls the log rather than the main page.
  const log = document.getElementById('status-log');
  const logWrapper = document.getElementById('status-log-wrapper');
  logWrapper.addEventListener('mouseenter', () => log.focus({ preventScroll: true }));
  logWrapper.addEventListener('mousedown', () => log.focus({ preventScroll: true }));

  // Log resize handler
  const handle = document.getElementById('status-resize-handle');
  let startY = 0, startHeight = 200;

  handle.addEventListener('mousedown', (e) => {
    e.preventDefault();
    startY = e.clientY;
    startHeight = parseInt(getComputedStyle(document.documentElement)
      .getPropertyValue('--log-height'), 10) || 200;
    handle.classList.add('dragging');

    function onMove(e) {
      const height = Math.max(80, Math.min(600, startHeight + (startY - e.clientY)));
      document.documentElement.style.setProperty('--log-height', height + 'px');
    }
    function onUp() {
      handle.classList.remove('dragging');
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
    }
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  });

  // Bar resize observer
  const bar = document.getElementById('status-bar');
  new ResizeObserver(() => {
    document.body.style.paddingBottom = bar.offsetHeight + 'px';
  }).observe(bar);
}
