Written by AI (GPT-6.1 Sol), 2026-10-18.

# Daily outlooks

The forecast command renders tomorrow's World, Europe and USA outlooks in its
own Go process. No browser, font service or extra cloud package is required.
The existing daily command and temporary forecast upload grant are sufficient.
There is no social posting code or social credential.

## Selection and rendering

Tomorrow means the next UTC calendar date. The forecast record for that date
describes each place's local 10:00–21:00 weather. The forecast day is calculated
from `firstDate`, rather than assuming it is always day 1.

World candidates have at least 300,000 residents in `places.tsv`; Europe and
USA candidates have at least 10,000.
Palestine places are excluded. Slugs and English names follow the Worker.
The daily runner writes `named.bin.gz` for every distinct place-list coordinate,
without a population cut-off. The app, Worker and outlook ranking read the same
canonical scores and nice/perfect/awful shares. Fourteen day slots and a separate
full-stretch slot preserve the existing median-before-spatial-blend rule.
The producer judges actual forecast `days.bin`, geometric cells and monthly felt
offsets with `internal/placenumbers.PlaceNumbers`, the Go port of the shared
app/Worker function, then applies the same normalized 4 × 4 B-spline footprint.
A retained run without the compact file still uses that calculation directly. The score image supplies only the painted overview. Rank by
nice days plus perfect days, then population, then slug. A perfect day counts twice. Select three at least 250 km apart.
Equal totals favour larger places.
Name each winning spot after the largest eligible place within 50 km whose score
is within five points. Both winning spots and the named places stay at least
250 km apart. The poster shows the named place’s own score and location.

USA means the 50 states and Washington, DC, including Alaska and Hawaii insets.
Europe includes European countries, Cyprus, Malta, the Canaries, Madeira and
the Azores. Turkey is limited to Thrace, west of the Bosporus, Marmara and
Dardanelles. Spanish cities in North Africa are excluded. The regional bounds
(32° W–40° E, 27–72° N) exclude other overseas territories. World uses all
eligible places. The Europe map extends south to include the Canaries and fades
at the eastern edge. Every poster identifies a single forecast day and its date.

The map uses a cubic B-spline over the score grid, with sea omitted, then the
site's six bands. Smoothing affects display only. Natural Earth land rings mask
the sea; its country boundaries and US state lines provide context. All poster
colours, type sizes, fonts, spacing and map frames live in `style.go`.
[Asset provenance and licences](assets/README.md) include reproducible preparation.

Each run contains nine outlook files: three manifests, three 1200 × 630 PNG
previews and three 2400 × 1260 PNG posters. All upload before the forecast's
`current.json` pointer. The pages read that same pointer, and withhold a stale
outlook rather than calling an old date tomorrow. Copy post uses a dated URL so
each day's link has its own social preview cache key.

## Render an existing forecast

Inside `devenv shell`:

```sh
go run ./tools/outlook --out /tmp/weather-outlook-check
go run ./tools/outlook --source ./artifacts --out /tmp/weather-outlook-check
```

`--source` accepts a data URL or artifacts directory. `--places-source` can read
place names from a different root, such as the public Site when the local
forecast folder has no place data. `--today YYYY-MM-DD` replays a publication
date. None of these commands upload anything. The source must contain the
forecast records and score images from the current runner. `--static-source`
selects the matching v6 static directory or URL; it defaults to
`<places-source>/v6`. The daily runner passes its existing `--v6-static` value
explicitly, so its named places and records use the same static grid.

To prepare only the compact named forecast for an existing run, without a new
download or publication:

```sh
go run ./cmd/niceweather named-forecast --source ./artifacts \
  --places-source ./artifacts --v6-static ./artifacts/v6 --out /tmp/named-forecast
```

The shared function supports both fine typical-year climate and the existing
compact forecast records. Outlooks remain one-day ECMWF forecasts. No ERA5
climatology correction is applied to forecast weather.

## Posting options, checked 17 October 2026

X's current API is prepaid pay-per-use, without the old Free/Basic/Pro posting
tiers. A [post containing a URL costs $0.200](https://docs.x.com/x-api/getting-started/pricing),
so three outlook links each day cost $18 per 30 days before other calls and tax.
An ordinary post without a URL costs $0.015; three daily would cost $1.35.
The [$20 introductory credit now requires a first purchase](https://docs.x.com/x-api/getting-started/free-credits),
so it is not a zero-spend route. The current [rate table](https://docs.x.com/x-api/fundamentals/rate-limits)
allows 100 post requests per user per 15 minutes and 10,000 per app daily.
[Photos](https://docs.x.com/x-api/media/introduction) may be up to 5 MB.
Josh would need to choose an account, authorise a developer app through OAuth,
buy credits and choose a spending limit. Manual posting remains an option.

[Bluesky's API is free](https://bsky.network/). Its
[write limit](https://bsky.network/docs/rate-limits/) is 5,000 points per hour
and 35,000 daily; creating a post costs three points. Josh would choose an account
and authorise OAuth or an App Password. Posts allow 300 graphemes and four photos.
The current [English posting tutorial](https://docs.bsky.app/docs/tutorials/creating-a-post)
allows 2 MB per image; all these generated posters are below 1 MB.
An automated client must upload a thumbnail and provide an external-card embed
to produce a [link preview](https://bsky.network/ja/docs/about-bluesky-content/posts/);
putting the URL in API post text alone does not create the card.

No account, key, authorisation, post or purchase was created for this work.
