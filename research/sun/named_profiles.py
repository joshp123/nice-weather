"""Cache PVGIS SARAH3 long-period monthly UTC direct-radiation profiles.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
DRcalc month=0 provides all 288 month/hour means in one small response.
Only independently satellite-derived radiation is eligible; ERA5 is excluded.
"""
import argparse
import concurrent.futures
import gzip
import hashlib
import json
from pathlib import Path
import time
import urllib.error
import urllib.parse
import urllib.request

from named_satellite import RequestPacer, atomic_json, coordinates, ROOT


def fetch(point, root, pacer):
    lat,lon=point
    key=f'{round(lat*1e5)},{round(lon*1e5)}'
    directory=root/'profiles'/hashlib.sha256(key.encode()).hexdigest()[:2]
    path=directory/f'{key}.json.gz'
    if path.exists():
        return json.loads(gzip.decompress(path.read_bytes())),0,True
    parameters=dict(lat=lat,lon=lon,month=0,usehorizon=0,glob_2axis=1,
                    raddatabase='PVGIS-SARAH3',outputformat='json')
    url='https://re.jrc.ec.europa.eu/api/v5_3/DRcalc?'+urllib.parse.urlencode(parameters)
    for attempt in range(5):
        pacer.wait()
        try:
            with urllib.request.urlopen(url,timeout=120) as response:
                raw=response.read()
            payload=json.loads(raw)
            break
        except urllib.error.HTTPError as error:
            if error.code==400:
                payload=dict(error=error.read().decode(),requestedCoordinates=[lat,lon],url=url)
                raw=json.dumps(payload).encode()
                break
            if error.code not in (429,500,502,503,504,529) or attempt==4:
                raise
            time.sleep(2**attempt)
        except (OSError,TimeoutError):
            if attempt==4:
                raise
            time.sleep(2**attempt)
    payload['requestedCoordinates']=[lat,lon]
    payload['sourceUrl']=url
    directory.mkdir(parents=True,exist_ok=True)
    path.write_bytes(gzip.compress(json.dumps(payload,separators=(',',':')).encode(),mtime=0))
    return payload,len(raw),False


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--coordinates',type=Path,required=True)
    parser.add_argument('--root',type=Path,default=ROOT)
    parser.add_argument('--output',type=Path,required=True)
    parser.add_argument('--workers',type=int,default=16)
    parser.add_argument('--limit',type=int)
    parser.add_argument('--requests-per-second',type=float,default=20)
    args=parser.parse_args()
    if not 0<args.requests_per_second<=25:
        raise ValueError('Must leave headroom below documented30requests/s')
    jobs=[point for point in coordinates(args.coordinates) if -60<=point[0]<=65 and -65<=point[1]<=65]
    if args.limit:
        jobs=jobs[:args.limit]
    pacer=RequestPacer(args.requests_per_second)
    started=time.monotonic();completed=transferred=cached=eligible=0
    args.output.parent.mkdir(parents=True,exist_ok=True)
    with args.output.open('w') as stream,concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        futures=[pool.submit(fetch,point,args.root,pacer) for point in jobs]
        for future in concurrent.futures.as_completed(futures):
            payload,size,was_cached=future.result()
            completed+=1;transferred+=size;cached+=was_cached
            independent=payload.get('inputs',{}).get('meteo_data',{}).get('radiation_db')=='PVGIS-SARAH3'
            eligible+=independent
            stream.write(json.dumps(payload,separators=(',',':'))+'\n')
            if completed%100==0 or completed==len(jobs):
                stream.flush()
                elapsed=time.monotonic()-started
                progress=dict(complete=completed,total=len(jobs),eligible=eligible,cached=cached,
                              transferredBytes=transferred,seconds=elapsed,
                              newSources=completed-cached,
                              newSourcesPerSecond=(completed-cached)/elapsed,
                              etaSeconds=elapsed/(completed-cached)*(len(jobs)-completed) if completed>cached else None)
                atomic_json(args.output.with_suffix('.progress.json'),progress)
                print(json.dumps(progress),flush=True)


if __name__=='__main__':
    main()
