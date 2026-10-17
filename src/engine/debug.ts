// Written by AI on 2026.10.17 by model gpt-6.1-sol. Isolated engine proof page.
import '@fontsource/barlow/400.css';
import '@fontsource/barlow/500.css';
import './debug.css';
import { createWeatherMap } from '../map';
import { bands } from '../palette';
import { createWeatherEngine, DEFAULT_TASTE, SLIDERS, type Period, type Month, type Taste } from './index';
import { WeatherData } from './data';
import { pointContributions } from './sampling';
import { SHIFT_MINIMUM, SHIFT_STEP } from './shaders';

const element = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const parameters = new URLSearchParams(location.search);
const dataUrl = parameters.get('data') ?? '/data/v6';
element('source').textContent = dataUrl.includes('synthetic') ? 'Synthetic days · real land grid' : 'Published v6 data';
const periods: { label: string; period: Period }[] = [
  { label: 'Year', period: { kind: 'year' } },
  ...['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'].map((label, index) => ({ label, period: { kind: 'month' as const, month: (index + 1) as Month } })),
  ...(['Dec-Feb', 'Mar-May', 'Jun-Aug', 'Sep-Nov'] as const).map(season => ({ label: season, period: { kind: 'season' as const, season } })),
  { label: '14-day forecast', period: { kind: 'forecast' } },
  ...Array.from({ length: 14 }, (_, day) => ({ label: `Forecast day ${day}`, period: { kind: 'forecast-day' as const, day } })),
];
const picker = element<HTMLSelectElement>('period');
for (const [index, entry] of periods.entries()) picker.add(new Option(entry.label, String(index)));
picker.value = '11';
let taste: Taste = { ...DEFAULT_TASTE };
let selectedPoint = { latitude: 36.72, longitude: -4.42 };
const { map } = await createWeatherMap(element('map'), bands, null);
const engine = createWeatherEngine(map, { dataUrl, onRedraw: timing => {
  element('timing').textContent = `${timing.milliseconds.toFixed(1)} ms · completed GPU frame`;
  showVerdict();
} });
map.addLayer(engine.layer, 'sea');

function showVerdict(): void {
  const verdict = engine.judgePoint(selectedPoint.latitude, selectedPoint.longitude);
  element('verdict').textContent = verdict
    ? `${selectedPoint.latitude.toFixed(2)}°, ${selectedPoint.longitude.toFixed(2)}°\n${verdict.band} · ${verdict.meanScore.toFixed(1)} / 100\n${(verdict.niceDayShare * 100).toFixed(0)}% nice days · ${verdict.sampleDays} samples\nWarmth ${verdict.parts.warmth.toFixed(1)} · walk home ${verdict.parts.evening.toFixed(1)}\nRain ×${verdict.parts.rainFactor.toFixed(2)} · wind ×${verdict.parts.windFactor.toFixed(2)}\nHumidity ×${verdict.parts.humidityFactor.toFixed(2)} · sun ×${verdict.parts.sunFactor.toFixed(2)}`
    : 'No land data here.';
}
for (const slider of SLIDERS) {
  const label = document.createElement('label');
  const title = document.createElement('span');
  const value = document.createElement('output');
  const unit = document.createElement('small');
  const input = document.createElement('input');
  title.textContent = slider.label;
  unit.textContent = slider.unit;
  value.textContent = String(slider.default);
  input.type = 'range'; input.min = String(slider.min); input.max = String(slider.max); input.step = String(slider.step); input.value = String(slider.default); input.id = slider.id;
  input.addEventListener('input', () => {
    value.textContent = input.value;
    taste = { ...taste, ...slider.toTaste(Number(input.value)) };
    engine.setTaste(taste).catch(showError);
  });
  label.append(title, value, unit, input);
  element('sliders').append(label);
}
function showError(error: unknown): void { element('status').textContent = String(error); }
async function setPeriod(period: Period): Promise<void> {
  const pickerIndex = periods.findIndex(entry => JSON.stringify(entry.period) === JSON.stringify(period));
  if (pickerIndex >= 0) picker.value = String(pickerIndex);
  element('status').textContent = 'Loading this period…';
  try { await engine.setPeriod(period); element('status').textContent = ''; showVerdict(); }
  catch (error) { showError(error); throw error; }
}
picker.addEventListener('change', () => { setPeriod(periods[Number(picker.value)].period).catch(() => {}); });
map.on('click', event => { selectedPoint = { latitude: event.lngLat.lat, longitude: event.lngLat.lng }; showVerdict(); });
async function measure(count = 12): Promise<number[]> {
  const results: number[] = [];
  for (let index = 0; index < count; index++) results.push((await engine.setTaste({ ...taste, warmShift: -3 + index % 12 * 0.5 })).milliseconds);
  await engine.setTaste(taste);
  const sorted = [...results].sort((a, b) => a - b);
  element('status').textContent = `${count} redraws: median ${sorted[Math.floor(count / 2)].toFixed(1)} ms · worst ${sorted[count - 1].toFixed(1)} ms`;
  return results;
}
element('measure').addEventListener('click', () => { measure().catch(showError); });

/** Debug-only source trace: reference curves can independently evaluate each weighted day. */
async function referenceSample(latitude: number, longitude: number, period: Period) {
  const source = new WeatherData(dataUrl);
  const [cells, data] = await Promise.all([source.cells, source.load(period)]);
  return pointContributions(latitude, longitude, cells, data)?.flatMap(contribution => {
    const group = data.groups[contribution.group];
    return Array.from({ length: group.dayCount }, (_, index) => {
      const start = (contribution.cell * group.records.samples + group.firstDay + index) * 15;
      return { record: Array.from(group.records.bytes.subarray(start, start + 15)), offset: SHIFT_MINIMUM + contribution.level * SHIFT_STEP, weight: contribution.weight / group.dayCount };
    });
  }) ?? null;
}
Object.assign(window, { weatherDebug: { engine, map, setPeriod, measure, referenceSample, getTaste: () => taste } });
await setPeriod(periods[Number(picker.value)].period).catch(() => {});
Object.assign(window, { weatherDebugReady: true });
