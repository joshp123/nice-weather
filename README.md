# Nice Weather Finder POC

POC for a Europe-first weather map that helps chase pleasant weather for travel and cycling.

## Scope

- Frontend: Vite + React + TypeScript + MapLibre.
- Backend: Go.
- Current forecast POC: DWD ICON-EU GRIB2, native 0.0625 degree Europe grid, rendered as static raster tiles.
- Target forecast data: same DWD source with useful forecast horizons beyond `now`.
- Target climate data: WorldClim 2.1 monthly normals, roughly 1 km.
- Profiles: `general`, `cycling`.
- Time basis: `forecast` now; `climatology` returns when real monthly artifacts exist.
- Horizons: `now`.

## One Scoring Contract

All source data must map into one canonical weather input:

```ts
type WeatherInput = {
  tempC: number;
  humidity: number;
  rainMm: number;
  cloudPct: number;
  windKmh: number;
};
```

The scoring engine is source-agnostic. It only accepts `WeatherInput` and returns component scores plus `general` and `cycling` composites.

## Backend Direction

Use Go for the backend/pipeline. This is grid/file processing, not app-server cleverness. Keep it boring: download source files, normalize fields, score cells, emit static map artifacts and small metadata JSON.
