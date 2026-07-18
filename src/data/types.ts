export type Mode = 'forecast' | 'climate';
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
  temporalResolutionHours: number;
};

export type LegendCategory = {
  label: string;
  color: string;
  min: number;
  max: number;
};

export type PaletteStop = { score: number; color: string };

export type WeatherManifest = {
  version: 4;
  releaseId: string;
  productContract: 'nice-weather-v7';
  dataStatus: DataStatus;
  generatedAt: string;
  bounds: [number, number, number, number];
  forecast: {
    updatedAt: string;
    staleAfterHours: number;
    days: Period[];
  };
  climate: {
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

export type PointWeather = {
  releaseId: string;
  lat: number;
  lon: number;
  periodLabel: string;
  score: number;
  category: string;
  components: ScoreComponents;
  summary: string;
  metrics: PointMetrics;
  source: PointSource;
};

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
