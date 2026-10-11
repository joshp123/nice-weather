---
written_by: AI, GPT-6 (Codex), 2026-10-12
---

# Product reset

Josh's goal is to choose where to position himself for warm, bright, dry and calm weather, worldwide. The product must support both immediate travel and a January–December journey. Cycling needs stricter rain and wind limits. Absolute comfort matters: 16°C is mediocre; oppressive humidity is different from dry heat. European forecast detail must remain better than the worldwide baseline.

## What the references teach us

[Theo Jaffee's app](https://weather.theojaffee.net/) delivers a complete interaction: change preferences, move through the year, compare regions, select a place and see its best months. The annual control, contextual city values, compact annual place chart and sharp geographic background do more useful work than a large explanation panel.

Its [source](https://github.com/tjaffee99/weather) also reveals limits we should not inherit. The climate calendar contains 365 sampled days stitched from short blocks across 2001–2025, rather than 25 complete years. Its 1° weather field gains visual detail through interpolation and terrain adjustment. Map colours, city labels and selected-place results use different lookup paths. A percentage from this sample cannot establish the reliability of a whole month or the risk of a poor week.

[Ryan Hall's daily map](https://ryanhallyall.com/nice) is the primary visual reference, as Josh explicitly clarified. Its separated blue, green and gold bands, quiet dark background, readable labels and directly visible day controls make favourable areas easy to see. Borrow its map hierarchy and colour clarity; borrow Theo's annual navigation and annual place chart. Ryan's locally relative temperature score does not serve Josh's absolute warm-weather preference. Forecast uncertainty should be described separately from suitability.

Our inspected app has a stale forecast covering 26 July–8 August 2026, no seasonal journey, a muddy palette and conspicuous grid steps at the coast. The fortnight place view shows score components without enough physical evidence to explain the result. Passing pipeline checks has not delivered the original product.

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

The same pinned Earthmover ERA5 snapshot has a temporal layout suited to long records for small spatial tiles. [The provider documents both layouts as lossless](https://registry.opendata.aws/earthmover-era5/). Test that access path on representative land tiles before extending the current full-world hourly acquisition. Its production throughput and storage savings are not yet measured. A cheaper access pattern is preferable to weakening the climate evidence.

Remove abandoned provider pipelines, the unreachable monolithic publisher and duplicate map renderers. Keep shared codecs that the active product uses. Do not replace React merely to resemble a smaller dependency list.

## Delivery and acceptance

Use the existing Nice Weather ChatGPT Sites project and `nice.jjpcodes.com`. Serve the small frontend through Sites, with compact weather artifacts in Sites-managed object storage and a thin request layer. Acquisition and scoring remain offline. Publish immutable artifacts before pointers. Verify the storage and byte-range behaviour on real production assets before release.

Judge the result by choosing where to go: compare Málaga, winter in northern Europe, humid Singapore and a windy cycling coast. Check the whole calendar, physical reasons, map/place agreement, readable phone controls and a working refresh. Retain tests that protect those actual behaviours. A test count, review verdict or screenshot collection is not a product milestone.

The October cleanup removes the rejected AgERA5 pipeline and obsolete commands. It does not make the current app complete or publish a new site.
