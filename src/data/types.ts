export type View = 'range' | 'daily' | 'climate';
export type Profile = 'nice' | 'cycling';
export type Lens = 'overall' | 'comfort' | 'dry' | 'bright' | 'wind';
export type LegendCategory = {
  label: string;
  color: string;
  min: number;
  max: number;
};

export type PaletteStop = { score: number; color: string };

export type PointMetrics = {
  temperatureC: number;
  minTempC: number;
  maxTempC: number;
  rainMm: number;
  wetTimeFraction?: number;
  solarEnergyMJm2?: number;
  cloudPct?: number;
  windKmh: number;
  gustKmh?: number;
  humidityPct?: number;
  dewPointC?: number;
};

export type ScoreComponents = {
  comfort: ComponentScore;
  dryness: ComponentScore;
  sun: ComponentScore;
  wind: ComponentScore;
};

export type ComponentScore = { value: number; category: string };

type PointBase = {
  sourceCell?: { latitude: number; longitude: number; distanceKm: number };
  releaseId: string;
  lat: number;
  lon: number;
  periodLabel: string;
  score: number;
  category: string;
  components: ScoreComponents;
};

export type ForecastWindowPoint = PointBase & {
  kind: 'range';
  window: {
    startDate: string;
    endDate: string;
    dayCount: number;
    niceDays: number;
    awfulDays: number;
    medianScore: number;
    longestPoorRun: number;
  };
  days: ForecastDayScore[];
  source: {
    basis: 'forecast-window';
    detailLabel: string;
    resolutionKm: number;
    artifactGeneratedAt: string;
  };
};

export type ForecastDayScore = {
  date: string;
  score: number;
  category: string;
};

export type DailyPoint = PointBase & {
  kind: 'daily';
  summary: string;
  temperatureNotice?: 'hot' | 'cold';
  metrics: PointMetrics;
  source: PointSource;
};

export type PointWeather = ForecastWindowPoint | DailyPoint;

export type PointSource = {
  basis: 'forecast';
  detailLabel: string;
  providerLabel: string;
  resolutionKm: number;
  temporalResolutionHours: number;
  artifactGeneratedAt: string;
  runAt?: string;
  validFrom?: string;
  validTo?: string;
};
