export type Basis = 'forecast' | 'climatology';
export type Profile = 'general' | 'cycling';

export type LayerManifest = {
  basis: Basis;
  profile: Profile;
  horizon: string;
  tileUrl: string;
};

export type LegendCategory = {
  label: string;
  color: string;
  min: number;
  max: number;
};

export type Manifest = {
  version: 1;
  generatedAt: string;
  sources: {
    forecast: {
      name: string;
      run: string;
      runStartedAt?: string;
      aggregation?: string;
      staleAfterHours?: number;
      horizons: string[];
      horizonDetails?: ForecastHorizon[];
    };
    climatology?: { name: string; period: string; horizons: string[] };
  };
  layers: LayerManifest[];
  anchors: Anchor[];
  legend: {
    min: number;
    max: number;
    categories: LegendCategory[];
  };
};

export type ForecastHorizon = {
  id: string;
  label: string;
  localDate: string;
  windowStart: string;
  windowEnd: string;
};

export type AnchorScore = {
  score: number;
  category: string;
  color: string;
  temp: number;
  rain: number;
  cloud: number;
  wind: number;
  humidity: number;
  why: string;
};

export type Anchor = {
  name: string;
  lon: number;
  lat: number;
  horizons: Record<string, AnchorHorizon>;
};

export type AnchorHorizon = {
  input: {
    tempC: number;
    humidity: number;
    rainMm: number;
    cloudPct: number;
    windKmh: number;
  };
  scores: Record<Profile, AnchorScore>;
};
