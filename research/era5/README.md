Written by AI on 2026.10.18 by model claude opus 5.5.

# ERA5 hourly download

`fetch_era5.py <chunk> <processes>` downloads one ERA5 "chunk year" (8,736 hours) of the six fields Nice Weather needs (2 m temperature and dew point, 10 m wind, total precipitation, direct radiation) for every 3° land tile, from Earthmover's free, anonymous public ERA5 archive (CC BY 4.0). It is resumable: finished tiles are skipped. `research/climate-v6` turns the downloaded chunks into the published typical-year files.
