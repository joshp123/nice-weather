export type View = 'range' | 'daily' | 'climate';
export type Profile = 'nice' | 'cycling';
export type Lens = 'overall' | 'comfort' | 'dry' | 'bright' | 'wind';
export type ForecastKind = 'detailed' | 'outlook';
export type DataStatus = 'live' | 'sample';

export type WeatherImage = {
  url: string;
  sha256: string;
  width: number;
  height: number;
  projection: 'web-mercator';
};

export type WindFrameAsset = {
  validAt: string;
  url: string;
  sha256: string;
  clippedComponents: number;
};

export type PointGeometry = {
  bounds: { west: number; east: number; south: number; north: number };
  quad: { west: number; east: number; north: number; south: number };
  rows: number;
  cols: number;
};

export type PointAsset = {
  url: string;
  sha256: string;
  payloadSha256: string;
  bytes: number;
  kind: 'forecast-day' | 'forecast-window' | 'legacy-climate-month';
  headerBytes: 128;
  recordBytes: 30 | 44;
  projection: 'web-mercator-image';
  geometry: PointGeometry;
};

export type Period = {
  id: string;
  label: string;
  shortLabel: string;
  date?: string;
  month?: number;
  kind?: ForecastKind;
  resolutionKm: number;
  images: Record<Profile, WeatherImage>;
  lenses: Record<'comfort' | 'dry' | 'bright' | 'wind-nice' | 'wind-cycling', WeatherImage>;
  providerLabel: string;
  sourceRunAt: string;
  sourceValidFrom: string;
  sourceValidTo: string;
  temporalResolutionHours: number;
  point: PointAsset;
  windFrames?: WindFrameAsset[];
};

export type ForecastWindow = {
  id: 'next-14-days';
  label: string;
  shortLabel: string;
  startDate: string;
  endDate: string;
  dayCount: 14;
  resolutionKm: number;
  point: PointAsset;
  profiles: Record<Profile, { image: WeatherImage }>;
};

export type LegendCategory = {
  label: string;
  color: string;
  min: number;
  max: number;
};

export type PaletteStop = { score: number; color: string };

export type WeatherManifest = {
  version: 6;
  releaseId: string;
  productContract: 'nice-weather-v14';
  dataStatus: DataStatus;
  generatedAt: string;
  bounds: [number, number, number, number];
  forecast: {
    updatedAt: string;
    staleAfterHours: number;
    window: ForecastWindow;
    days: Period[];
  };
  climate?: {
    label: string;
    period: string;
    months: Period[];
  };
  legend: LegendCategory[];
  palette: PaletteStop[];
};

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
  metrics: PointMetrics;
  source: PointSource;
};

export type PointWeather = ForecastWindowPoint | DailyPoint;

export type PointSource = {
  basis: 'forecast' | 'climate';
  detailLabel: string;
  providerLabel: string;
  resolutionKm: number;
  temporalResolutionHours: number;
  artifactGeneratedAt: string;
  runAt?: string;
  validFrom?: string;
  validTo?: string;
  referencePeriod?: string;
};

export type PointRequest = {
  releaseId: string;
  lat: number;
  lon: number;
  period: string;
  profile: Profile;
};
