let topDialogZ = 1100;

function parseZ(value) {
  const n = Number.parseInt(value, 10);
  return Number.isFinite(n) ? n : 0;
}

export function bringDialogToFront(dialogEl) {
  if (!dialogEl || !dialogEl.isConnected) return 0;
  const current = parseZ(dialogEl.style.zIndex);
  if (current > topDialogZ) topDialogZ = current;
  topDialogZ += 1;
  dialogEl.style.zIndex = String(topDialogZ);
  return topDialogZ;
}

export function registerFloatingDialog(dialogEl) {
  if (!dialogEl) return () => {};
  const onActivate = () => {
    bringDialogToFront(dialogEl);
  };

  dialogEl.addEventListener('mousedown', onActivate, true);
  dialogEl.addEventListener('focusin', onActivate, true);
  dialogEl.addEventListener('touchstart', onActivate, { capture: true, passive: true });
  bringDialogToFront(dialogEl);

  return () => {
    dialogEl.removeEventListener('mousedown', onActivate, true);
    dialogEl.removeEventListener('focusin', onActivate, true);
    dialogEl.removeEventListener('touchstart', onActivate, true);
  };
}
