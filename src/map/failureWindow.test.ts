import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { activateRendererAttempt, decideRendererFailure, detachRenderer, recordFailure, withCurrentRenderer } from './failureWindow';

describe('sustained map failure window', () => {
  it('does not treat one parallel failure burst as sustained', () => {
    let failures: number[] = [];
    for (const time of [1_000, 1_010, 1_020, 1_030, 1_040]) {
      const result = recordFailure(failures, time);
      failures = result.failures;
      expect(result.sustained).toBe(false);
    }
  });

  it('triggers after five failures spanning at least 1.5 seconds', () => {
    let result = recordFailure([], 1_000);
    for (const time of [1_100, 1_200, 1_300, 2_500]) result = recordFailure(result.failures, time);
    expect(result.sustained).toBe(true);
  });

  it('discards failures outside the bounded window', () => {
    const result = recordFailure([0, 100, 200, 300], 6_000);
    expect(result).toEqual({ failures: [6_000], sustained: false });
  });

  it('retries one context-loss incident once despite duplicate callbacks', () => {
    let state = { activeAttempt: 0, retryUsed: false, retryPending: false };
    let decision = decideRendererFailure(state, 0, 'context');
    state = decision.state;
    expect(decision.action).toBe('retry');
    decision = decideRendererFailure(state, 0, 'context');
    expect(decision.action).toBe('ignore');

    state = activateRendererAttempt(state, 1);
    decision = decideRendererFailure(state, 1, 'context');
    expect(decision.action).toBe('fallback');
    expect(decideRendererFailure(state, 0, 'context').action).toBe('ignore');
  });

  it('keeps the active MapLibre failure callback stable across equivalent parent renders', () => {
    const source = readFileSync(new URL('./WeatherMap.tsx', import.meta.url), 'utf8');
    expect(source).toContain('const handleActiveFailure = useCallback((reason: MapFailureReason) => {\n    handleFailure(reason, rendererAttempt);\n  }, [handleFailure, rendererAttempt]);');
    expect(source).toContain('onFailure={handleActiveFailure}');
    expect(source).not.toContain('onFailure={(reason) => handleFailure(reason, rendererAttempt)}');
    expect(source).toContain('withCurrentRenderer(mapRef.current, activeMap, removeOverlay);');
    expect(source).toContain('renderWorldCopies: false,');
  });

  it('does not touch a renderer after retry teardown has made it stale', () => {
    const destroyed = { getLayer: () => { throw new TypeError("Cannot read properties of null (reading 'getLayer')"); } };
    expect(() => withCurrentRenderer(null, destroyed, (renderer) => renderer.getLayer())).not.toThrow();
    expect(withCurrentRenderer(null, destroyed, (renderer) => renderer.getLayer())).toBe(false);

    let called = false;
    expect(withCurrentRenderer(destroyed, destroyed, () => { called = true; })).toBe(true);
    expect(called).toBe(true);

    const replacement = {};
    expect(detachRenderer(destroyed, destroyed)).toBeNull();
    expect(detachRenderer(replacement, destroyed)).toBe(replacement);
  });
});
