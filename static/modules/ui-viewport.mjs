const PHONE_MEDIA_QUERY = '(max-width: 768px) and (pointer: coarse)';

export function isPhoneViewport(matchMediaFn) {
  const mm = matchMediaFn || (q => window.matchMedia(q));
  return !!mm(PHONE_MEDIA_QUERY).matches;
}

export function readChromeOffsets(getVarFn) {
  const getVar = getVarFn || (name => getComputedStyle(document.documentElement).getPropertyValue(name));
  const top = parseInt(getVar('--top-chrome-height'), 10) || 0;
  const bottom = parseInt(getVar('--bottom-chrome-height'), 10) || 0;
  return { top, bottom };
}

export function computeMaximizedBounds(innerWidth, innerHeight, offsets, minHeight) {
  const top = offsets?.top || 0;
  const bottom = offsets?.bottom || 0;
  const floor = typeof minHeight === 'number' ? minHeight : 0;
  const height = Math.max(floor, innerHeight - top - bottom);
  return { left: 0, top, width: innerWidth, height };
}
