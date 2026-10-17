Written by AI on 2026.10.18 by model GPT-6.1 Sol.

The shared climate calculation is `placeNumbers` in `fine-place.mjs`. The Worker,
app engine and a JavaScript poster producer import this same function. It reads
one exact 30 arc-second pixel and replays the nearest own native ERA5 point with
independent monthly high/low corrections. It uses the ordered exception when the
corrected daily range would collapse, the physical saturation cap for felt
humidity, and native dew point observations. It does not substitute inland
weather or move the temperature to sea level.

The weighted median band and perfect/nice-or-better/awful counts use the existing
representative dates, eight per month, weighted by month length. The score uses
all twelve hourly temperatures from 10:00 to 21:00. The monthly high and 21:00
facts use every assembled day. Monthly dew facts report uncapped native dew;
the humidity factor uses the mean of `min(native dew, corrected air)`.
The card's explanation retains the representative-day means used by its score.

`createFinePlaceLoader(readBytes)` accepts a storage reader returning bytes or
null. Its `enabled()` reads `v6/fine/current.json`. An absent pointer retains the
existing product. With a pointer present, missing native data or an unsupported
fine pixel is unavailable; it never silently substitutes the old quarter-degree
numbers. The pointer contains `nativeRoot`, `tileRoot`, and `status`. The native
root's manifest lists `nativeFineIndices` and the exact correction `generation`.
Native replay files are gzip JSON at `native/<fineIndex>.json.gz`. Correction
files are `tiles/MM/RRR/CCC.nwf.gz` under the tile root. The loader checks their
pixel address, payload size, reference support, and generation. It caches shared
native points and correction tiles, rather than duplicating requests per city.

The current data is an explicitly rejected thirteen-place diagnostic. Its pointer
must remain local. No worldwide native replay carrier or accepted publication
bundle exists. The previous 25 km overview stays in use.

The Worker place API accepts `period=july`, another month, a month range, or
`year`, and returns the exact monthly facts plus the default-taste judgement.
Link preview text uses that same calculation. When the fine pointer is present,
old prebuilt climate preview images are suppressed because they contain previous
numbers. Images must be regenerated before publishing an accepted candidate.

For a JavaScript climate poster or named-place list:

```js
import {createFinePlaceLoader, placeNumbers} from './fine-place.mjs';
const loader = createFinePlaceLoader(readBytes);
const packet = await loader.load(place.latitude, place.longitude);
const judgement = packet ? placeNumbers(packet, period, taste) : null;
```

The existing Go forecast/outlook producer does not call this module. It also has
no native forecast point carrier, so the new climate data cannot truthfully
supply a one-kilometre forecast. Forecast cards, rankings, posters and previews
retain the current model. Completing them requires a native-point forecast
carrier and an actual producer integration; this work does not claim that they
have one-kilometre detail.
