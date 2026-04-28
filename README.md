# Nice Weather Finder POC

POC for a Europe-first weather map that helps chase pleasant weather for travel and cycling.

## Scope

- Frontend: Vite + React + TypeScript + MapLibre.
- Backend: Go.
- Current forecast POC: DWD ICON-EU GRIB2, native 0.0625 degree Europe grid, rendered as static raster tiles.
- Current map mask: Natural Earth land polygons hide sea pixels.
- Target forecast data: same DWD source with useful forecast horizons beyond `d1`.
- Target climate data: WorldClim 2.1 monthly normals, roughly 1 km.
- Profiles: `general`, `cycling`.
- Time basis: `forecast` daytime aggregate from DWD steps 008..018 for the 00Z run, representing 10:00-20:00 Europe/Amsterdam time; `climatology` returns when real monthly artifacts exist.
- Horizons: `d1`.

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
