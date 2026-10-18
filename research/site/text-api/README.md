<!-- Written by AI on 2026.10.18 by model GPT-6.1 Sol. -->

The place text API accepts latitude, longitude and the same period word or date as a page link:

`/api/place?lat=-6.77&lon=-79.84&period=nov-mar`

The response identifies the requested point and the named place whose card it describes. `card` contains the displayed word, score, whole-day counts, sentence, and the reasons with both physical figures and the exact display text. The app card and API import the same reason presentation function. Existing `judgement` and `months` fields remain available to the engine.

The quoted live URL without a period returned Chiclayo correctly when checked on 18 October. Missing and empty coordinate parameters did previously become zero through `Number(null)` or `Number('')`; they now return 400. Unknown periods return 400, and dates outside the available forecast return 404.

Run the app locally with its active named-place overlay, then run `check.py` headlessly through Playwright. It compares the API with rendered cards for Chiclayo, Chania, Málaga and London across the year, November to March, the 14-day forecast and a dated day. Captures and the detailed report are written to `/tmp/nwf-agents/round5/text-api/`.

All 16 card comparisons passed, including 112 reason rows. Seven invalid requests returned 400. No browser errors. `npm run build` passed. The API changes and figures do not change scoring, bands or weather data.
