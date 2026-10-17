# Written by AI on 2026.10.17 by model claude opus 5.5.
# Downloads one ERA5 "chunk year" (8736 hours) of the six fields Nice Weather needs, for every 3° land tile
# (12 x 12 cells of 0.25°) from the free Earthmover archive, and keeps them on the SSD in plain units.
# Usage: fetch_era5.py <chunk index> <processes>. Resumable: finished tiles are skipped.
import argparse, hashlib, io, os, shlex, subprocess, time, pathlib, multiprocessing as mp
import numpy as np, icechunk, pcodec, zarr
OUT = pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/era5')
REMOTE = None
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
def open_process_archive(output, remote):
    global PROCESS_ARCHIVE, OUT, REMOTE
    OUT = pathlib.Path(output); REMOTE = remote
    PROCESS_ARCHIVE = archive()  # once per process: opening it downloads the archive's index

def work(args):
    chunk, tiles = args
    group = PROCESS_ARCHIVE; start = chunk * HOURS; folder = OUT / f'chunk{chunk}'
    for r, c in tiles:
        path = folder / f'r{r:02d}c{c:03d}.npz'
        if not REMOTE and path.exists(): continue
        rows, cols = slice(r * 12, min(r * 12 + 12, 721)), slice(c * 12, c * 12 + 12)
        for attempt in range(6):  # the connection drops now and then; a tile is worth retrying
            try:
                arrays = {name: convert(group[name][start:start + HOURS, rows, cols]).astype(np.float16) for name, convert in FIELDS.items()}
                break
            except Exception as error:
                if attempt == 5: raise
                print(f'retrying r{r}c{c} after {type(error).__name__}', flush=True); time.sleep(10 * (attempt + 1))
        tmp = path.with_suffix('.partial.npz')
        if REMOTE:
            buffer = io.BytesIO(); np.savez_compressed(buffer, **arrays)
            payload = buffer.getvalue(); digest = hashlib.sha256(payload).hexdigest()
            command = (f'cat > {shlex.quote(str(tmp))} && '
                       f'echo {shlex.quote(digest + "  " + str(tmp))} | sha256sum -c - && '
                       f'mv {shlex.quote(str(tmp))} {shlex.quote(str(path))}')
            subprocess.run(['/usr/bin/ssh', '-o', 'BatchMode=yes', REMOTE, command], input=payload,
                           stdout=subprocess.DEVNULL, check=True)
        else:
            np.savez_compressed(tmp, **arrays); os.replace(tmp, path)
    return len(tiles)

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description='Download ERA5 tiles locally or directly to a NAS over SSH.')
    parser.add_argument('chunk', type=int); parser.add_argument('processes', type=int)
    parser.add_argument('--out', type=pathlib.Path, default=OUT)
    parser.add_argument('--remote', help='SSH destination; --out is then on that host. Raw tiles never touch local disk.')
    parser.add_argument('--static-out', type=pathlib.Path, help='Local directory for the four static arrays when using --remote.')
    args = parser.parse_args(); chunk, processes = args.chunk, args.processes
    OUT = args.out; REMOTE = args.remote
    group = archive(); tiles = land_tiles(group)
    folder = OUT / f'chunk{chunk}'
    if REMOTE:
        listing = subprocess.check_output(['/usr/bin/ssh', '-o', 'BatchMode=yes', REMOTE,
            f'mkdir -p {shlex.quote(str(folder))} && find {shlex.quote(str(folder))} -name "r*.npz" -type f'], text=True)
        complete = {pathlib.Path(name).name for name in listing.splitlines() if '.partial' not in name}
        tiles = [(r, c) for r, c in tiles if f'r{r:02d}c{c:03d}.npz' not in complete]
    else:
        folder.mkdir(parents=True, exist_ok=True)
    first = np.datetime64('1940-01-01T00') + np.timedelta64(chunk * HOURS, 'h')
    print(f'chunk {chunk}: {first} to {first + np.timedelta64(HOURS, "h")}, {len(tiles)} land tiles', flush=True)
    static_out = args.static_out if REMOTE else OUT
    if static_out:
        static_out.mkdir(parents=True, exist_ok=True)
        np.save(static_out / 'static_lsm.npy', group['lsm'][:]); np.save(static_out / 'static_z.npy', group['z_sfc'][:])
        np.save(static_out / 'latitude.npy', group['latitude'][:]); np.save(static_out / 'longitude.npy', group['longitude'][:])
    batches = [(chunk, tiles[i::processes * 8]) for i in range(processes * 8)]
    started = time.time(); done = 0
    with mp.Pool(processes, initializer=open_process_archive, initargs=(str(OUT), REMOTE)) as pool:
        for n in pool.imap_unordered(work, batches):
            done += n; print(f'{done}/{len(tiles)} tiles, {(time.time() - started) / 60:.1f} min', flush=True)
