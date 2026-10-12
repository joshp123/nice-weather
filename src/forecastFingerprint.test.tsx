import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { ForecastFingerprint } from './App';
import type { ForecastDayScore } from './data/types';
import { WEATHER_PRESENTATION } from './weatherPresentation';

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
    expect(markup).toContain('--day-colour:#F2D55E');
    expect(markup).toContain('--day-colour:#426580');
  });
});

const legend = WEATHER_PRESENTATION.legend;

function rating(score: number): string {
  if (score >= 85) return 'Excellent';
  if (score >= 70) return 'Very good';
  if (score >= 55) return 'Good';
  if (score >= 40) return 'Mixed';
  if (score >= 25) return 'Poor';
  return 'Awful';
}
