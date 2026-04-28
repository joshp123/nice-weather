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
    forecast: { name: string; run: string; horizons: string[] };
    climatology?: { name: string; period: string; horizons: string[] };
  };
  layers: LayerManifest[];
  legend: {
    min: number;
    max: number;
    categories: LegendCategory[];
  };
};
