Written by AI on 2026.10.17 by model GPT-6.1 Sol.

# Held-out daily-extrema evidence

All errors below are mean absolute error (MAE), °C, on paired QA-passing station observation dates. “Strict” requires eight complete month-years (each at least 80% daily coverage) in 2016–2025. Complete-year tables require all twelve strict months and use the same station-months for every listed method. No per-station method blending or further fitting was performed. Gridded products may contain these stations in their original historical interpolation; only our residual interpolation has explicit spatial exclusion.

## Recommendation from the daily instruments

Use a fixed availability-only source policy: PRISM in its contiguous-US coverage; otherwise exact WorldClim 1.4, then its tested centred 5×5 valid-pixel mean, then CHELSA, then native ERA5 with a coarse-data flag. Apply available-period mean-anomaly high/low offsets, using their averaged uniform offset on the rare day when corrected extrema would cross. This keeps normal daily anomalies and avoids the affine method's strong range amplification. No source is selected by its result at an individual test station.

On the same thirteen complete strict stations, that practical ordered policy has **0.64/0.72 °C high/low MAE**, versus **2.31/1.01 °C raw ERA5** and **2.05/1.36 °C the current algorithm**. The expensive historical-extrema WorldClim 1.4 reference is 0.61/0.72 °C. In the five-station contiguous-US cohort, the practical PRISM policy is 0.56/0.76 °C versus 3.04/2.11 °C current. Exact WorldClim pixels modestly outperform 5 km averaging on the common cohort.

Do not substitute the private global GHCN fit: its rights are unresolved and its errors are worse on the shared private-benchmark cohort. The source-filtered US residual fit also has much worse lows than PRISM (1.63 versus 0.74 °C). The separate held-out 21:00 observations must still validate the chosen hourly transform; daily TMIN does not establish walk-home accuracy.

The strict global cohort has only thirteen complete stations, concentrated in Europe, the US and Australia. Lima, Valparaíso, Cape Town, Funchal, Las Palmas, Nairobi, Bangkok and uncertain Chania need their separate sparse/hourly evidence. These daily results do not prove globally uniform accuracy. Agadir has no usable daily-instrument record and is validated only through the labelled airport hourly proxy.

## Global eligible methods

TMAX: 13 stations, 156 station-months; TMIN: 13 stations, 156 station-months.

| Method | High MAE °C | Low MAE °C |
|---|---:|---:|
| Fixed regional/global source policy, ordered | 0.64 | 0.72 |
| Raw ERA5 | 2.31 | 1.01 |
| Current algorithm, 24h | 2.05 | 1.36 |
| CHELSA: historical extrema | 1.73 | 1.06 |
| CHELSA 5 km: historical extrema | 1.74 | 1.06 |
| WorldClim 1.4: historical extrema | 0.61 | 0.72 |
| WorldClim 1.4 5 km: historical extrema | 0.62 | 0.75 |
| WorldClim 1.4: available mean, dual | 0.77 | 0.77 |
| WorldClim 1.4: available affine | 0.81 | 0.80 |
| WorldClim 1.4: available ordered | 0.77 | 0.77 |
| WorldClim 1.4 5 km: available mean, dual | 0.78 | 0.80 |
| WorldClim 1.4 5 km: available affine | 0.82 | 0.83 |
| WorldClim 1.4 5 km: available ordered | 0.78 | 0.80 |
| CHELSA: available mean, dual | 1.83 | 1.07 |
| CHELSA: available affine | 1.81 | 1.08 |
| CHELSA: available ordered | 1.83 | 1.07 |
| CHELSA 5 km: available mean, dual | 1.84 | 1.06 |
| CHELSA 5 km: available affine | 1.82 | 1.08 |
| CHELSA 5 km: available ordered | 1.84 | 1.06 |

## Contiguous-US regional comparison

TMAX: 5 stations, 60 station-months; TMIN: 5 stations, 60 station-months.

| Method | High MAE °C | Low MAE °C |
|---|---:|---:|
| Fixed regional/global source policy, ordered | 0.56 | 0.76 |
| Raw ERA5 | 1.70 | 1.28 |
| Current algorithm, 24h | 3.04 | 2.11 |
| WorldClim 1.4: historical extrema | 0.71 | 0.83 |
| WorldClim 1.4: available mean, dual | 0.89 | 0.88 |
| WorldClim 1.4: available affine | 0.86 | 0.92 |
| WorldClim 1.4: available ordered | 0.89 | 0.88 |
| PRISM: historical extrema | 0.54 | 0.74 |
| PRISM: available mean, dual | 0.56 | 0.76 |
| PRISM: available affine | 0.60 | 0.77 |
| PRISM: available ordered | 0.56 | 0.76 |
| US-government-origin station fit | 0.64 | 1.63 |

