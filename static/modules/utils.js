// Utility functions: HTML escaping, formatting

export function esc(str) {
  return String(str)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;')
    .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

export function formatBytes(n) {
  if (n == null) return 'unknown';
  if (n < 1024) return n + ' B';
  if (n < 1048576) return (n / 1024).toFixed(1) + ' KiB';
  if (n < 1073741824) return (n / 1048576).toFixed(1) + ' MiB';
  return (n / 1073741824).toFixed(2) + ' GiB';
}

export function formatETA(secs) {
  if (secs < 60) return secs + 's';
  const m = Math.floor(secs / 60), s = secs % 60;
  return m + 'm ' + (s < 10 ? '0' : '') + s + 's';
}

export function showErr(id, msg) {
  const el = document.getElementById(id);
  el.textContent = msg;
  el.style.display = msg ? 'block' : 'none';
}

export function clearErr(id) { showErr(id, ''); }

export async function copyTextToClipboard(text) {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch (_) {}
  }
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.setAttribute('readonly', '');
  ta.style.cssText = 'position:fixed;top:0;left:0;width:1px;height:1px;opacity:0;';
  document.body.appendChild(ta);
  const prevActive = document.activeElement;
  ta.focus();
  ta.select();
  let ok = false;
  try { ok = document.execCommand('copy'); } catch (_) { ok = false; }
  document.body.removeChild(ta);
  if (prevActive && typeof prevActive.focus === 'function') prevActive.focus();
  return ok;
}

export function joinPath(base, rel) {
  const b = String(base || '');
  const r = String(rel || '');
  if (!b) return r;
  if (!r) return b;
  const sep = b.includes('\\') ? '\\' : '/';
  const baseNorm = b.replace(/[\\/]+$/, '');
  let relNorm = r.replace(/[\\/]+/g, sep);
  while (relNorm.startsWith(sep)) relNorm = relNorm.slice(1);
  return relNorm ? (baseNorm + sep + relNorm) : baseNorm;
}
