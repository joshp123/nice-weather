import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { ForecastFingerprint } from './App';
import type { ForecastDayScore, WeatherManifest } from './data/types';

describe('forecast fingerprint', () => {
  it('renders all 14 ordered dates with their canonical score and rating', () => {
    const days: ForecastDayScore[] = Array.from({ length: 14 }, (_, index) => {
      const date = new Date('2026-07-18T00:00:00Z');
      date.setUTCDate(date.getUTCDate() + index);
      const score = 91 - index * 5;
      return { date: date.toISOString().slice(0, 10), score, category: rating(score) };
    });
    const markup = renderToStaticMarkup(<ForecastFingerprint days={days} legend={legend} />);

    expect(markup.match(/<li/g)).toHaveLength(14);
    expect(markup.indexOf('dateTime="2026-07-18"')).toBeLessThan(markup.indexOf('dateTime="2026-07-31"'));
    expect(markup).toContain('aria-label="Sat 18, 91, Excellent"');
    expect(markup).toContain('aria-label="Fri 31, 26, Poor"');
    expect(markup).toContain('--day-colour:#f06031');
    expect(markup).toContain('--day-colour:#5e6382');
  });
});

const legend: WeatherManifest['legend'] = [
  { label: 'Awful', color: '#343b53', min: 0, max: 24 },
  { label: 'Poor', color: '#5e6382', min: 25, max: 39 },
  { label: 'Mixed', color: '#97949c', min: 40, max: 54 },
  { label: 'Good', color: '#c9a47f', min: 55, max: 69 },
  { label: 'Very good', color: '#e18952', min: 70, max: 84 },
  { label: 'Excellent', color: '#f06031', min: 85, max: 100 },
];

function rating(score: number): string {
  if (score >= 85) return 'Excellent';
  if (score >= 70) return 'Very good';
  if (score >= 55) return 'Good';
  if (score >= 40) return 'Mixed';
  if (score >= 25) return 'Poor';
  return 'Awful';
}