## Private benchmark on its available common cohort

TMAX: 8 stations, 96 station-months; TMIN: 8 stations, 96 station-months.

| Method | High MAE °C | Low MAE °C |
|---|---:|---:|
| Raw ERA5 | 2.75 | 0.85 |
| Current algorithm, 24h | 1.80 | 1.04 |
| WorldClim 1.4: historical extrema | 0.56 | 0.62 |
| WorldClim 1.4: available mean, dual | 0.70 | 0.68 |
| WorldClim 1.4: available affine | 0.77 | 0.72 |
| WorldClim 1.4: available ordered | 0.70 | 0.68 |
| WorldClim 2.1 private: historical extrema | 0.49 | 0.60 |
| Private foreign-station fit | 1.08 | 0.73 |

## Per-station strict months

The following tables use each station’s available strict months. H/L rows can have different month counts. Captured published highs are diagnostic comparisons against the 2016–2025 observations: the live product uses five discontinuous 360-day samples. Current 24h uses the current source/height algorithm on paired dates; current 10–20 uses its actual daytime window and therefore differs from station 24h TMAX. TMIN is a dawn/extreme measure, not 21:00 felt temperature.

### TMAX

| Station | Months | Fixed regional/global source policy, ordered | Raw ERA5 | Current algorithm, 24h | Current algorithm, 10–20 | Published high | WorldClim 1.4: historical extrema | WorldClim 1.4: available mean, dual | WorldClim 1.4: available affine | WorldClim 1.4: available ordered | WorldClim 2.1 private: historical extrema |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Málaga | 12 | 0.49 | 3.00 | 0.77 | 0.78 | 0.74 | 0.40 | 0.49 | 0.49 | 0.49 | 0.15 |
| Tenerife South | 12 | 1.14 | 5.31 | 3.77 | 3.81 | 3.76 | 1.10 | 1.14 | 1.52 | 1.14 | 0.44 |
| Sydney | 12 | 0.43 | 3.50 | 0.99 | 1.02 | 1.00 | 0.42 | 0.43 | 0.48 | 0.43 | 0.27 |
| Perth | 12 | 1.18 | 2.01 | 1.09 | 1.12 | 0.92 | 0.98 | 1.18 | 1.18 | 1.18 | 0.65 |
| Honolulu | 12 | 0.21 | 4.10 | 1.25 | 1.25 | 1.44 | 0.21 | 0.21 | 0.29 | 0.21 | 0.61 |
| Rotterdam | 12 | 0.85 | 0.82 | 0.82 | 0.94 | 0.66 | 0.53 | 0.86 | 0.86 | 0.85 | 0.10 |
| London Heathrow | 12 | 0.60 | 1.82 | 1.82 | 1.91 | 1.71 | 0.39 | 0.60 | 0.65 | 0.60 | 0.16 |
| Marrakech | 4 | 1.28 | 3.76 | 3.76 | 3.77 | 3.59 | 1.16 | 1.28 | 1.27 | 1.28 | 1.31 |
| San Francisco Downtown | 12 | 0.83 | 1.40 | 3.87 | 3.86 | 4.11 | 0.49 | 0.71 | 0.65 | 0.71 | 1.55 |
| San Diego | 12 | 0.72 | 2.14 | 1.05 | 1.05 | 0.82 | 0.23 | 0.33 | 0.41 | 0.33 | — |
| Los Angeles coastal airport | 12 | 0.25 | 1.07 | 3.65 | 3.64 | 4.15 | 2.09 | 2.21 | 2.13 | 2.21 | — |
| Santa Barbara | 12 | 0.75 | 1.64 | 4.39 | 4.38 | 4.90 | 0.54 | 0.75 | 0.68 | 0.75 | — |
| Seville | 12 | 0.68 | 0.97 | 0.97 | 0.98 | 1.05 | 0.35 | 0.68 | 0.71 | 0.68 | — |
| Las Vegas | 12 | 0.22 | 2.27 | 2.27 | 2.30 | 2.09 | 0.17 | 0.45 | 0.45 | 0.45 | — |

### TMIN

