// Written by AI on 2026.10.16 by model claude opus 5.5.
// Plain words for what the numbers mean. Descriptions only: the scores come from Sites.
import type { DayWeather } from './api';
import type { Profile } from './state';
import { NICE_DAY_MINIMUM_SCORE } from './palette';

export type Factor = Readonly<{ label: string; score: number; value: string; detail: string; problem: string }>;

const round = (value: number) => Math.round(value);

export function factorsForDay(day: DayWeather, profile: Profile): Factor[] {
  const temperature = `${round(day.temperatureC)} °C`;
  const dewPoint = `dew point ${round(day.dewPointC)} °C`;
  // 22-27 °C is ideal; 16 °C is mediocre, not bad (Josh, April).
  const comfort =
    day.dewPointC >= 24 ? 'Oppressively humid' : day.dewPointC >= 20 ? 'Humid'
      : day.temperatureC < 10 ? 'Cold' : day.temperatureC < 16 ? 'Cool' : day.temperatureC < 20 ? 'Mild'
      : day.temperatureC > 33 ? 'Too hot' : day.temperatureC > 28 ? 'Hot' : 'Warm';
  const sky = day.cloudPercent < 25 ? 'Sunny' : day.cloudPercent < 55 ? 'Some cloud' : day.cloudPercent < 80 ? 'Mostly cloudy' : 'Overcast';
  const rain = day.rainMm.toFixed(day.rainMm < 10 ? 1 : 0);
  return [
    { label: 'Comfort', score: day.scores.comfort, value: temperature, detail: `${comfort}, ${dewPoint}`, problem: `${comfort.toLowerCase()} (${comfort.includes('humid') ? dewPoint : temperature})` },
    { label: 'Dry', score: day.scores.dry, value: `${rain} mm`, detail: day.rainMm < 0.2 ? 'No rain' : `Rain for ${round(day.wetPercent)}% of the day`, problem: `wet (${rain} mm of rain)` },
    { label: 'Clear sky', score: day.scores.clear, value: `${round(day.cloudPercent)}%`, detail: `${sky}`, problem: `${sky.toLowerCase()} (${round(day.cloudPercent)}% cloud)` },
    { label: 'Calm', score: profile === 'cycling' ? day.scores.calmCycling : day.scores.calmNice, value: `${round(day.windKph)} km/h`, detail: day.gustKph === null ? 'Average daytime wind' : `Gusts to ${round(day.gustKph)} km/h`, problem: `windy (${round(day.windKph)} km/h)` },
  ];
}

/** One short sentence: what spoils the day, or that nothing does. */
export function summaryForDay(day: DayWeather, profile: Profile): string {
  const problems = factorsForDay(day, profile)
    .filter((factor) => factor.score < NICE_DAY_MINIMUM_SCORE)
    .sort((left, right) => left.score - right.score)
    .slice(0, 2)
    .map((factor) => factor.problem);
  if (problems.length === 0) return 'Nothing spoils this day.';
  const sentence = problems.join(' and ');
  return `${sentence[0].toUpperCase()}${sentence.slice(1)}.`;
}
