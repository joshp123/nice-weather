Written by AI (GPT-6.1 Sol), 2026-10-17.

# Daily outlooks

The forecast command renders tomorrow's World, Europe and USA outlooks in its
own Go process. No browser, font service or extra cloud package is required.
The existing daily command and temporary forecast upload grant are sufficient.
There is no social posting code or social credential.

## Selection and rendering

Tomorrow means the next UTC calendar date. The forecast record for that date
describes each place's local 10:00–21:00 weather. The forecast day is calculated
from `firstDate`, rather than assuming it is always day 1.

Candidates are named places with at least 10,000 residents in `places.tsv`.
Palestine places are excluded. Slugs and English names follow the Worker.
The same row-major nearest land point within ±2 fine grid points supplies the
score; sea and missing points are omitted. Rank by the published whole-number
default score, then population, then slug. Select three at least 250 km apart.
This deliberately resolves equal displayed scores in favour of larger places.

USA means the 50 states and Washington, DC, including Alaska and Hawaii insets.
Europe uses European countries plus Turkey and Cyprus, within 25° W–45° E and
30–72° N; this excludes overseas territories. World uses all eligible places.

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
corrected single-point, floored score images from the current runner.

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
