// Written by AI on 2026.10.17 by model GPT-6.1 Sol.
import {
  COLD_GRADE_BY_FEELS_IN_SUN, HEAT_GRADE_BY_FEELS_IN_SHADE, EVENING_GRADE_BY_FEELS_AT_21,
  RAIN_FACTOR_BY_MM, WIND_FACTOR_BY_KMH, HUMIDITY_FACTOR_BY_DEW_POINT, SUN_FACTOR_BY_SUNNY_SHARE,
  DEFAULT_TASTE, readCurve, type Curve, type Taste,
} from '../engine/taste';
import './scoreCurve.css';

type ReferencePoint = Readonly<{ label: string; value: number }>;
export type ScoreAxis = 'warmth' | 'heat' | 'evening' | 'rain' | 'wind' | 'humidity' | 'sunshine';
declare const __CURVE_REFERENCE_POINTS__: Readonly<Record<ScoreAxis, readonly ReferencePoint[]>>;

type Axis = Readonly<{
  title: string; unit: string; domain: readonly [number, number]; ticks: readonly number[];
  factor?: boolean; curve: Curve; input: (value: number, taste: Taste) => number;
}>;
const axes: Record<ScoreAxis, Axis> = {
  warmth: { title: 'Warmth in the sun', unit: '°C feels-like in the sun', domain: [0, 40], ticks: [0, 10, 20, 30, 40], curve: COLD_GRADE_BY_FEELS_IN_SUN, input: (x, taste) => x - taste.warmShift },
  heat: { title: 'Heat in the shade', unit: '°C feels-like in the shade', domain: [15, 55], ticks: [15, 25, 35, 45, 55], curve: HEAT_GRADE_BY_FEELS_IN_SHADE, input: (x, taste) => x + taste.heatShift },
  evening: { title: 'The walk home at 21:00', unit: '°C feels-like at 21:00', domain: [-5, 45], ticks: [-5, 5, 15, 25, 35, 45], curve: EVENING_GRADE_BY_FEELS_AT_21, input: (x, taste) => x - taste.eveningShift },
  rain: { title: 'Rain', unit: 'mm from 10:00 to 21:00', domain: [0, 25], ticks: [0, 5, 10, 15, 20, 25], factor: true, curve: RAIN_FACTOR_BY_MM, input: (x, taste) => x * taste.rainScale },
  wind: { title: 'Wind', unit: 'km/h, mean from 10:00 to 21:00', domain: [0, 100], ticks: [0, 25, 50, 75, 100], factor: true, curve: WIND_FACTOR_BY_KMH, input: (x, taste) => x - taste.windShift },
  humidity: { title: 'Humidity', unit: '°C dew point, mean from 10:00 to 21:00', domain: [0, 36], ticks: [0, 9, 18, 27, 36], factor: true, curve: HUMIDITY_FACTOR_BY_DEW_POINT, input: (x, taste) => x - taste.humidityShift },
  sunshine: { title: 'Sunshine', unit: '% of daylight hours from 10:00', domain: [0, 1], ticks: [0, 0.25, 0.5, 0.75, 1], factor: true, curve: SUN_FACTOR_BY_SUNNY_SHARE, input: x => x },
};

const svgNamespace = 'http://www.w3.org/2000/svg';
function svgNode<K extends keyof SVGElementTagNameMap>(tag: K, attributes: Record<string, string | number>, text?: string): SVGElementTagNameMap[K] {
  const node = document.createElementNS(svgNamespace, tag);
  for (const [key, value] of Object.entries(attributes)) node.setAttribute(key, String(value));
  if (text !== undefined) node.textContent = text;
  return node;
}

