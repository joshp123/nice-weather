Written by AI on 2026.10.18 by model GPT-6.1 Sol.

# One image for sharing

`hosting/sites/share-template.mjs` owns the 1200 × 630 composition used by
link previews, Copy image and Make a poster. It draws only the map, the place
or region, the dates, the score and whole-day counts when a place is selected,
the colour key, Nice! and the address. It never captures DOM controls.

The browser gives the template its current map canvas. A place is kept in the
crop. Phone captures temporarily increase the map's drawing resolution, then
restore it, so the exported map stays sharp without changing the screen layout.
The Worker gives it a world map made from the existing typical-day score
PNG and the existing outlook land and border outlines. No weather files,
scoring curves, score bands or publishing steps are changed.

Every normal app link, including the home page and a place's default forecast,
advertises `/preview/share.png?path=...`. The route supplies the forecast's
explicit dates. Named and coordinate links use the same shared place judgement
as their card. Sample counts name their sample denominator.

The preparation scripts run in this order: `export-brand.py`, `prepare-assets.py`,
then `export-flags.py`, with Playwright and Pillow available in the devenv.

The template uses the existing Barlow atlas, an export of the app's existing
Nice! wordmark and country flag artwork. Its module loads only when a browser
makes an image, so these assets add no first-view bytes. The same wordmark is
used on outlook pages and newly rendered outlook images.

Run the actual-browser proof with the local server running:

```
devenv shell -- sh -c 'cd /tmp && uv run --quiet --with playwright python -I /Users/josh/code/nice-weather-finder/research/site/sharing/proof.py'
```

It fetches the preview in real page metadata for home, four forecast places,
coordinates, a typical year, a winter and an absolute date. It makes a poster
at 390 × 844, 1512 × 982 and 3840 × 1080, writes each PNG and screenshot, and
compares the clipboard PNG's pixels with the poster's pixels. Evidence goes
under `/tmp/nwf-agents/round5/sharing/`.

On 18 October 2026, all nine preview requests returned PNGs with status 200.
All three actual clipboard images matched their poster's pixels, with no
browser errors. The exported images were opened and checked. Stored outlook
PNGs receive the shared wordmark when the existing forecast runner next
renders them; this change does not republish weather data.
