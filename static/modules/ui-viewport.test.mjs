import test from 'node:test';
import assert from 'node:assert/strict';

import {
  isPhoneViewport,
  readChromeOffsets,
  computeMaximizedBounds,
} from './ui-viewport.mjs';

test('isPhoneViewport uses phone media query result', () => {
  const yes = isPhoneViewport(() => ({ matches: true }));
  const no = isPhoneViewport(() => ({ matches: false }));
  assert.equal(yes, true);
  assert.equal(no, false);
});

test('readChromeOffsets parses integer css variables with fallback', () => {
  const offsets = readChromeOffsets((name) => {
    if (name === '--top-chrome-height') return '72px';
    if (name === '--bottom-chrome-height') return '34px';
    return '';
  });
  assert.deepEqual(offsets, { top: 72, bottom: 34 });

  const fallback = readChromeOffsets(() => '');
  assert.deepEqual(fallback, { top: 0, bottom: 0 });
});

test('computeMaximizedBounds keeps dialog between top and bottom chrome', () => {
  const bounds = computeMaximizedBounds(390, 844, { top: 88, bottom: 36 }, 220);
  assert.deepEqual(bounds, { left: 0, top: 88, width: 390, height: 720 });
});

test('computeMaximizedBounds respects minimum height floor', () => {
  const bounds = computeMaximizedBounds(320, 300, { top: 100, bottom: 250 }, 180);
  assert.deepEqual(bounds, { left: 0, top: 100, width: 320, height: 180 });
});
