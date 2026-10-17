// Written by AI on 2026.10.17 by model claude opus 5.5.
// How Nice Weather judges a day: Fable's graded method (research/taste/nice_v6.py), ported exactly.
// Every hour gets a warmth grade; the walk home at 21:00 counts like two hours; rain, wind, humidity and
// sun then scale the day. A day scores 0 to 100. A period's score is the mean of its days.

export type Curve = readonly (readonly [number, number])[];

export const COLD_GRADE_BY_FEELS_IN_SUN: Curve = [[9, 0], [11, 3], [13, 10], [15, 22], [17, 40], [19, 62], [21, 85], [23, 100]];
export const HEAT_GRADE_BY_FEELS_IN_SHADE: Curve = [[28, 100], [31, 85], [34, 60], [37, 30], [40, 10], [43, 0]];
export const EVENING_GRADE_BY_FEELS_AT_21: Curve = [[4, 0], [8, 30], [12, 65], [16, 90], [19, 100]];
export const RAIN_FACTOR_BY_MM: Curve = [[0, 1], [0.5, 0.95], [1, 0.88], [2, 0.74], [4, 0.5], [8, 0.22], [15, 0.05]];
export const WIND_FACTOR_BY_KMH: Curve = [[15, 1], [20, 0.97], [25, 0.92], [30, 0.85], [40, 0.65], [50, 0.4], [65, 0.15]];
export const HUMIDITY_FACTOR_BY_DEW_POINT: Curve = [[16, 1], [18, 0.95], [20, 0.85], [22, 0.65], [24, 0.4], [26, 0.2], [28, 0.1]];
export const SUN_FACTOR_BY_SUNNY_SHARE: Curve = [[0, 0.75], [0.3, 0.85], [0.6, 0.95], [0.8, 1]];
export const EVENING_WORTH_IN_HOURS = 2;
export const NICE_SCORE = 55;

export const BANDS = [
  { name: 'Awful', minimum: 0 },
  { name: 'Poor', minimum: 25 },
  { name: 'Fair', minimum: 40 },
  { name: 'Nice', minimum: 55 },
  { name: 'Beautiful', minimum: 70 },
  { name: 'Perfect', minimum: 85 },
] as const;
export type BandName = (typeof BANDS)[number]['name'];

export function bandOf(score: number): BandName {
  let name: BandName = 'Awful';
  for (const band of BANDS) if (score >= band.minimum) name = band.name;
  return name;
}

export function readCurve(curve: Curve, x: number): number {
  if (x <= curve[0][0]) return curve[0][1];
  for (let i = 1; i < curve.length; i++) {
    const [x1, y1] = curve[i];
    if (x <= x1) {
      const [x0, y0] = curve[i - 1];
      return y0 + ((y1 - y0) * (x - x0)) / (x1 - x0);
    }
  }
  return curve[curve.length - 1][1];
}

// A visitor's taste, as shifts of the curves above. Zero everywhere is Josh's taste, the default.
export type Taste = Readonly<{
  warmShift: number; // °C: positive needs it warmer before an hour is perfect
  heatShift: number; // °C: positive means heat bothers sooner
  eveningShift: number; // °C: positive needs a warmer walk home
  humidityShift: number; // °C of dew point: positive means humidity bothers later
  windShift: number; // km/h: positive means wind bothers later
  rainScale: number; // 1 = default; 0.5 means rain bothers half as much
}>;

export const DEFAULT_TASTE: Taste = { warmShift: 0, heatShift: 0, eveningShift: 0, humidityShift: 0, windShift: 0, rainScale: 1 };

// The sliders, in the units a person reads. Each one moves one curve. `toTaste` maps the slider value onto the taste.
export type Slider = Readonly<{
  id: 'perfectFrom' | 'tooHotFrom' | 'walkHome' | 'humidity' | 'wind' | 'rain';
  label: string;
  unit: string;
  min: number;
  max: number;
  step: number;
  default: number;
  toTaste: (value: number) => Partial<Taste>;
}>;

