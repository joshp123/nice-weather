<!-- Written by AI on 2026.10.18 by model GPT-6.1 Sol. -->

# Named-place delivery: rendered proof

The compact default-taste path passes the local active and inactive checks. It uses the accepted daytime correction and uniform-shift evening. No upload or deployment was performed. Upload order, release sizes and daily-runner steps are in [UPLOAD.md](UPLOAD.md).

At default taste, named places use the shared `namedPlaceNumbers` aggregate for cards, map-label scores/counts and headline ranking. The Worker uses that same function for its card API and previews. Climate facts load only for the selected place through `/api/named/place`; the browser downloads no replay carrier or correction tiles. Forecast scores/counts use the compact daily forecast table; forecast physical facts retain the existing model.

An exact catalogue-coordinate match is required. Unnamed coordinates, unsupported records and changed taste use the existing 25 km sampler. The existing period line says “Area estimate” for these cards. The month strip also returns to overview facts when taste changes. The weather map itself remains the 25 km overview.

## What the cards actually show

All numbers below were checked in the rendered browser against the shared Worker aggregate (active), or the unchanged browser sampler over published files (inactive and unnamed). Counts are displayed whole days. Phone and laptop San Francisco values agree.

| Place and period | Inactive: score / nice / perfect / awful | Active: score / nice / perfect / awful |
| --- | ---: | ---: |
| San Francisco, July; phone and laptop | 98 / 31 / 30 / 0 | 75 / 27 / 8 / 0 |
| Funchal, July | 95 / 31 / 26 / 0 | 100 / 31 / 31 / 0 |
| San Diego, July | 90 / 31 / 21 / 0 | 100 / 31 / 31 / 0 |
| Unnamed point near San Francisco, July | 98 / 31 / 30 / 0 | 98 / 31 / 30 / 0 |
| San Francisco, next 14 days; phone and laptop | 43 / 6 / 3 / 6 | 43 / 6 / 3 / 6 |

The unnamed point is `37.7751,-122.4181`: resolving its display name to San Francisco does not substitute the named city's numbers. Its selected coordinate stays intact and its card says “Area estimate”. Changing the comfortable-temperature slider from 23 to 24 °C produces the same coarse result in both states: San Francisco July becomes 96 / 31 / 27 / 0; forecast becomes 32 / 6 / 3 / 6.

The public catalogue contains 170,513 distinct coordinates; 31 unsupported climate records fall back. The Tenerife south airport station reference is not a catalogue entry and therefore falls back. Named Arona and Adeje do use the fine numbers. The accepted station comparison and Rotterdam's known May limitation remain unchanged.

## Labels, headlines and fallback

`research/fine/named_app_proof.mjs` ran eight views in each state, using the real Worker and production bundle with private local data fixtures: world July, Funchal July, San Francisco July on phone/laptop, San Diego July, unnamed July on phone, and San Francisco forecast on phone/laptop. Inactive means both fine and named activation pointers are absent. All 16 views passed with no runtime exceptions or null cards.

The proof inspects actual MapLibre worker source messages, comparing label score/count properties with the shared aggregate: the first 12 catalogue labels plus San Francisco, Funchal and San Diego entries. It independently ranks the world catalogue through the app's ranking rule and checks the rendered headline. Active July names are Los Angeles, Belo Horizonte and Brasília; inactive names are Brasília, Uberlândia and Ribeirão Preto. A changed taste returns headlines and counts to the overview path.

The run exposed a pending old replay-label query that could execute after fine mode became inactive. Fine mode now becomes enabled only after its status resolves active, and the idle query checks that state again before execution. Both full runs were repeated after this fix.

## Slider cost

These are actual `nice-weather-redraw` event durations for one 23→24 °C slider change per view on this Mac, in headless Chrome. Both states made **zero additional network requests** during the slider change. The GPU overview scoring path is unchanged, but the active run measured slower: timings are still tens of milliseconds, not numerically identical.

| View | Inactive ms | Active ms |
| --- | ---: | ---: |
| World, laptop | 19.5 | 69.2 |
| Funchal, laptop | 29.8 | 36.0 |
| San Francisco, phone | 31.6 | 48.8 |
| San Francisco, laptop | 24.0 | 31.8 |
| San Diego, laptop | 14.5 | 53.4 |
| Unnamed point, phone | 16.9 | 74.0 |
| San Francisco forecast, phone | 17.0 | 30.7 |
| San Francisco forecast, laptop | 22.9 | 32.4 |
| Median / maximum | 21.2 / 31.6 | 42.4 / 74.0 |

A separate paired San Francisco laptop run then measured the first default→24 °C transition and twelve warm inputs (25→26→24, four times), awaiting every actual redraw. First-transition redraw: active **32.3 ms**, inactive **21.6 ms**. Warm redraw median/maximum: active **31.25 / 32.5 ms**, inactive **31.40 / 35.1 ms**. Warm input-to-redraw wall medians were **80.4 / 82.25 ms**, including the existing input debounce. Both runs made zero slider network requests and had no exceptions. This bounded paired check shows no warm-drag slowdown; it does not erase the slower first-transition readings above. Raw [active timing](</Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/named-b1/timing-active/results.json>) and [inactive timing](</Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/named-b1/timing-inactive/results.json>) retain all twelve samples. Set `NWF_NAMED_TIMING_ONLY=1` to rerun this check without changing product code.

These measurements use 390×844 and 1440×1080 viewports; they are not physical-phone hardware or remote Sites latency measurements. The compact browser tables transfer 11,207,848 climate bytes and 15,783,945 forecast bytes. Selected-place facts arrive as small JSON responses rather than downloading the facts shards. No replay or correction-tile requests occurred in either run.

## Full replay comparison

The separate [397-place comparison](</Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/named-b1-20261018/parity.json>) covers the thirteen reference places and 384 seeded random points. Private validation coordinates do not expand public catalogue membership.

- 26,996 climate comparisons across 17 periods: score and day-count differences exactly zero.
- 17,220 forecast comparisons: score differences zero; count difference at most 0.000000413 days, with identical displayed whole-day counts.
- 28,584 physical-fact comparisons: maximum difference 0.000003815 from float storage.
- Of 60,741 visible comparisons, 46 physical-fact displays at random points differ by one unit at rounding ties (mainly sunny/wet percentages, also daylight hours). No score/count display differs. This is not a claim of bit-identical physical facts.

## Screenshots and rerun evidence

The following final screenshots were opened and visually checked. Cards remain readable; phone cards scroll for the lower facts. No interface styling was changed.

- [Active San Francisco, phone](</Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/named-b1/app-active/san-francisco-phone-default.png>) and [laptop](</Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/named-b1/app-active/san-francisco-laptop-default.png>).
- [Active Funchal](</Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/named-b1/app-active/funchal-cold-default.png>) and [San Diego](</Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/named-b1/app-active/san-diego-desktop-default.png>).
- [Inactive San Francisco, phone](</Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/named-b1/app-inactive/san-francisco-phone-default.png>) and [laptop](</Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/named-b1/app-inactive/san-francisco-laptop-default.png>).
- [Active unnamed fallback](</Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/named-b1/app-active/unnamed-phone-default.png>).
- Complete [active result](</Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/named-b1/app-active/results.json>) and [inactive result](</Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/named-b1/app-inactive/results.json>) include source hashes, card text, actual label-source properties, requests and timings.

The final production bundle was rebuilt before these runs. The proof is rerunnable with `research/fine/named_app_proof.mjs OUTPUT OVERLAY PACKAGE active|inactive`; the package argument can be an empty string to use the current `dist`. Fixture overlays and screenshots stay on the SSD. They are not upload inputs.
