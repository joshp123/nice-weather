"""Anonymous NSRDB GOES acquisition, sharing each physical HDF5 chunk.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
The plan action downloads only metadata, counts exact 500-site groups and
reports the radiation byte budget. The extract action requires an explicit
maximum number of physical groups, so a probe cannot become an unbounded run.
"""
import argparse
import concurrent.futures
import hashlib
import http.client
import json
from pathlib import Path
import time
import urllib.request

import h5py
import numpy as np
from scipy.spatial import cKDTree

from named_satellite import atomic_json, coordinates, solar_sunset
from named_timezone_lookup import TargetTimezones
from satellite import ChunkLocationFile
from instant_hours import integrate_civil_hours
from named_range_file import AnonymousRangeFile, read_range_before_deadline

ROOT = Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/sun/named/goes')
KEYS = {'tmy': 'GOES/tmy/v4.0.0/nsrdb_tmy-2024.h5',
        'tdy': 'GOES/tmy/v4.0.0/nsrdb_tdy-2024.h5',
        'disc2022': 'GOES/full_disc/v4.0.0/nsrdb_full_disc_irradiance_2022.h5',
        'disc2024': 'GOES/full_disc/v4.0.0/nsrdb_full_disc_irradiance_2024.h5'}


def selected_years(hdf,url,root,source):
    path=root/f'{source}-selected-years.npy'
    if path.exists():
        return np.load(path,mmap_mode='r')
    dataset=hdf['tmy_year_short']
    size=dataset.size*dataset.dtype.itemsize
    offset=dataset.id.get_offset()
    starts=list(range(0,size,2*1024*1024))
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        chunks=list(pool.map(lambda start:range_read(url,offset+start,min(2*1024*1024,size-start)),starts))
    result=np.frombuffer(b''.join(chunks),dtype=dataset.dtype).reshape(dataset.shape)
    np.save(path,result)
    return result


def range_read(url, offset, size):
    for attempt in range(4):
        try:
            request = urllib.request.Request(url, headers={'Range': f'bytes={offset}-{offset+size-1}'})
            with urllib.request.urlopen(request, timeout=30) as response:
                data = read_range_before_deadline(response,size,60)
            if len(data) != size:
                raise ValueError(f'Wrong byte range length {len(data)} != {size}')
            return data
        except (OSError, TimeoutError, http.client.IncompleteRead):
            if attempt == 3:
                raise
            time.sleep(2**attempt)


def cartesian(points):
    radians = np.deg2rad(points)
    return np.column_stack((np.cos(radians[:,0])*np.cos(radians[:,1]),
                            np.cos(radians[:,0])*np.sin(radians[:,1]), np.sin(radians[:,0])))


def metadata_coordinates(hdf, url, root, source):
    path = root/f'{source}-coordinates.npy'
    if path.exists():
        return np.load(path)
    earlier_source='disc2022' if source=='disc2024' else 'tmy' if source=='tdy' else None
    if earlier_source and (root/f'{earlier_source}-coordinates.npy').exists():
        earlier=np.load(root/f'{earlier_source}-coordinates.npy')
        if len(earlier)==len(hdf['meta']):
            samples=np.linspace(0,len(earlier)-1,25).astype(int)
            check=hdf['meta'][samples]
            if np.array_equal(np.column_stack((check['latitude'],check['longitude'])),earlier[samples]):
                return earlier
    dataset = hdf['meta']
    offset, size = dataset.id.get_offset(), dataset.size*dataset.dtype.itemsize
    raw = root/f'{source}-meta.bin'
    if not raw.exists():
        temporary = raw.with_suffix('.partial')
        completed=temporary.stat().st_size if temporary.exists() else 0
        ranges=list(range(completed,size,2*1024*1024))
        def fetch(start):
            return range_read(url,offset+start,min(2*1024*1024,size-start))
        with temporary.open('ab') as stream,concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            for data in pool.map(fetch,ranges):
                stream.write(data)
                stream.flush()
        temporary.replace(raw)
    metadata = np.memmap(raw, mode='r', dtype=dataset.dtype, shape=dataset.shape)
    result = np.column_stack((metadata['latitude'],metadata['longitude']))
    np.save(path,result)
    return result