export const SLIDERS: readonly Slider[] = [
  { id: 'perfectFrom', label: 'Warm enough from', unit: '°C feels-like in the sun', min: 17, max: 29, step: 0.5, default: 23, toTaste: v => ({ warmShift: v - 23 }) },
  { id: 'tooHotFrom', label: 'Too hot from', unit: '°C feels-like in the shade', min: 22, max: 36, step: 0.5, default: 28, toTaste: v => ({ heatShift: 28 - v }) },
  { id: 'walkHome', label: 'Walk home at 21:00 is perfect from', unit: '°C feels-like', min: 10, max: 26, step: 0.5, default: 19, toTaste: v => ({ eveningShift: v - 19 }) },
  { id: 'humidity', label: 'Humidity halves a day at', unit: '°C dew point', min: 16, max: 30, step: 0.5, default: 23, toTaste: v => ({ humidityShift: v - 23 }) },
  { id: 'wind', label: 'Wind halves a day at', unit: 'km/h average', min: 25, max: 70, step: 1, default: 46, toTaste: v => ({ windShift: v - 46 }) },
  { id: 'rain', label: 'Rain halves a day at', unit: 'mm, 10:00 to 21:00', min: 1, max: 20, step: 0.5, default: 4, toTaste: v => ({ rainScale: 4 / v }) },
];

export function tasteFromSliders(values: Readonly<Record<Slider['id'], number>>): Taste {
  return SLIDERS.reduce<Taste>((taste, slider) => ({ ...taste, ...slider.toTaste(values[slider.id]) }), DEFAULT_TASTE);
}

// One day, as the 15-byte record decodes (see .agent/v6-contract.md), with felt temperatures already shifted by
// the place's fine-grid offset.
export type Day = Readonly<{
  feelsInSun: readonly number[]; // local 10, 12, 14, 16, 18, 20 h
  feelsInShade: readonly number[]; // local 12, 15, 18, 21 h
  dewPoint: number;
  rainMm: number;
  windKmh: number;
  sunnyHours: number;
  daylightHours: number;
}>;

export type DayJudgement = Readonly<{
  score: number;
  warmth: number; // 0-100 before the factors
  evening: number; // 0-100, the walk home
  rainFactor: number;
  windFactor: number;
  humidityFactor: number;
  sunFactor: number;
}>;

const SUN_HOURS = [0, 2, 4, 6, 8, 10];
const SHADE_HOURS = [2, 5, 8, 11];

function hourly(values: readonly number[], keys: readonly number[], hour: number): number {
  if (hour <= keys[0]) return values[0];
  for (let i = 1; i < keys.length; i++) {
    if (hour <= keys[i]) return values[i - 1] + ((values[i] - values[i - 1]) * (hour - keys[i - 1])) / (keys[i] - keys[i - 1]);
  }
  return values[values.length - 1];
}

export function judgeDay(day: Day, taste: Taste, feltOffset = 0): DayJudgement {
  let daytime = 0;
  for (let hour = 0; hour < day.daylightHours; hour++) {
    const sun = hourly(day.feelsInSun, SUN_HOURS, hour) + feltOffset;
    const shade = hourly(day.feelsInShade, SHADE_HOURS, hour) + feltOffset;
    daytime += Math.min(readCurve(COLD_GRADE_BY_FEELS_IN_SUN, sun - taste.warmShift), readCurve(HEAT_GRADE_BY_FEELS_IN_SHADE, shade + taste.heatShift));
  }
  const at21 = day.feelsInShade[3] + feltOffset;
  const evening = Math.min(readCurve(EVENING_GRADE_BY_FEELS_AT_21, at21 - taste.eveningShift), readCurve(HEAT_GRADE_BY_FEELS_IN_SHADE, at21 + taste.heatShift));
  const warmth = (daytime + EVENING_WORTH_IN_HOURS * evening) / (day.daylightHours + EVENING_WORTH_IN_HOURS);
  const rainFactor = readCurve(RAIN_FACTOR_BY_MM, day.rainMm * taste.rainScale);
  const windFactor = readCurve(WIND_FACTOR_BY_KMH, day.windKmh - taste.windShift);
  const humidityFactor = readCurve(HUMIDITY_FACTOR_BY_DEW_POINT, day.dewPoint - taste.humidityShift);
  const sunFactor = readCurve(SUN_FACTOR_BY_SUNNY_SHARE, day.sunnyHours / day.daylightHours);
  return { score: warmth * rainFactor * windFactor * humidityFactor * sunFactor, warmth, evening, rainFactor, windFactor, humidityFactor, sunFactor };
}

export function decodeDay(record: Uint8Array, offset: number): Day {
  const temperature = (byte: number) => byte / 3 - 30;
  return {
    feelsInSun: [0, 1, 2, 3, 4, 5].map(i => temperature(record[offset + i])),
    feelsInShade: [6, 7, 8, 9].map(i => temperature(record[offset + i])),
    dewPoint: temperature(record[offset + 10]),
    rainMm: (record[offset + 11] / 50) ** 2,
    windKmh: record[offset + 12] / 2,
    sunnyHours: record[offset + 13],
    daylightHours: Math.max(1, record[offset + 14]),
  };
}
