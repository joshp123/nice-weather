---
written_by: AI, GPT-6 (Codex), 2026-10-12
---

# Interface system

The map is the page. Search opens discovery; one bottom control selects time, preference and weather component. One place sheet explains the result.

- One font: Avenir Next, Avenir, Segoe UI, sans-serif. Names use 21–23 px; body uses 12–14 px; secondary information uses 10–11 px.
- Spacing: 4, 8, 12 and 16 px. Surfaces use 10 px corners; controls use 6 px. Neutral charcoal surfaces and quiet borders keep attention on weather.
- Selection: the same segmented control for time, Nice/Cycling and component. Dates scroll horizontally with a visible continuation cue. Interactive targets are at least 44 px high where space permits; secondary desktop segments are 36 px high; phone segments are 44 px high.
- Weather: six discrete blue, green and gold categories retain the canonical score thresholds. Water and missing evidence remain dark. The legend explicitly distinguishes No data from Awful.
- Geography: vector water, borders and single-language place labels sit above the weather field. No interpolation invents a value for missing land.
- Place: nice-day count, physical cause, poor-stretch risk, temperature/moisture, rain, cloud and wind. The quiet date strip selects a real day. Model and calculation details are disclosed on request.
- Phone: search and the bottom control remain visible. The compact place summary expands into the same sheet; the sheet temporarily takes the control's space.

Colours and thresholds live in `src/weatherPresentation.ts`; surface and spacing tokens live in `src/styles.css`. Weather scores remain backend data.
