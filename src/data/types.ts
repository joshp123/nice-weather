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
    forecast: { name: string; run: string; aggregation?: string; horizons: string[] };
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
  input: {
    tempC: number;
    humidity: number;
    rainMm: number;
    cloudPct: number;
    windKmh: number;
  };
  scores: Record<Profile, AnchorScore>;
};
