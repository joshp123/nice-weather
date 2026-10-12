import { describe, expect, it } from 'vitest';
import { scoreOverlayBefore } from './overlayPlacement';

describe('weather overlay placement', () => {
  it('keeps weather below administrative boundaries and labels', () => {
    const layers = [{ id: 'background', type: 'background' }, { id: 'admin-boundary', type: 'line' }, { id: 'place-label', type: 'symbol' }];
    expect(scoreOverlayBefore(layers)).toBe('admin-boundary');
  });

  it('uses labels when a style has no boundary layer', () => {
    const layers = [{ id: 'background', type: 'background' }, { id: 'place-label', type: 'symbol' }];
    expect(scoreOverlayBefore(layers)).toBe('place-label');
  });
});