def locate_points(hdf, url, root, source, points, metadata):
    if metadata:
        available = metadata_coordinates(hdf,url,root,source)
        distance, index = cKDTree(cartesian(available)).query(cartesian(np.array(points)))
        return [(int(i),available[i].tolist()) if d*6371 < 8 else None for i,d in zip(index,distance)]
    locations = []
    cache_path=root/f'{source}-selected.json'
    cached=json.loads(cache_path.read_text()) if cache_path.exists() else {}
    earlier=json.loads((root/'tmy-selected.json').read_text()) if source=='tdy' and (root/'tmy-selected.json').exists() else {}
    for latitude,longitude in points:
        key=f'{round(latitude*1e5)},{round(longitude*1e5)}'
        if key in cached:
            locations.append(cached[key])
            continue
        if key in earlier and earlier[key] is not None:
            index,selected=earlier[key]
            record=hdf['meta'][index]
            if selected==[float(record['latitude']),float(record['longitude'])]:
                locations.append(earlier[key]);cached[key]=earlier[key]
                atomic_json(cache_path,cached)
                continue
        low,high=0,len(hdf['meta'])
        while low<high:
            middle=(low+high)//2
            if float(hdf['meta'][middle]['longitude']) < longitude-.025:
                low=middle+1
            else:
                high=middle
        first=max(0,low-8000)
        stop=min(len(hdf['meta']),low+16000)
        dataset=hdf['meta']
        raw=range_read(url,dataset.id.get_offset()+first*dataset.dtype.itemsize,(stop-first)*dataset.dtype.itemsize)
        nearby=np.frombuffer(raw,dtype=dataset.dtype)
        distance=(nearby['latitude']-latitude)**2+((nearby['longitude']-longitude)*np.cos(np.deg2rad(latitude)))**2
        local=int(np.argmin(distance))
        location=(first+local,[float(nearby[local]['latitude']),float(nearby[local]['longitude'])]) if distance[local]<.08**2 else None
        locations.append(location)
        cached[key]=location
        atomic_json(cache_path,cached)
    return locations


def summarize(values, timestamps, latitude, longitude, zone_name, source,selected=None):
    rows=[[] for _ in range(12)]
    for day in integrate_civil_hours(values,timestamps,zone_name,selected):
        date=day['date'].replace(year=day['actual_year'])
        daylight=solar_sunset(date,latitude,longitude,day['utc_offset_hours'])
        if not day['complete'][:daylight].all():
            continue
        hourly=day['hourly_dni']
        rows[day['date'].month-1].append((np.count_nonzero(hourly[:daylight]>=300)/daylight,daylight,hourly))
    return [dict(sunnyShare=float(np.mean([day[0] for day in days])),
                 daylightHours=float(np.mean([day[1] for day in days])),
                 hourlyDni=np.mean([day[2] for day in days],axis=0).tolist(),sourceDays=len(days)) if days else None
            for days in rows]