| Station | Months | Fixed regional/global source policy, ordered | Raw ERA5 | Current algorithm, 24h | WorldClim 1.4: historical extrema | WorldClim 1.4: available mean, dual | WorldClim 1.4: available affine | WorldClim 1.4: available ordered | WorldClim 2.1 private: historical extrema |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Málaga | 12 | 0.78 | 1.26 | 0.97 | 0.62 | 0.78 | 0.77 | 0.78 | 0.97 |
| Tenerife South | 12 | 0.33 | 0.60 | 2.10 | 0.33 | 0.33 | 0.52 | 0.33 | 0.27 |
| Las Palmas | 1 | 0.88 | 2.55 | 0.17 | 0.79 | 0.88 | 0.77 | 0.88 | 0.81 |
| Funchal | 3 | 0.80 | 2.04 | 0.11 | — | — | — | — | — |
| Sydney | 12 | 0.88 | 1.79 | 0.60 | 0.97 | 0.88 | 1.03 | 0.88 | 0.80 |
| Perth | 12 | 0.47 | 0.38 | 1.30 | 0.39 | 0.47 | 0.47 | 0.47 | 0.48 |
| Honolulu | 12 | 1.40 | 1.32 | 0.74 | 1.42 | 1.40 | 1.25 | 1.40 | 0.71 |
| Rotterdam | 12 | 0.65 | 0.71 | 0.71 | 0.39 | 0.65 | 0.64 | 0.65 | 0.26 |
| London Heathrow | 12 | 0.73 | 0.43 | 0.43 | 0.62 | 0.73 | 0.81 | 0.73 | 0.36 |
| San Francisco Downtown | 12 | 0.42 | 0.32 | 1.50 | 0.20 | 0.23 | 0.26 | 0.23 | 0.96 |
| San Diego | 12 | 0.43 | 0.98 | 0.54 | 0.73 | 0.58 | 0.73 | 0.58 | — |
| Los Angeles coastal airport | 12 | 0.34 | 0.21 | 0.61 | 0.33 | 0.42 | 0.42 | 0.42 | — |
| Santa Barbara | 12 | 0.98 | 2.14 | 5.12 | 0.64 | 0.76 | 0.74 | 0.76 | — |
| Seville | 12 | 0.38 | 0.25 | 0.25 | 0.45 | 0.38 | 0.38 | 0.38 | — |
| Las Vegas | 12 | 1.63 | 2.77 | 2.77 | 2.27 | 2.43 | 2.42 | 2.43 | — |

## Diagnostics and excluded coverage

| Station | Strict high/low months | All-observation high/low days | Note |
|---|---:|---:|---|
| Las Palmas | 0 / 1 | 186 / 1456 | Exposed island airport |
| Funchal | 0 / 3 | 197 / 1633 | Sheltered island slope |
| Lisbon | 0 / 0 | 1660 / 1726 | Atlantic estuary coast |
| Cape Town | 0 / 0 | 1087 / 1022 | Inland airport on coastal plain |
| Nairobi | 0 / 0 | 384 / 636 | Highland city |
| Lima | 0 / 0 | 775 / 1382 | Desert fog coast |
| Marrakech | 4 / 0 | 1992 / 1221 | Hot interior plain below mountains |
| Valparaíso | 0 / 0 | 212 / 220 | Incomplete strict month coverage |
| Agadir | 0 / 0 | 0 / 0 | Incomplete strict month coverage |
| Chania | 0 / 0 | 1810 / 1607 | DWD/GCOS2024 WMO16746 coordinate used as modern-location assumption; instrument identity/location not proven exact. HNMS March2025 lists35.55,24.12. Historical moves unresolved; Chania daily data diagnostic-only because coverage fails. |
| Bangkok | 0 / 0 | 1301 / 1661 | Incomplete strict month coverage |

Full monthly values, flags and product-period coverage: monthly_comparison.csv. Complete station/method/cohort errors: station_method_summary.csv. Exact period definitions and live/date hashes: comparison_metadata.json. WorldClim 2.1 and foreign GHCN residual predictions are private benchmarks only. US-government residual is separately source-filtered, rather than blanket clearance for NOAA-distributed data.

## Separate sparse/proxy diagnostics

These are monthly comparisons using all available QA observations, including months that fail strict coverage. They do not enter the recommendation tables. Each high/low pair below is MAE °C; a missing value means no comparable observations or anchor.

