// Status bar and log resize handling

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

export function setupStatusBar() {
  document.getElementById('status-bar-main').addEventListener('click', toggleStatusBar);

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
