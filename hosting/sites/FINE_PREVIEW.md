Written by AI on 2026.10.18 by model GPT-6.1 Sol.

# Corrected climate link images

When fine climate is enabled, named and coordinate place URLs advertise
`/preview/fine.png?path=<app path>&generation=<data generation>&model=<evening model>&style=1`.
The image route resolves the same place and period and calls the Worker's
`defaultPeriodJudgement`, which calls shared `placeNumbers`. It renders the
typical-day band, score, and nice-or-better, perfect and awful day counts.
Scores are floored and counts rounded, as in the preview text. No city-period
image collection needs to be stored or uploaded. Forecast and world overview
images keep their existing path.

The renderer produces a 1200×630 RGB PNG using native compression streams.
Its font is a precomputed antialiased Barlow alpha atlas. It needs no runtime
package, font service or account. The existing font's Latin coverage is used;
place names outside that coverage use the place catalogue's plain-name spelling.
The PNG carries the exact input numbers and displayed labels in a `NiceWeather`
text chunk, making route parity inspectable without OCR. Old generation or evening-model requests
redirect to the active generation; images are publicly cacheable for one day.

Barlow: Copyright 2017 The Barlow Project Authors
(https://github.com/jpt/barlow), SIL Open Font License 1.1.
The full copyright and licence accompany the distributed atlas in
`fine-preview-font.mjs`; the source licence is `outlook/assets/OFL.txt`.
Regenerate the atlas from the existing fonts with:

```
devenv shell -- go run ./research/fine/preview-font
```

Exercise real page metadata, PNG and API values for the thirteen city points:

```
devenv shell -- node research/fine/fine_preview_proof.mjs \
  '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data' \
  '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/b1-worldwide/overlay' \
  '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/b1-worldwide/previews'
```

The report is `parity.json` beside the thirteen PNGs. July's thirteen score,
three day counts and total days match `/api/place` exactly, as do the rendered
integer labels and page description. Named URLs, May, Dec–Feb, the year and stale
generation redirect are exercised. San Francisco and Funchal images were opened
and visually checked. Images are about 28–34 kB. This is local Worker execution;
the remote Sites runtime has not been deployed or measured.
