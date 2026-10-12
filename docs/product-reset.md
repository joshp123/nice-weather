---
written_by: AI, GPT-6 (Codex), 2026-10-12
---

# Product reset

Josh's goal is to choose where to position himself for warm, bright, dry and calm weather, worldwide. The product must support both immediate travel and a January–December journey. Cycling needs stricter rain and wind limits. Absolute comfort matters: 16°C is mediocre; oppressive humidity is different from dry heat. European forecast detail must remain better than the worldwide baseline.

## What the references teach us

[Theo Jaffee's app](https://weather.theojaffee.net/) delivers a complete interaction: change preferences, move through the year, compare regions, select a place and see its best months. The annual control, contextual city values, compact annual place chart and sharp geographic background do more useful work than a large explanation panel.

Its [source](https://github.com/tjaffee99/weather) also reveals limits we should not inherit. The climate calendar contains 365 sampled days stitched from short blocks across 2001–2025, rather than 25 complete years. Its 1° weather field gains visual detail through interpolation and terrain adjustment. Map colours, city labels and selected-place results use different lookup paths. A percentage from this sample cannot establish the reliability of a whole month or the risk of a poor week.

[Ryan Hall's daily map](https://ryanhallyall.com/nice) is the primary visual reference, as Josh explicitly clarified. Its separated blue, green and gold bands, quiet dark background, readable labels and directly visible day controls make favourable areas easy to see. Borrow its map hierarchy and colour clarity; borrow Theo's annual navigation and annual place chart. Ryan's locally relative temperature score does not serve Josh's absolute warm-weather preference. Forecast uncertainty should be described separately from suitability.

The initial October inspection found a stale forecast covering 26 July–8 August 2026, no seasonal journey, a muddy palette and conspicuous grid steps at the coast. The fortnight place view showed score components without enough physical evidence to explain the result. Pipeline checks had not delivered the original product. The current forecast, palette and physical place evidence now address those failures; the complete annual publication remains outstanding.

## The interface

One map, one bounded bottom control, one search and one place sheet.

- **Time:** directly accessible forecast and year views. Open on the next-14-day overview and remember the last chosen view, as Josh selected. Forecast offers the overview and individual dates. Year offers all twelve months. Do not present unavailable months as working controls.
- **Preference:** Nice or Cycling. Keep unrestricted scoring sliders out of the resting screen.
- **Lens:** Overall, Comfort, Dry, Clear sky and Calm, coordinated with the same selected time and preference.
- **Place:** lead with a useful answer, such as the number of nice forecast days or the monthly chance of a nice day. Follow with the main physical reason and units. A compact day strip or annual chart provides comparison and time selection. Temperature and moisture, rain, cloud and wind explain the result. Source and calculation details stay behind disclosure.
- **Geography:** clear water, coastline, borders and place labels. Weather may be smooth without suggesting finer meteorological resolution. City values must use the same stored meaning as the selected place.

The minimal design system has one font stack, a small spacing scale, quiet neutral surfaces, one segmented-control family, one sheet, one marker and one legend. Derive the palette from Ryan's clear blue-to-green-to-gold bands and subdued unsuitable areas. Preserve the difference between poor and awful weather in the legend and selected-place evidence. Avoid the current muddy continuous brown palette. Choose exact colours by inspecting the map, legend and phone view together.

## Information and implementation

Retain one canonical weather domain and scoring path, IFS worldwide forecasts and ICON-EU European refinement. Preserve real-day seasonal distributions, physical evidence and poor-spell risk. Map and place results must agree. Model resolution, freshness and missing coverage must remain honest.

The next seasonal milestone is the complete annual journey. A monthly build can be an internal acquisition step; another public November-only pilot does not fulfil it. Do not solve missing data by presenting an averaged fake day or relabelling a tiny sample as a long-term distribution.

The same pinned Earthmover ERA5 snapshot has a temporal layout suited to long records for small spatial tiles. [The provider documents both layouts as lossless](https://registry.opendata.aws/earthmover-era5/). Actual complete twenty-year tiles for Málaga, Amsterdam and Singapore downloaded in 7–12 seconds and reduced in 4–10 seconds on existing hardware. One persistent reader and one-tile prefetch now process the world into compact twelve-month checkpoints. Raw scratch is bounded to two tiles, about 1.4 GB. Complete worldwide elapsed time and final hosted size remain to be measured. This acquisition replaces the repeated monthly reader without weakening the climate evidence.

The October source review found two further references. [Pohoda](https://pohoda.app/method) uses 30 complete years of ERA5-Land and a seasonal trend, with useful explicit limitations. Its 9–11 km land grid does not provide finer cloud observations: sunshine is estimated from radiation and cloud cover is absent. Its permanent settings panel and red/green map do not improve our visual direction. [SeasonMap](https://seasonmap.app/about) supplements ten-year ERA5 normals with nearby weather stations and shows a compact annual strip. Its place-centred monthly averages cannot replace worldwide real-day reliability. Keep Ryan's map hierarchy and our full-day distribution requirement.

[ECMWF announced finer free IFS delivery](https://forum.ecmwf.int/t/ecmwf-free-and-open-data-at-9-km-soon-to-come/15366) for 13–15 October 2026. The announced 0.1° regular grid is different from the native 9 km grid; current 0.25° files remain available during the transition. Our 11 October live 0.1° availability check returned 404. Upgrade only after all required fields and fourteen days can actually be acquired, decoded and published within the free hosting limits. [DWD ICON-DREAM](https://opendata.dwd.de/climate_environment/CDC/help/landing_pages/doi_landingpage_ICON-DREAM_v1-en.html) is a genuinely finer atmospheric reanalysis, global 13 km and European 6.5 km, but its record starts in 2010 and monthly hourly files are multi-gigabyte. It cannot supply the required 2006–2025 history. These are source decisions, not grounds for adding provider selection to the interface.

A further live check found current native O1280 IFS forecasts in [Open-Meteo's sponsored bulk archive](https://registry.opendata.aws/open-meteo/), with anonymous access and CC-BY 4.0 data. The completed 11 October 12 UTC run contains all seven required fields through 360 hours. A bounded reader retrieved complete finite fifteen-day records for each field. Temperature, dew point and wind components retain 0.05-unit precision; cloud retains one percentage point and rain 0.1 mm. Native timestamps become three- and six-hourly beyond the short range; interval fields have one fewer record than instantaneous fields. Do not interpret the metadata's hourly nominal interval as hourly observations. Assess this finer route against processing time, hosted size and free request limits before replacing the direct ECMWF ingest. The same archive provides compact hourly ERA5 on its original 0.25° grid, but rain quantisation can change wet-time evidence. The operating lossless Earthmover temporal reader remains the annual source.

Remove abandoned provider pipelines, the unreachable monolithic publisher and duplicate map renderers. Keep shared codecs that the active product uses. Do not replace React merely to resemble a smaller dependency list.

## Delivery and acceptance

Use the existing Nice Weather ChatGPT Sites project at `https://nice-weather.jjpcodes.chatgpt.site`. Serve the small frontend through Sites, with compact weather artifacts in Sites-managed object storage and a thin request layer. Acquisition and scoring remain offline. Publish immutable artifacts before pointers. Verify the storage and byte-range behaviour on real production assets before release.

Josh subsequently made zero new cost a hard requirement: only free hosting, storage, request and data features are permitted. Existing hardware supplies offline processing. Sites provides included managed hosting without a personal Cloudflare account. Route53 charges for the optional weather domain's queries, so its binding and three weather-specific records were removed. Do not enable paid overages or cloud compute. Retain current and previous publications, then prune older generated forecast data after a successful publication.

Judge the result by choosing where to go: compare Málaga, winter in northern Europe, humid Singapore and a windy cycling coast. Check the whole calendar, physical reasons, map/place agreement, readable phone controls and a working refresh. Retain tests that protect those actual behaviours. A test count, review verdict or screenshot collection is not a product milestone.

The current forecast and accepted interface are published on the existing owner-private Site. Actual hosted forecast flows pass on desktop and phone. The annual release and first successful scheduled refresh remain required before completion.
