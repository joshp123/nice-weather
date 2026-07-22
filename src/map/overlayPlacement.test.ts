import { describe, expect, it } from 'vitest';
import { baseOverlayBefore, scoreOverlayBefore } from './overlayPlacement';

describe('weather overlay placement', () => {
  it('keeps weather below administrative boundaries and labels', () => {
    const layers = [{ id: 'background', type: 'background' }, { id: 'admin-boundary', type: 'line' }, { id: 'place-label', type: 'symbol' }];
    expect(baseOverlayBefore(layers)).toBe('admin-boundary');
    expect(scoreOverlayBefore(layers)).toBe('admin-boundary');
  });

  it('uses labels when a style has no boundary layer', () => {
    const layers = [{ id: 'background', type: 'background' }, { id: 'place-label', type: 'symbol' }];
    expect(baseOverlayBefore(layers)).toBe('place-label');
    expect(scoreOverlayBefore(layers)).toBe('place-label');
  });
});
