# Written by AI on 2026.10.17 by model claude opus 5.5.
# Downloads one ERA5 "chunk year" (8736 hours) of the six fields Nice Weather needs, for every 3° land tile
# (12 x 12 cells of 0.25°) from the free Earthmover archive, and keeps them on the SSD in plain units.
# Usage: fetch_era5.py <chunk index> <processes>. Resumable: finished tiles are skipped.
import os, sys, time, pathlib, multiprocessing as mp
import numpy as np, icechunk, zarr
OUT = pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/era5')
HOURS = 8736
FIELDS = {'t2m': lambda a: a - 273.15, 'd2m': lambda a: a - 273.15, 'u10': lambda a: a, 'v10': lambda a: a,
          'tp': lambda a: a * 1000.0, 'fdir': lambda a: a / 3600.0}  # °C, °C, m/s, m/s, mm per hour, W/m² (hour mean)

def archive():
    storage = icechunk.s3_storage(bucket='earthmover-icechunk-era5', prefix='icechunkV2', region='us-east-1', anonymous=True)
    return zarr.open_group(icechunk.Repository.open(storage).readonly_session(snapshot_id='TZ2KX6A24CJ5DG17T7MG').store, mode='r', path='single/temporal')

def land_tiles(group):
    lsm = group['lsm'][:]; lat = group['latitude'][:]
    tiles = []
    for r in range(61):
        for c in range(120):
            rows = slice(r * 12, min(r * 12 + 12, 721))
            if lat[rows].max() < -60: continue  # Antarctica
            if (lsm[rows, c * 12:c * 12 + 12] > 0.05).any(): tiles.append((r, c))
    return tiles

PROCESS_ARCHIVE = None
def open_process_archive():
    global PROCESS_ARCHIVE
    PROCESS_ARCHIVE = archive()  # once per process: opening it downloads the archive's index

def work(args):
    chunk, tiles = args
    group = PROCESS_ARCHIVE; start = chunk * HOURS; folder = OUT / f'chunk{chunk}'
    for r, c in tiles:
        path = folder / f'r{r:02d}c{c:03d}.npz'
        if path.exists(): continue
        rows, cols = slice(r * 12, min(r * 12 + 12, 721)), slice(c * 12, c * 12 + 12)
        for attempt in range(6):  # the connection drops now and then; a tile is worth retrying
            try:
                arrays = {name: convert(group[name][start:start + HOURS, rows, cols]).astype(np.float16) for name, convert in FIELDS.items()}
                break
            except Exception as error:
                if attempt == 5: raise
                print(f'retrying r{r}c{c} after {type(error).__name__}', flush=True); time.sleep(10 * (attempt + 1))
        tmp = path.with_suffix('.partial.npz'); np.savez_compressed(tmp, **arrays); os.replace(tmp, path)
    return len(tiles)

if __name__ == '__main__':
    chunk, processes = int(sys.argv[1]), int(sys.argv[2])
    group = archive(); tiles = land_tiles(group)
    (OUT / f'chunk{chunk}').mkdir(parents=True, exist_ok=True)
    first = np.datetime64('1940-01-01T00') + np.timedelta64(chunk * HOURS, 'h')
    print(f'chunk {chunk}: {first} to {first + np.timedelta64(HOURS, "h")}, {len(tiles)} land tiles', flush=True)
    np.save(OUT / 'static_lsm.npy', group['lsm'][:]); np.save(OUT / 'static_z.npy', group['z_sfc'][:])
    np.save(OUT / 'latitude.npy', group['latitude'][:]); np.save(OUT / 'longitude.npy', group['longitude'][:])
    batches = [(chunk, tiles[i::processes * 8]) for i in range(processes * 8)]
    started = time.time(); done = 0
    with mp.Pool(processes, initializer=open_process_archive) as pool:
        for n in pool.imap_unordered(work, batches):
            done += n; print(f'{done}/{len(tiles)} tiles, {(time.time() - started) / 60:.1f} min', flush=True)