| Station | Months high/low | Raw high/low | Current 24h high/low | WC1.4 reference high/low | WC1.4 available mean high/low | WC1.4 available affine high/low | WC1.4 available ordered high/low |
|---|---:|---:|---:|---:|---:|---:|---:|
| Las Palmas | 12 / 12 | 3.37 / 2.17 | 0.99 / 0.33 | 1.03 / 0.61 | 0.89 / 0.59 | 1.28 / 0.64 | 0.89 / 0.59 |
| Funchal | 12 / 12 | 3.89 / 1.94 | 1.77 / 0.22 | — / — | — / — | — / — | — / — |
| Lisbon | 12 / 12 | 2.74 / 0.66 | 2.30 / 0.71 | 0.34 / 0.92 | 0.59 / 0.84 | 0.62 / 0.99 | 0.59 / 0.84 |
| Cape Town | 12 / 12 | 4.70 / 2.38 | 1.28 / 0.82 | 1.39 / 0.67 | 1.50 / 0.53 | 1.63 / 0.55 | 1.50 / 0.53 |
| Nairobi | 12 / 12 | 0.89 / 1.55 | 0.89 / 1.55 | 0.65 / 0.25 | 0.78 / 0.30 | 0.90 / 0.44 | 0.78 / 0.30 |
| Lima | 12 / 12 | 1.73 / 0.36 | 3.59 / 2.92 | 0.78 / 1.24 | 0.38 / 1.47 | 0.38 / 1.58 | 0.38 / 1.47 |
| Marrakech | 12 / 12 | 3.62 / 1.88 | 3.62 / 1.88 | 0.91 / 0.80 | 1.07 / 0.75 | 1.12 / 0.77 | 1.07 / 0.75 |
| Valparaíso | 8 / 8 | 0.33 / 2.49 | 5.46 / 2.77 | 1.30 / 1.20 | 1.21 / 1.16 | 1.22 / 1.42 | 1.21 / 1.16 |
| Agadir | 0 / 0 | — / — | — / — | — / — | — / — | — / — | — / — |
| Chania | 12 / 12 | 2.99 / 3.11 | 2.05 / 3.97 | 0.28 / 0.97 | 0.28 / 0.95 | 0.79 / 1.08 | 0.28 / 0.95 |
| Bangkok | 12 / 12 | 0.19 / 0.37 | 0.19 / 0.37 | 0.23 / 0.60 | 0.32 / 0.83 | 0.31 / 0.88 | 0.32 / 0.83 |

## Daily range inversion audit

Independent high/low additive corrections can make the corrected daily high no warmer than its low. This audit uses complete interior local days from continuous native histories, excluding endpoint fragments. It covers 2016–2025 separately from captured published dates; the table shows 2016–2025. Positive affine scaling produced zero inverted ranges throughout these samples.

| Anchor | Sites | Additive inverted days / total | Affine inverted days |
|---|---:|---:|---:|
| worldclim14 | 23 | 12 / 84019 | 0 |
| worldclim14_5km | 24 | 12 / 87672 | 0 |
| prism | 5 | 0 / 18265 | 0 |
| chelsa | 24 | 59 / 87672 | 0 |

WorldClim 1.4 exact-pixel inversions occur in Rotterdam (11) and Seville (1), with none at San Francisco or any US site. PRISM has none at the five US sites. CHELSA has 33 inversions in Santa Barbara. Affine scaling avoids inversion but can magnify individual-day swings: maximum monthly scaling is 5.87 at uncertain Chania, 5.42 in Honolulu and 4.10 at Tenerife South. See range_inversion_audit.csv for every station/month, minimum corrected range and ratio.

The ordered rule repairs the same days counted as additive inversions above and leaves **zero ordered inversions** for every tested source. At WorldClim 1.4 exact pixels this is 12/84,019 days; at PRISM it is 0/18,265. Per-month paired-observation repair counts remain in monthly_comparison.csv, with complete-day counts in range_inversion_audit.csv.

## Fog-coast examples

Paired 2016–2025 observed 24h monthly highs versus the ordered source-policy candidate. Published high is the captured five-chunk daytime card value, so its dates and hours differ; values are °C. Lima remains a sparse diagnostic.

| Station/month | Observed | Raw ERA5 | Captured published high | Ordered policy |
|---|---:|---:|---:|---:|
| San Francisco Downtown 6 | 19.10 | 17.82 | 27.30 | 20.13 |
| San Francisco Downtown 7 | 18.79 | 17.99 | 27.30 | 20.12 |
| San Francisco Downtown 8 | 20.13 | 19.08 | 27.30 | 21.28 |
| Lima 6 | 20.20 | 19.68 | 25.70 | 19.89 |
| Lima 7 | 19.47 | 19.21 | 25.70 | 19.21 |
| Lima 8 | 19.05 | 18.99 | 25.70 | 18.90 |
| Los Angeles coastal airport 6 | 22.13 | 23.82 | 28.30 | 22.44 |
| Los Angeles coastal airport 7 | 24.10 | 26.65 | 31.30 | 24.41 |
| Los Angeles coastal airport 8 | 25.11 | 27.45 | 32.30 | 25.21 |
| Santa Barbara 6 | 21.82 | 23.44 | 29.70 | 21.35 |
| Santa Barbara 7 | 23.27 | 25.59 | 32.00 | 22.22 |
| Santa Barbara 8 | 24.39 | 26.01 | 32.00 | 23.18 |

Rerun the base comparison with `sh research/fine/run.sh compare.py`. The same composite is available as a reduction of an existing monthly CSV with `sh research/fine/run.sh compare.py --source-policy-only`; it does not refetch or refit anything. The current ordered run took 188.05 seconds for 23 daily stations and 544 extrema-months, plus the cached range audit. Source-policy aggregate counts are in source_policy_summary.json.
