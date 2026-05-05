import { describe, expect, it } from 'vitest';
import { formatUTC } from './time';

describe('time formatting', () => {
  it('formats UTC run times with timezone text in browsers', () => {
    const formatted = formatUTC('2026-05-05T06:00:00Z');
    expect(formatted).toContain('2026');
    expect(formatted).toMatch(/UTC|GMT/);
  });

  it('returns invalid values unchanged', () => {
    expect(formatUTC('not-a-date')).toBe('not-a-date');
  });
});
