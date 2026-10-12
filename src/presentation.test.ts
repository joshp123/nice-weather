import { describe, expect, it } from 'vitest';
import { pointSourceLabel } from './presentation';

describe('point detail presentation', () => {
  it('shows source fidelity without a second, conflicting freshness clock', () => {
    const label = pointSourceLabel({
      basis: 'forecast-window',
      detailLabel: '14-day forecast',
      resolutionKm: 28,
      artifactGeneratedAt: '2026-07-19T11:08:00Z',
    });
    expect(label).toBe('14-day forecast · about 28 km');
    expect(label).not.toMatch(/built|updated|ago/i);
  });

  it('keeps forecast provenance while omitting artifact build time', () => {
    const common = {
      detailLabel: 'Detailed forecast', providerLabel: 'ICON-EU', resolutionKm: 7,
      temporalResolutionHours: 1, artifactGeneratedAt: '2026-07-19T11:08:00Z',
    };
    expect(pointSourceLabel({ basis: 'forecast', ...common })).toBe('Detailed forecast · ICON-EU · about 7 km · hourly detail');
  });
});
