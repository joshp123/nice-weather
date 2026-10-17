Written by AI on 2026.10.18 by model GPT-6.1 Sol.

The selected exact-pixel climate model keeps the existing ordered high/low
temperature correction from 10:00 through 20:00. At 21:00 it adds the uniform
shift `(highOffset + lowOffset) / 2` to native air temperature. Both offsets
come from the same exact 30 arc-second pixel and month.

The shared `placeNumbers` and `placeMonths` calculations use that air value
to recompute the felt-temperature vapour cap. The humidity nuisance uses the
mean of `min(native dew, corrected air)` over all twelve hours, including
the revised 21:00 value. Native dew, wind, rain and radiation are retained.
Monthly native-dew facts remain unchanged.

Every new release must declare `"eveningModel": "uniform"` in both
`v6/fine/current.json` and its native replay `manifest.json`. The loader
passes that single global choice into every place packet and rejects a
pointer/manifest disagreement. There are no place-specific choices.

The module also accepts `ordered` and `native` for comparison diagnostics.
`native` applies no temperature shift at 21:00. Old diagnostic packets with
no model field retain their original ordered behaviour. An absent fine
pointer preserves the existing production product. This source change
does not create or activate a production pointer.

The revised evening choice changes neither correction tile bytes nor the
10:00–20:00 correction. All 156 monthly highs across the thirteen-city
cohort were exactly equal for ordered, uniform and native evening variants.
An independent calculation of both alternatives matched all 312 monthly
felt21 values within 0.000000000000002 °C. Representative 14:00 felt
temperatures were also exactly unchanged.