export function createScoreCurve(axisName: ScoreAxis, taste: Taste = DEFAULT_TASTE, compact = false) {
  const axis = axes[axisName];
  const element = document.createElement('figure');
  element.className = `score-curve${compact ? ' score-curve-compact' : ''}`;
  element.dataset.axis = axisName;
  const svg = svgNode('svg', { viewBox: `0 0 320 ${compact ? 88 : 118}`, role: 'img' });
  const left = 29, right = 307, top = compact ? 14 : 20, bottom = compact ? 55 : 85;
  const positionX = (value: number) => left + (value - axis.domain[0]) / (axis.domain[1] - axis.domain[0]) * (right - left);
  const positionY = (grade: number) => bottom - grade / 100 * (bottom - top);
  const number = (value: number) => axisName === 'rain' && value > 0 && value < 0.1 ? value.toFixed(2) : String(Math.round(value * (axisName === 'sunshine' ? 100 : 10)) / (axisName === 'sunshine' ? 1 : 10));
  svg.append(svgNode('text', { x: left, y: 10, class: 'curve-caption' }, axis.factor ? 'Share kept (%)' : 'Grade (0 to 100)'));
  for (const value of [0, 50, 100]) {
    svg.append(svgNode('line', { x1: left, x2: right, y1: positionY(value), y2: positionY(value), class: 'curve-grid' }));
    svg.append(svgNode('text', { x: left - 6, y: positionY(value) + 3, 'text-anchor': 'end', class: 'curve-tick' }, String(value)));
  }
  for (const value of axis.ticks) svg.append(svgNode('text', { x: positionX(value), y: bottom + 13, 'text-anchor': 'middle', class: 'curve-tick' }, number(value)));
  svg.append(svgNode('text', { x: (left + right) / 2, y: compact ? 84 : 114, 'text-anchor': 'middle', class: 'curve-caption' }, axis.unit));
  const path = svgNode('path', { class: 'curve-line', fill: 'none' });
  svg.append(path);
  element.append(svg);
  const references = __CURVE_REFERENCE_POINTS__[axisName];
  const markers = references.map((reference, index) => {
    const group = svgNode('g', { class: 'curve-reference' });
    const dot = svgNode('circle', { cx: positionX(reference.value), r: 3.5 });
    const label = svgNode('text', { x: positionX(reference.value), 'text-anchor': 'middle' }, String(index + 1));
    group.append(dot, label);
    svg.append(group);
    return { dot, label };
  });
  if (!compact) {
    const caption = document.createElement('figcaption');
    caption.className = 'curve-references';
    for (const [index, reference] of references.entries()) {
      const item = document.createElement('span');
      const unit = axisName === 'sunshine' ? '%' : axisName === 'wind' ? ' km/h' : axisName === 'rain' ? ' mm' : ' °C';
      item.textContent = `${index + 1}. ${reference.label}: ${number(reference.value)}${unit}`;
      caption.append(item);
    }
    element.append(caption);
  }
  function update(next: Taste) {
    function gradeAt(value: number) {
      const grade = readCurve(axis.curve, axis.input(value, next)) * (axis.factor ? 100 : 1);
      return axisName === 'evening' ? Math.min(grade, readCurve(HEAT_GRADE_BY_FEELS_IN_SHADE, value + next.heatShift)) : grade;
    }
    // Include each curve's actual corners, including heat's limit on the walk home.
    const inputZero = axis.input(0, next), inputStep = axis.input(1, next) - inputZero;
    const vertices = axis.curve.map(([x]) => (x - inputZero) / inputStep);
    if (axisName === 'evening') vertices.push(...HEAT_GRADE_BY_FEELS_IN_SHADE.map(([x]) => x - next.heatShift));
    const values = [axis.domain[0], ...vertices.filter(x => x > axis.domain[0] && x < axis.domain[1]), axis.domain[1]].sort((a, b) => a - b);
    if (axisName === 'evening') {
      const difference = (x: number) => readCurve(EVENING_GRADE_BY_FEELS_AT_21, x - next.eveningShift) - readCurve(HEAT_GRADE_BY_FEELS_IN_SHADE, x + next.heatShift);
      const intersections: number[] = [];
      for (let index = 1; index < values.length; index++) {
        const start = values[index - 1], end = values[index];
        const startDifference = difference(start), endDifference = difference(end);
        if (startDifference * endDifference < 0) intersections.push(start + (end - start) * -startDifference / (endDifference - startDifference));
      }
      values.push(...intersections);
      values.sort((a, b) => a - b);
    }
    path.setAttribute('d', values.map((x, index) => `${index ? 'L' : 'M'}${positionX(x).toFixed(2)},${positionY(gradeAt(x)).toFixed(2)}`).join(' '));
    for (const [index, reference] of references.entries()) {
      const y = positionY(gradeAt(reference.value));
      markers[index].dot.setAttribute('cy', String(y));
      markers[index].label.setAttribute('y', String(y > top + 14 ? y - 8 : y + 15));
    }
    svg.setAttribute('aria-label', `${axis.title}. ${axis.factor ? 'Percentage of the day kept' : 'Grade from 0 to 100'} by ${axis.unit}. ${references.map(reference => `${reference.label}: ${number(reference.value)}, ${Math.round(gradeAt(reference.value))}${axis.factor ? '% kept' : ' points'}`).join('. ')}.`);
  }
  update(taste);
  return { element, update };
}
