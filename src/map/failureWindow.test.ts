import { describe, expect, it } from 'vitest';
import { recordFailure } from './failureWindow';

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
});