def summarize_cached(args,points):
    source=args.source
    typical=source in ('tmy','tdy')
    zones=TargetTimezones(args.root.parent/'target-timezones.json')
    locations=json.loads((args.root/f'{source}-selected.json').read_text())
    years=np.load(args.root/f'{source}-selected-years.npy',mmap_mode='r') if typical else None
    records=[]
    for lat,lon in points:
        key=f'{round(lat*1e5)},{round(lon*1e5)}'
        raw=args.root/source/f'{key}.npz'
        data=np.load(raw)
        zone=zones.timezone_at(lat=lat,lng=lon)
        index,selected=locations.get(key,[None,data['selected'].tolist()])
        if typical and index is None:
            raise ValueError('Typical-year replay requires the cached native source index')
        selection=years[:,index].astype(int).tolist() if typical else None
        records.append(dict(latitude=lat,longitude=lon,
                            months=summarize(data['dni'],data['time'].tolist(),lat,lon,zone,source,selection) if zone else [None]*12,
                            modelSupported=zone is not None,satelliteEligible=True,
                            source='NSRDB GOES4 '+(source.upper()+'1998–2024' if typical else source[-4:]),
                            years=[1998,2024] if typical else [int(source[-4:])],selectedYears=selection,
                            selectedCoordinates=selected,sourceIndex=index,timezone=zone,
                            url='https://nrel-pds-nsrdb.s3.amazonaws.com/'+KEYS[source],
                            rawSha256=hashlib.sha256(raw.read_bytes()).hexdigest()))
    atomic_json(args.output,dict(records=records,integration='Linear instantaneous DNI at60minute midpoints per civil hour'))


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action',choices=['plan','extract','summarize','index'])
    parser.add_argument('--source',choices=KEYS,required=True)
    parser.add_argument('--coordinates',type=Path,required=True)
    parser.add_argument('--root',type=Path,default=ROOT)
    parser.add_argument('--output',type=Path,required=True)
    parser.add_argument('--metadata',action='store_true')
    parser.add_argument('--max-groups',type=int,default=20)
    parser.add_argument('--workers',type=int,default=12)
    parser.add_argument('--all-latitudes',action='store_true')
    parser.add_argument('--exclude-profiles',type=Path)
    parser.add_argument('--exclude-locations',type=Path)
    args=parser.parse_args()
    args.root.mkdir(parents=True,exist_ok=True)
    points=[p for p in coordinates(args.coordinates) if -60<=p[0]<=60 and -180<=p[1]<-22
            and (p[0]>=-21 if args.source in ('tmy','tdy') else args.all_latitudes or p[0]<-20)]
    if args.exclude_profiles:
        preferred=set()
        for line in args.exclude_profiles.read_text().splitlines():
            profile=json.loads(line)
            if profile.get('inputs',{}).get('meteo_data',{}).get('radiation_db')=='PVGIS-SARAH3':
                preferred.add(tuple(profile['requestedCoordinates']))
        points=[point for point in points if tuple(point) not in preferred]
    if args.exclude_locations:
        preferred={(row['latitude'],row['longitude']) for row in json.loads(args.exclude_locations.read_text()) if row['eligible']}
        points=[point for point in points if tuple(point) not in preferred]
    source=args.source
    if args.action=='summarize':
        summarize_cached(args,points)
        return
    url='https://nrel-pds-nsrdb.s3.amazonaws.com/'+KEYS[source]
    started=time.monotonic()
    with AnonymousRangeFile(url) as remote:
        recording=ChunkLocationFile(remote)
        with h5py.File(recording) as hdf:
            if args.action=='index':
                chunks=[]
                def visit(chunk):
                    chunks.append([list(chunk.chunk_offset),chunk.byte_offset,chunk.size])
                    if len(chunks)%1000==0:
                        print(json.dumps(dict(source=source,indexedChunks=len(chunks),seconds=time.monotonic()-started,
                                              metadataBytes=remote.transferred,metadataRequests=remote.requests)),flush=True)
                hdf['dni'].id.chunk_iter(visit)
                atomic_json(args.output,dict(sourceUrl=url,dniShape=hdf['dni'].shape,dniChunks=hdf['dni'].chunks,records=chunks))
                return
            locations=locate_points(hdf,url,args.root,source,points,args.metadata or args.action=='plan')
            groups={}
            for row,location in enumerate(locations):
                if location is not None:
                    groups.setdefault(location[0]//500,[]).append(row)
            dataset=hdf['dni']; time_chunk,site_chunk=dataset.chunks
            if site_chunk!=500 or dataset.compression is not None:
                raise ValueError('Unexpected NSRDB chunk format')
            chunks_per_group=int(np.ceil(dataset.shape[0]/time_chunk))
            budget=dict(source=source,sourceUrl=url,requested=len(points),eligible=sum(map(len,groups.values())),
                        groups=len(groups),chunksPerGroup=chunks_per_group,
                        radiationBytes=len(groups)*chunks_per_group*time_chunk*site_chunk*dataset.dtype.itemsize,
                        dniShape=dataset.shape,dniChunks=dataset.chunks,seconds=time.monotonic()-started)
            atomic_json(args.output.with_suffix('.plan.json'),budget)
            atomic_json(args.output.with_suffix('.locations.json'),
                        [dict(latitude=point[0],longitude=point[1],eligible=location is not None,
                              sourceIndex=location[0] if location else None,
                              selectedCoordinates=location[1] if location else None)
                         for point,location in zip(points,locations)])
            print(json.dumps(budget),flush=True)
            if args.action=='plan':
                return
            if len(groups)>args.max_groups:
                raise ValueError('Group count exceeds explicit acquisition budget')
            timestamps=[value.decode() for value in hdf['time_index'][:]]
            source_years=selected_years(hdf,url,args.root,source) if source in ('tmy','tdy') else None
            results={row:np.empty(dataset.shape[0],dtype=dataset.dtype) for rows in groups.values() for row in rows}
            raw_directory=args.root/source
            raw_directory.mkdir(exist_ok=True)
            downloads=[];group_remaining={};group_rows={}
            index_path=args.root/f'{source}-dni-chunks.json'
            chunk_index={}
            if index_path.exists():
                index_payload=json.loads(index_path.read_text())
                if index_payload['sourceUrl']!=url or index_payload['dniShape']!=list(dataset.shape) or index_payload['dniChunks']!=list(dataset.chunks):
                    raise ValueError('Physical chunk index describes a different source dataset')
                chunk_index={tuple(row[0]):(row[1],row[2]) for row in index_payload['records']}
            indexed=0
            for group,rows in groups.items():
                remaining=[]
                for row in rows:
                    lat,lon=points[row]
                    raw_path=raw_directory/f'{round(lat*1e5)},{round(lon*1e5)}.npz'
                    if raw_path.exists():
                        cached=np.load(raw_path)
                        results[row][:]=cached['dni']
                    else:
                        remaining.append(row)
                if not remaining:
                    continue
                group_rows[group]=remaining
                ranges=[]
                for start in range(0,dataset.shape[0],time_chunk):
                    size=time_chunk*site_chunk*dataset.dtype.itemsize
                    logical=(start,group*500)
                    if logical in chunk_index:
                        offset,indexed_size=chunk_index[logical]
                        if indexed_size!=size:
                            raise ValueError('Physical DNI index has an unexpected chunk size')
                        ranges.append((start,offset,indexed_size))
                    else:
                        recording.locating_size=size; recording.locations=[]
                        dataset.id.read_direct_chunk(logical)
                        recording.locating_size=None
                        if len(recording.locations)!=1:
                            raise ValueError('Cannot locate physical DNI chunk')
                        ranges.append((start,*recording.locations[0]))
                group_remaining[group]=len(ranges)
                downloads.extend((group,*item) for item in ranges)
                indexed+=1
                if indexed%100==0:
                    print(json.dumps(dict(source=source,indexedGroups=indexed,totalGroups=len(groups),seconds=time.monotonic()-started)),flush=True)
            dtype=dataset.dtype
            def fetch(item):
                group,start,offset,size=item
                data=range_read(url,offset,size)
                values=np.frombuffer(data,dtype=dtype).reshape(time_chunk,site_chunk)
                return group,start,{row:values[:,locations[row][0]%500].copy() for row in group_rows[group]}
            finished_groups=0
            radiation_started=time.monotonic()
            with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
                futures=[pool.submit(fetch,item) for item in downloads]
                for future in concurrent.futures.as_completed(futures):
                    group,start,values=future.result()
                    for row,column in values.items():
                        count=min(len(column),len(results[row])-start)
                        results[row][start:start+count]=column[:count]
                    group_remaining[group]-=1
                    if group_remaining[group]==0:
                        finished_groups+=1
                        for row in group_rows[group]:
                            lat,lon=points[row]
                            np.savez_compressed(raw_directory/f'{round(lat*1e5)},{round(lon*1e5)}.npz',
                                                dni=results[row],time=np.array(timestamps),requested=[lat,lon],selected=locations[row][1],sourceIndex=locations[row][0])
                        elapsed=time.monotonic()-started
                        progress=dict(source=source,groupsComplete=finished_groups,totalGroups=len(group_remaining),
                                      radiationBytesComplete=finished_groups*chunks_per_group*time_chunk*site_chunk*dtype.itemsize,
                                      seconds=elapsed,radiationSeconds=time.monotonic()-radiation_started,
                                      radiationBytesPerSecond=finished_groups*chunks_per_group*time_chunk*site_chunk*dtype.itemsize/(time.monotonic()-radiation_started),
                                      etaSeconds=elapsed/max(1,finished_groups)*(len(group_remaining)-finished_groups))
                        atomic_json(args.output.with_suffix('.progress.json'),progress)
                        if finished_groups%10==0 or finished_groups==len(group_remaining):
                            print(json.dumps(progress),flush=True)
            timezone=TargetTimezones(args.root.parent/'target-timezones.json')
            records=[]
            for row,values in results.items():
                lat,lon=points[row];zone=timezone.timezone_at(lat=lat,lng=lon)
                raw_path=raw_directory/f'{round(lat*1e5)},{round(lon*1e5)}.npz'
                selected=source_years[:,locations[row][0]].astype(int).tolist() if source_years is not None else None
                records.append(dict(latitude=lat,longitude=lon,months=summarize(values,timestamps,lat,lon,zone,source,selected) if zone else [None]*12,
                                    modelSupported=zone is not None,satelliteEligible=True,
                                    source='NSRDB GOES4 '+(source.upper()+'1998–2024' if source in ('tmy','tdy') else source[-4:]),
                                    years=[1998,2024] if source in ('tmy','tdy') else [int(source[-4:])],
                                    selectedYears=selected,
                                    selectedCoordinates=locations[row][1],timezone=zone,url=url,
                                    sourceIndex=locations[row][0],rawSha256=hashlib.sha256(raw_path.read_bytes()).hexdigest()))
            atomic_json(args.output,dict(records=records,plan=budget,seconds=time.monotonic()-started))


if __name__=='__main__':
    main()
