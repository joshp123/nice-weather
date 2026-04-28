# ChatGPT Browser-Use Handover Prompt

Use browser-use to inspect these references:

- https://ryanhallyall.com/nice
- https://weatherspark.com/
- https://weatherspark.com/compare/y/51258~557/Comparison-of-the-Average-Weather-in-Rotterdam-and-San-Francisco
- https://weatherspark.com/y/51258/Average-Weather-in-Rotterdam-Netherlands-Year-Round#Sections-Sources

I want a POC architecture for a Europe-first weather-chasing map.

Product goal:

- I want to buy a camper van and travel a lot.
- I can also fly.
- I hate winter weather.
- I like cycling.
- I like sun.
- I want to find where in Europe the weather is good now, soon, and by month.

Hard scope:

- Europe only.
- Forecast source: DWD ICON-EU, roughly 6.5 km.
- Climate source: WorldClim 2.1 monthly normals, roughly 1 km.
- Frontend: Vite + React + TypeScript + MapLibre.
- Backend/pipeline: recommend Go unless there is a strong reason not to.
- No model soup. No city-only shortcut. No fake global 2 km precision.
- No UV/AQI in POC unless they are available without adding another source.

Canonical dimensions:

- `basis`: `forecast | climatology`
- `horizon`: `now | d1..dN | jan..dec`
- `profile`: `general | cycling`
- `variable`: `temp | humidity | rain | cloud | wind`

One and only one scoring contract:

```ts
type WeatherInput = {
  tempC: number;
  humidity: number;
  rainMm: number;
  cloudPct: number;
  windKmh: number;
};
```

All data sources must map into `WeatherInput`. The scoring engine must not know or care whether the source is ICON-EU or WorldClim.

Profiles are just different weights over the same component scores:

- `general`: temp, humidity, rain, cloud, wind
- `cycling`: same components, higher wind/rain penalties

Engineering principles:

- Uncle Bob clean code, but pragmatic.
- Zen of Python, especially: “There should be one and preferably only one obvious way to do it.”
- Simple > complex.
- Explicit > implicit.
- Flat > nested.
- Good interfaces.
- Small modules.
- No overengineering.
- No fallback paths, abstraction factories, plugin systems, compatibility layers, premature scaling, or speculative future-proofing.
- Fail clearly.

Please produce:

1. A simple POC architecture.
2. Data pipeline outline.
3. Frontend component outline.
4. Backend package/module outline.
5. Exact source-field-to-`WeatherInput` mapping.
6. Missing fields / approximation risks.
7. Minimal artifact format for the frontend.
8. A first milestone that can be built in a week.
9. Things explicitly not to build.

Keep it concise and concrete.

