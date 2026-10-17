<!-- Written by AI on 2026.10.18 by model GPT-6.1 Sol. -->

# One city, one coordinate

The existing label generator rounds coordinates to two decimal places. At 30 arc-seconds that can select a different correction pixel from the search result, Worker preview or card. Measured across twelve months, rounded Funchal labels differ from the canonical city point by up to 15 score points and 3.875 nice days; San Diego by 5 score points; Lima by 1.5 points. The Tenerife south airport reference has no city label. The other nine named validation cities currently have identical scores and day counts, although Los Angeles and Bangkok select different pixels.

Hardcoded home/reference points also differ from the city catalogue. Of eighteen, eleven select a different pixel. Monthly score differences reach 7.5 points in Chicago and Auckland and 5 in Tokyo; nice-day differences reach 3.75 days in Toronto, Auckland and Santiago. These are coordinate differences, not a change to the correction or scoring model.

`canonical_labels.py` reads the existing `places.tsv` and label files. It preserves all 15,000 rows, order, names, slugs and populations, and retains the source coordinates' precision. GeoNames attribution accompanies the result. The two label JSON files total 881,313 bytes; 7,210 rows change pixels under the shared JavaScript pixel calculation.

The Worker serves `/api/fine/labels-1.json` and `/api/fine/labels-2.json`. An active pointer with `labelsRoot` selects the versioned files; otherwise it serves the original label files. Responses expire after sixty seconds. Existing immutable `/data/v6/labels-*.json` URLs remain untouched. The actual Worker fixture passed activation and rollback for both files and checked every coordinate against the source catalogue.

`CANONICAL_COORDINATES.patch` is an **unapplied** patch for review. It changes the map's label-fetch URL and the eighteen hardcoded home/reference coordinates. It does not restyle the interface. Arbitrary numeric coordinates continue to select their own exact pixel; there is no city-name lookup or coordinate snapping in the place API.

Build:

```sh
python3 research/fine/canonical_labels.py \
  --places artifacts/places.tsv --legacy-labels artifacts/v6 \
  --output '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/v6-fine-b1-20261018/labels'
```

Measure the actual routes and monthly differences:

```sh
devenv shell -- node research/fine/canonical_coordinates_proof.mjs \
  '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data' \
  '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/b1-worldwide/overlay' \
  '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/b1-worldwide/coordinates.json'
```

Release activation must declare `labelsRoot: "v6-fine-b1-20261018/labels"` after these files and the matching Worker/app code are present. The UI patch needs the owner's scope approval before application.
