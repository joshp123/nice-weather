"""Compare actual Step A files with live cards and independent station records.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
All station-derived results are private validation, not production inputs.
"""
import argparse
import calendar
import collections
import concurrent.futures
import csv
import datetime as dt
import functools
import gzip
import hashlib
import json
import math
import pathlib
import shutil
import statistics
import struct
import subprocess
import sys
import time
import urllib.parse
import zoneinfo

import numpy as np

from compare import ZONES, published_dates
from published import decode_months, nearest_published_land
from stations import distance_km as station_distance_km

DATA_ROOT = pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data')
PHASE1 = pathlib.Path('/tmp/nwf-agents/round4/fine')
TARGETS = [
    ('sf','San Francisco','United States'),('los_angeles','Los Angeles','United States'),
    ('santa_barbara','Santa Barbara','United States'),('san_diego','San Diego','United States'),
    ('lima','Lima','Peru'),('valparaiso','Valparaíso','Chile'),('malaga','Málaga','Spain'),
    ('tenerife_south','Tenerife South','Spain'),('funchal','Funchal','Portugal'),
    ('cape_town','Cape Town','South Africa'),('seville','Seville','Spain'),
    ('rotterdam','Rotterdam','Netherlands'),('bangkok','Bangkok','Thailand')]
HOURLY_ALIASES = {
    'sf':['USW00023272'], 'los_angeles':['USW00023174'],
    'santa_barbara':['USW00023190'], 'san_diego':['USW00023188'],
    'malaga':['SPU0006155A'], 'tenerife_south':['SPN000C429I'],
    'seville':['SPMU0098391'], 'rotterdam':['NLI0000EHRD']}
HOURLY_WMO = {'los_angeles':'72295','san_diego':'72290','santa_barbara':'72392',
              'malaga':'08482','tenerife_south':'60025','seville':'08391','rotterdam':'06344'}


def distance_km(latitude,longitude,other_latitude,other_longitude):
    return station_distance_km(dict(lat=latitude,lon=longitude),dict(lat=other_latitude,lon=other_longitude))


def sha256(path):
    digest=hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda:stream.read(8*1024*1024),b''):
            digest.update(block)
    return digest.hexdigest()


def download(url,path,cached=True,missing_ok=False):
    path.parent.mkdir(parents=True,exist_ok=True)
    if cached and path.exists() and path.stat().st_size:
        return path
    partial=path.with_name(path.name+'.part')
    result=subprocess.run(['curl','--retry','2','--connect-timeout','20','--max-time','180',
        '-fsS',url,'-o',str(partial)],capture_output=True,text=True)
    if result.returncode:
        if missing_ok and result.returncode==22:
            return None
        raise RuntimeError(f'{url}: {result.stderr.strip()}')
    partial.replace(path)
    return path


def compressed_isd_csv(url,target):
    parts=urllib.parse.urlparse(url).path.strip('/').split('/')
    year,station=parts[-2],parts[-1].split('.')[0]
    compressed_url=f'https://www.ncei.noaa.gov/pub/data/noaa/{year}/{station[:6]}-{station[6:]}-{year}.gz'
    compressed=target.with_name(f'{station[:6]}-{station[6:]}-{year}.gz')
    if download(compressed_url,compressed,missing_ok=True) is None:
        return None
    temporary=target.with_name(target.name+'.normalized.part')
    fields=['STATION','DATE','LATITUDE','LONGITUDE','ELEVATION','TMP','DEW','WND']
    with gzip.open(compressed,'rt',encoding='ascii') as source,temporary.open('w') as stream:
        writer=csv.writer(stream);writer.writerow(fields)
        for record in source:
            stamp=f'{record[15:19]}-{record[19:21]}-{record[21:23]}T{record[23:25]}:{record[25:27]}:00'
            writer.writerow([record[4:15],stamp,int(record[28:34])/1000,int(record[34:41])/1000,
                int(record[46:51]),record[87:92]+','+record[92],record[93:98]+','+record[98],
                ','.join([record[60:63],record[63],record[64],record[65:69],record[69]])])
    temporary.replace(target)
    target.with_suffix('.source.json').write_text(json.dumps(dict(url=compressed_url,raw_file=str(compressed),
        raw_sha256=sha256(compressed),format='NOAA ISD mandatory fixed-width fields, normalized to minimal CSV',
        documentation='https://www.ncei.noaa.gov/pub/data/noaa/isd-format-document.pdf',
        geometry='Original per-report coordinates from ISD mandatory section; CSV service may instead attach modern station-catalogue coordinates.'),indent=2)+'\n')
    return target


def download_hourly(url,target,missing_ok=False):
    if target.exists() and target.stat().st_size:
        return target
    if 'noaa-global-hourly-pds.s3.amazonaws.com' in url:
        normalized=compressed_isd_csv(url,target)
        if normalized is not None:
            return normalized
    partial=target.with_name(target.name+'.part')
    result=subprocess.run(['curl','--retry','2','--connect-timeout','20','--max-time','900',
        '--speed-time','60','--speed-limit','1024','--continue-at','-',
        '-fsS',url,'-o',str(partial)],capture_output=True,text=True)
    if result.returncode:
        if missing_ok and result.returncode==22:
            return None
        raise RuntimeError(f'{url}: {result.stderr.strip()}')
    partial.replace(target)
    return target


def instrument_daily_observations(path):
    values={}
    for line in path.read_text().splitlines():
        year,month,element=int(line[11:15]),int(line[15:17]),line[17:21]
        if not 2015<=year<=2025 or element not in ('TMAX','TMIN'):
            continue
        for day in range(1,calendar.monthrange(year,month)[1]+1):
            field=line[21+(day-1)*8:29+(day-1)*8]
            if int(field[:5])!=-9999 and field[6]==' ':
                date=dt.date(year,month,day).isoformat()
                values.setdefault(date,{})[element]=int(field[:5])/10
    return values


def read_stations(observations):
    return {station['slug']:station for station in json.loads((observations/'evaluation_stations.json').read_text())}


def resolve_city_queries(stations,output,base_url):
    directory=output/'city-search';directory.mkdir(parents=True,exist_ok=True)
    def resolve(target):
        slug,name,country=target
        url=base_url+'/api/search?'+urllib.parse.urlencode({'q':name})
        path=download(url,directory/f'{slug}.json',cached=False)
        results=json.loads(path.read_text())
        matching=[row for row in results if country.casefold() in row.get('region','').casefold()]
        if matching:
            exact=[row for row in matching if row['name'].casefold()==name.casefold()]
            selected=(exact or matching)[0]
            return dict(slug=slug,name=name,lat=selected['latitude'],lon=selected['longitude'],
                query_kind='app_city_search',search_url=url,search_result=selected)
        if slug=='tenerife_south':
            station=stations[slug]
            return dict(slug=slug,name='Tenerife south (airport reference)',lat=station['lat'],lon=station['lon'],
                query_kind='explicit_airport_region_reference',search_url=url,
                note='The descriptive Tenerife south query has no city-search match; use the identified southern airport, not an invented city centre.')
        raise ValueError(f'No {country} app search match for {name}; inspect {path}')
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
        queries=list(pool.map(resolve,TARGETS))
    (output/'city_queries.json').write_text(json.dumps(queries,indent=2)+'\n')
    return queries


def acquire_missing_hourly(stations,output,workers):
    import hourly_stations
    hourly_stations.download=download_hourly
    directory=output/'hourly';(directory/'raw').mkdir(parents=True,exist_ok=True)
    catalogue_url='https://www.ncei.noaa.gov/oa/global-historical-climatology-network/hourly/doc/ghcnh-station-list.csv'
    catalogue=download(catalogue_url,directory/'ghcnh-station-list.csv')
    isd_url='https://www.ncei.noaa.gov/pub/data/noaa/isd-history.csv'
    isd_path=download(isd_url,directory/'isd-history.csv')
    catalogue_rows=list(csv.DictReader(catalogue.open()))
    history=list(csv.DictReader(isd_path.open()))
    legacy={}
    selections={}
    for slug,aliases in HOURLY_ALIASES.items():
        station=stations[slug]
        exact=[row for row in catalogue_rows if row['GHCN_ID'] in aliases]
        for row in exact:
            separation=distance_km(station['lat'],station['lon'],float(row['LATITUDE']),float(row['LONGITUDE']))
            if separation>5:
                raise ValueError(f'Hourly alias is not colocated with daily station: {slug} {row}')
        candidates=[row for row in history if row['LAT'] and row['LON'] and row['END']>='20160101'
            and distance_km(station['lat'],station['lon'],float(row['LAT']),float(row['LON']))<=5
            and (row['WBAN']==station['station'][-5:] or
                 (slug in HOURLY_WMO and row['USAF'].startswith(HOURLY_WMO[slug])))]
        if candidates and slug not in ('sf','los_angeles','san_diego','santa_barbara'):
            selected=max(candidates,key=lambda row:(row['END'],row['BEGIN']))
            legacy[slug]=selected['USAF']+selected['WBAN']
        selections[slug]=dict(daily_station=station['station'],aliases=exact,
            legacy_candidates=candidates,legacy_selected=legacy.get(slug))
    hourly_stations.LEGACY_STATIONS.update(legacy)
    # US GHCNh contains dense hourly reports; avoid downloading equivalent larger legacy CSVs.
    for slug in ('sf','los_angeles','san_diego','santa_barbara'):
        hourly_stations.LEGACY_STATIONS.pop(slug,None)
    jobs=[(directory,slug,aliases,ZONES[slug],year) for slug,aliases in HOURLY_ALIASES.items()
          for year in range(2016,2026)]
    rows=[];provenance=[];started=time.monotonic()
    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
        for info,days in pool.map(hourly_stations.process_station_year,jobs):
            if info['status']=='ok':
                transport=directory/'raw'/f"ISD_{info['hourly_station']}_{info['year']}.source.json"
                if transport.exists():
                    info['transport']=json.loads(transport.read_text())
                    info['format']='isd_normalized_csv'
                    info['url']=info['transport']['url']
            provenance.append(info);rows.extend(days)
            print('Hourly',info['slug'],info['year'],info['status'],info.get('days21',0),flush=True)
    distinct={}
    for row in rows:
        if not 2016<=int(row['date'][:4])<=2025:
            continue
        key=row['slug'],row['date']
        if key not in distinct or row['temperature_hours']>distinct[key]['temperature_hours']:
            distinct[key]=row
    rows=sorted(distinct.values(),key=lambda row:(row['slug'],row['date']))
    if rows:
        with (directory/'daily_sampled_extrema.csv').open('w') as stream:
            writer=csv.DictWriter(stream,fieldnames=list(rows[0]));writer.writeheader();writer.writerows(rows)
    report=dict(written_by='AI, GPT-6.1 Sol, 2026.10.17',selections=selections,
        catalogue=dict(url=catalogue_url,sha256=sha256(catalogue)),isd_history=dict(url=isd_url,sha256=sha256(isd_path)),
        years=[2016,2025],files=provenance,seconds=time.monotonic()-started,
        method='Existing hourly_stations.py QA and nearest-whole-hour reduction; colocated aliases checked within 5km; separate instruments remain explicit.')
    (directory/'summary.json').write_text(json.dumps(report,indent=2)+'\n')
    return report


def acquire_sf_hourly_proxy(output,workers):
    import hourly_stations
    hourly_stations.download=download_hourly
    directory=output/'hourly-sf-proxy'
    if (directory/'summary.json').exists():
        return json.loads((directory/'summary.json').read_text())
    (directory/'raw').mkdir(parents=True,exist_ok=True)
    # The downtown USW record contains no evening measurements. Preserve a different urban instrument explicitly.
    hourly_stations.LEGACY_STATIONS.pop('sf',None)
    jobs=[(directory,'sf',['USL000PXOC1'],'America/Los_Angeles',year) for year in range(2016,2026)]
    files=[]
    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
        for info,days in pool.map(hourly_stations.process_station_year,jobs):
            files.append(info)
            print('SF Pier1 proxy',info['year'],info['status'],info.get('days21',0),flush=True)
    report=dict(written_by='AI, GPT-6.1 Sol, 2026.10.17',files=files,
        note='SF evening/hourly validation uses SAN FRANCISCO PIER1, a separate urban waterfront instrument about 4.3km northeast of the downtown daily instrument. Daily high validation remains USW00023272. This is a geographic proxy, not the downtown instrument.')
    (directory/'summary.json').write_text(json.dumps(report,indent=2)+'\n')
    return report


def acquire_quintero_historical_alias(output,workers):
    import hourly_stations
    hourly_stations.download=download_hourly
    hourly_stations.LEGACY_STATIONS.pop('valparaiso',None)
    directory=output/'hourly-quintero-alias';(directory/'raw').mkdir(parents=True,exist_ok=True)
    if (directory/'summary.json').exists():
        return json.loads((directory/'summary.json').read_text())
    jobs=[(directory,'valparaiso',['CIU00085543'],'America/Santiago',year) for year in range(2016,2023)]
    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
        files=[info for info,days in pool.map(hourly_stations.process_station_year,jobs)]
    report=dict(written_by='AI, GPT-6.1 Sol, 2026.10.17',files=files,
        note='Primary catalogue alternative Quintero IGY alias within2km; probe missing historical years rather than assume the modern ICAO alias is exhaustive. File absence remains explicit.')
    (directory/'summary.json').write_text(json.dumps(report,indent=2)+'\n')
    return report


def load_hourly_observations(stations,directories,output):
    """Retain QA-passing nearest-hour reports with explicit colocated source identities."""
    import pyarrow.parquet as parquet
    from hourly_stations import QUALITY_ACCEPTED,VARIABLES
    records=collections.defaultdict(dict);sources=collections.defaultdict(list)
    for directory in directories:
        summary_path=directory/'summary.json'
        if not summary_path.exists():
            continue
        for info in json.loads(summary_path.read_text())['files']:
            slug=info['slug']
            if slug not in stations:
                continue
            if info['status']!='ok':
                sources[slug].append(dict(**info,rejected_no_file=True))
                continue
            if slug=='sf' and directory.name!='hourly-sf-proxy' and (output/'hourly-sf-proxy/summary.json').exists():
                sources[slug].append(dict(**info,rejected_for_proxy_selection=True))
                continue
            station=stations[slug];location=info['location']
            if location is None:
                sources[slug].append(dict(**info,rejected_empty_file=True))
                continue
            separation=distance_km(station['lat'],station['lon'],location['LATITUDE'],location['LONGITUDE'])
            if separation>5:
                sources[slug].append(dict(**info,rejected_location_km=separation))
                continue
            legacy='noaa-global-hourly-pds' in info['url'] or info.get('format')=='isd_normalized_csv'
            name=(f"ISD_{info['hourly_station']}_{info['year']}.csv" if legacy else
                  f"GHCNh_{info['hourly_station']}_{info['year']}.parquet")
            path=directory/'raw'/name
            sources[slug].append(dict(**info,raw_file=str(path),sha256=sha256(path),daily_station_distance_km=separation))
            if legacy:
                with path.open() as stream:
                    inputs=[]
                    for row in csv.DictReader(stream):
                        fields={'DATE':row['DATE']}
                        for variable,key,position in [('temperature','TMP',0),('dew_point_temperature','DEW',0),('wind_speed','WND',3)]:
                            value,quality=row[key].split(',')[position:position+2]
                            fields[variable]=None if abs(int(value))==9999 else int(value)/10
                            fields[variable+'_Quality_Code']=quality
                        inputs.append(fields)
            else:
                columns=['DATE']+[field for variable in VARIABLES for field in (variable,variable+'_Quality_Code')]
                inputs=parquet.read_table(path,columns=columns).to_pylist()
            for row in inputs:
                stamp=dt.datetime.fromisoformat(row['DATE']).replace(tzinfo=dt.timezone.utc)
                if not 2016<=stamp.year<=2025:
                    continue
                rounded=(stamp+dt.timedelta(minutes=30)).replace(minute=0,second=0)
                hour=int(rounded.timestamp()//3600);distance=abs((stamp-rounded).total_seconds())
                for variable in VARIABLES:
                    value=row[variable];quality=row[variable+'_Quality_Code']
                    if value is None or not math.isfinite(float(value)) or quality not in QUALITY_ACCEPTED:
                        continue
                    previous=records[slug].setdefault(hour,{}).get(variable)
                    if previous is None or distance<previous[1]:
                        records[slug][hour][variable]=(float(value),distance,info['hourly_station'])
    cache=output/'hourly-cache';cache.mkdir(exist_ok=True)
    for slug,hours in records.items():
        times=sorted(hour for hour,values in hours.items() if 'temperature' in values)
        np.savez_compressed(cache/f'{slug}.npz',utc_hours=np.array(times,dtype=np.int64),
            temperature_c=np.array([hours[h]['temperature'][0] for h in times]),
            dew_point_c=np.array([hours[h].get('dew_point_temperature',(np.nan,))[0] for h in times]),
            wind_ms=np.array([hours[h].get('wind_speed',(np.nan,))[0] for h in times]))
    (output/'hourly-observation-provenance.json').write_text(json.dumps(dict(
        written_by='AI, GPT-6.1 Sol, 2026.10.17',sources=sources,
        method='Nearest report within 30min of UTC hour, separately per variable across colocated catalogue aliases; existing NOAA QA whitelist. Source instrument must be within 5km of the declared daily instrument; rejected locations retained. SF uses only the separately declared Pier1 proxy, never a blend with the downtown daily instrument.'),indent=2)+'\n')
    return records,sources


def station_daily_hourly(records,zone_name):
    zone=zoneinfo.ZoneInfo(zone_name);days=collections.defaultdict(dict)
    for hour,fields in records.items():
        if 'temperature' not in fields:
            continue
        stamp=dt.datetime.fromtimestamp(hour*3600,dt.timezone.utc).astimezone(zone)
        days[stamp.date().isoformat()][stamp.hour]=fields
    rows={}
    for date,hours in days.items():
        if not 2016<=int(date[:4])<=2025:
            continue
        daytime=[fields['temperature'][0] for hour,fields in hours.items() if 10<=hour<=20]
        at21=hours.get(21,{})
        temperature=at21.get('temperature',(None,))[0]
        dew=at21.get('dew_point_temperature',(None,))[0]
        wind=at21.get('wind_speed',(None,))[0]
        felt=None
        if all(value is not None for value in (temperature,dew,wind)):
            capped=min(dew,temperature)
            felt=temperature+.33*6.105*math.exp(17.27*capped/(237.7+capped))-.70*wind-4
        rows[date]=dict(sampled_high=max(fields['temperature'][0] for fields in hours.values()) if len(hours)>=18 else None,
            daytime_high=max(daytime) if len(daytime)>=9 else None,air21=temperature,felt21=felt,
            hours=hours,temperature_hours=len(hours),daytime_hours=len(daytime))
    return rows


@functools.lru_cache(maxsize=1)
def climate_pipeline():
    directory=pathlib.Path(__file__).resolve().parents[1]/'climate-v6'
    sys.path.insert(0,str(directory))
    import pipeline
    return pipeline


def raw_field(raw_root,fine,chunk,name):
    row,column=divmod(int(fine),1440);column=(column+720)%1440
    path=raw_root/f'chunk{chunk}'/f'r{row//12:02d}c{column//12:03d}.npz'
    with np.load(path) as arrays:
        result=arrays[name][:,row%12,column%12].astype(np.float32)
    return result,path


@functools.lru_cache(maxsize=12)
def point_weather(raw_root,fine,chunk,zone_name,temperature_source=None,shift=0):
    """Replay the existing pipeline's full local10–21h point-weather sampling."""
    pipeline=climate_pipeline();raw_root=pathlib.Path(raw_root);source=int(fine if temperature_source is None else temperature_source)
    t,t_path=raw_field(raw_root,source,chunk,'t2m');dew,dew_path=raw_field(raw_root,source,chunk,'d2m')
    u,path=raw_field(raw_root,fine,chunk,'u10');v,_=raw_field(raw_root,fine,chunk,'v10')
    rain,_=raw_field(raw_root,fine,chunk,'tp');radiation,_=raw_field(raw_root,fine,chunk,'fdir')
    t+=shift;dew+=shift;wind=np.hypot(u,v)*3.6
    epoch,dates=pipeline.chunk_dates(chunk);zone=zoneinfo.ZoneInfo(zone_name)
    utc=np.array([[(dt.datetime.combine(date,dt.time(hour),zone).astimezone(dt.timezone.utc)-epoch).total_seconds()/3600
                   for hour in range(10,22)] for date in dates])
    low=np.floor(utc).astype(int);fraction=utc-low
    def sample(field,accumulation=False):
        base=low+(1 if accumulation else 0)
        return field[base]*(1-fraction)+field[base+1]*fraction
    temperature=sample(t);dewpoint=sample(dew);winds=sample(wind);precipitation=sample(rain,True);direct=sample(radiation,True)
    row,column=divmod(int(fine),1440);latitude=90-row*.25;longitude=-180+column*.25
    cosine=np.zeros_like(direct)
    for midpoint in (.1127016654,.5,.8872983346):
        weight=8/18 if midpoint==.5 else 5/18
        utc_dates=np.datetime64(epoch.date())+np.floor((utc+midpoint)/24).astype('timedelta64[D]')
        day_numbers=(utc_dates-utc_dates.astype('datetime64[Y]')).astype(int)+1
        value,_,_=pipeline.solar(day_numbers,(utc%24+midpoint)%24,latitude,longitude)
        cosine+=weight*np.maximum(0,value)
    dni=np.divide(direct,cosine,out=np.zeros_like(direct),where=cosine>.001)
    day_numbers=np.array([date.timetuple().tm_yday for date in dates])
    _,equation,declination=pipeline.solar(day_numbers,12,latitude,longitude)
    latitude_rad=np.deg2rad(latitude)
    argument=(np.cos(np.deg2rad(90.833))/(np.cos(latitude_rad)*np.cos(declination))-np.tan(latitude_rad)*np.tan(declination))
    sunset=(720-4*longitude-equation+4*np.rad2deg(np.arccos(np.clip(argument,-1,1))))/60
    offsets=np.array([dt.datetime.combine(date,dt.time(12),zone).utcoffset().total_seconds()/3600 for date in dates])
    wrap=24*np.rint((offsets-longitude/15)/24)
    daylight=np.clip(np.ceil(sunset+offsets-wrap)-10,1,11)
    daylight=np.where(argument>=1,1,np.where(argument<=-1,11,daylight))
    return dict(dates=[date.isoformat() for date in dates],T=temperature,Td=dewpoint,W=winds,rain=precipitation,
        dni=dni,daylight=daylight,raw_files=sorted({str(path),str(t_path),str(dew_path)}),
        utc_hours=np.rint((utc+epoch.timestamp()/3600)).astype(np.int64))


def weather_scores(weather,temperatures,retain_native_humidity=False):
    pipeline=climate_pipeline()
    scores=pipeline.taste.day_scores(temperatures,weather['Td'],weather['W'],weather['dni'],
        weather['rain'][:,:11].sum(1),weather['daylight'])[0]
    if retain_native_humidity:
        native_dew=np.minimum(weather['Td'],weather['T']).mean(1)
        changed_dew=np.minimum(weather['Td'],temperatures).mean(1)
        scores*=pipeline.taste.HUMIDITY_FACTOR(native_dew)/pipeline.taste.HUMIDITY_FACTOR(changed_dew)
    return scores


class Snapshot:
    def __init__(self,root):
        self.root=root/'v6' if not (root/'points.bin').exists() and (root/'v6/points.bin').exists() else root
        self.facts=(self.root/'climate/facts.bin').read_bytes()
        self.scores=(self.root/'climate/scores.bin').read_bytes()
        points=(self.root/'points.bin').read_bytes()
        self.points={fine:(source,shift/100,cell) for fine,source,shift,cell in struct.iter_unpack('<IIhH',points[4:])}
        self.meta=json.loads((self.root/'climate/meta.json').read_text())
        self.provenance=dict(root=str(self.root.resolve()),meta=self.meta,
            sha256={name:sha256(self.root/name) for name in ('points.bin','climate/facts.bin','climate/scores.bin','climate/meta.json')})

    def select(self,latitude,longitude):
        fine=nearest_published_land(latitude,longitude,self.facts)
        if fine is None:
            return None
        source,shift,cell=self.points[fine];row,column=divmod(fine,1440)
        return dict(fine=fine,lat=90-row*.25,lon=-180+column*.25,source=source,shift_c=shift,cell=cell,
            distance_km=distance_km(latitude,longitude,90-row*.25,-180+column*.25),
            months=decode_months(fine,self.facts,self.scores))


def verify_current_live(snapshot,queries,stations,output,base_url):
    directory=output/'live-verification';directory.mkdir(exist_ok=True)
    meta_url=base_url+'/data/v6/climate/meta.json'
    before=json.loads(download(meta_url,directory/'meta-before.json',cached=False).read_text())
    coordinates={}
    for query in queries:
        coordinates[query['slug']+'_city']=(query['lat'],query['lon'])
        station=stations[query['slug']]
        coordinates[query['slug']+'_station']=(station['lat'],station['lon'])
    def verify(item):
        name,(latitude,longitude)=item
        url=base_url+'/api/place?'+urllib.parse.urlencode(dict(lat=latitude,lon=longitude))
        path=download(url,directory/(name+'.json'),cached=False)
        api=json.loads(path.read_text());selection=snapshot.select(latitude,longitude)
        expected=selection['months'] if selection else None
        if api.get('months')!=expected:
            raise ValueError(f'Current root differs from current API for {name}: {path}')
        return dict(query=name,url=url,latitude=latitude,longitude=longitude,fine=selection['fine'] if selection else None,
            response_sha256=sha256(path),matched=True)
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
        checks=list(pool.map(verify,coordinates.items()))
    after=json.loads(download(meta_url,directory/'meta-after.json',cached=False).read_text())
    if before!=after or before!=snapshot.meta:
        raise ValueError('Current root metadata differs from stable live metadata; preserve this capture and refresh the baseline')
    result=dict(captured_at_utc=dt.datetime.now(dt.timezone.utc).isoformat(),meta=before,queries=checks,
        selection='Exact Worker rounded-centre/clipped5x5/nonzero-facts policy, with first row/column ties; all 12 decoded months equal API for each city and instrument query.')
    (directory/'provenance.json').write_text(json.dumps(result,indent=2)+'\n')
    return result


def mean_available(values):
    values=[float(value) for value in values if value is not None and math.isfinite(float(value))]
    return statistics.mean(values) if values else None


def diagnostics_for_station(station,current_selection,step_selection,records,step_chunks,raw_root,adjustments):
    fine=int(step_selection['fine'])
    delta=adjustments.get(fine)
    if delta is None:
        raise ValueError(f'Step A point has no recorded uniform offset: {fine}')
    results={};raw_files=set()
    for chunk in step_chunks:
        weather=point_weather(str(raw_root),fine,int(chunk),ZONES[station['slug']])
        raw_files.update(weather['raw_files'])
        month_index=np.array([int(date[5:7])-1 for date in weather['dates']])
        corrected=weather['T']+np.asarray(delta)[month_index,None]
        native_scores=weather_scores(weather,weather['T']);step_scores=weather_scores(weather,corrected,retain_native_humidity=True)
        _,step_shade,_=climate_pipeline().taste.felt(corrected,weather['Td'],weather['W'],weather['dni'])
        current_weather=point_weather(str(raw_root),int(current_selection['fine']),int(chunk),ZONES[station['slug']],
            int(current_selection['source']),float(current_selection['shift_c']))
        raw_files.update(current_weather['raw_files'])
        current_scores=weather_scores(current_weather,current_weather['T'])
        _,current_shade,_=climate_pipeline().taste.felt(current_weather['T'],current_weather['Td'],current_weather['W'],current_weather['dni'])
        observed_temperature=np.full(weather['T'].shape,np.nan)
        valid=[]
        for day,hours in enumerate(weather['utc_hours']):
            samples=[records.get(int(hour),{}).get('temperature') for hour in hours]
            if all(sample is not None for sample in samples):
                observed_temperature[day]=[sample[0] for sample in samples];valid.append(day)
        observed_scores=weather_scores(weather,observed_temperature,retain_native_humidity=True)
        for day,date in enumerate(weather['dates']):
            record=dict(current_air21_c=float(current_weather['T'][day,11]),
                native_air21_c=float(weather['T'][day,11]),step_a_air21_c=float(corrected[day,11]),
                native_dew_mean_c=float(np.minimum(weather['Td'][day],weather['T'][day]).mean()),
                current_replayed_high_c=float(current_weather['T'][day,:11].max()),
                current_replayed_felt21_c=float(current_shade[day,11]),
                step_a_replayed_high_c=float(corrected[day,:11].max()),step_a_replayed_felt21_c=float(step_shade[day,11]))
            if day in valid:
                record.update(station_temperature_substituted_score=float(observed_scores[day]),
                    native_full_hour_score_same_dates=float(native_scores[day]),
                    step_a_full_hour_score_same_dates=float(step_scores[day]),
                    current_full_hour_score_same_dates=float(current_scores[day]))
            results[date]=record
    return results,sorted(raw_files)


def verify_replayed_facts(slug,selection,diagnostics):
    fields={'highC':'step_a_replayed_high_c','feelsAt21C':'step_a_replayed_felt21_c','dewPointC':'native_dew_mean_c'}
    comparisons=[]
    for month in range(1,13):
        days=[value for date,value in diagnostics.items() if int(date[5:7])==month]
        for encoded,replayed in fields.items():
            expected=mean_available(day[replayed] for day in days)
            actual=selection['months'][month-1][encoded]
            comparisons.append(dict(month=month,field=encoded,replayed_c=expected,encoded_c=actual,
                absolute_difference_c=abs(expected-actual)))
    return dict(slug=slug,fine=selection['fine'],latitude=selection['lat'],longitude=selection['lon'],
        native_temperature_source=selection['source']==selection['fine'] and selection['shift_c']==0,
        maximum_difference_c=max(item['absolute_difference_c'] for item in comparisons),
        mismatches=[item for item in comparisons if item['absolute_difference_c']>.201],comparisons=comparisons)


def compare_outputs(args,stations,queries):
    started=time.monotonic();current=Snapshot(args.current_root);step=Snapshot(args.step_a_root)
    if current.meta['chunks']!=step.meta['chunks']:
        raise ValueError('Current and Step A chunk periods differ; a changed-period comparison requires explicit separate baselines')
    live=verify_current_live(current,queries,stations,args.output,args.base_url.rstrip('/')) if not args.skip_live_verification else None
    directories=[args.base_hourly,args.output/'hourly',args.output/'hourly-sf-proxy',args.output/'hourly-quintero-alias']
    records,sources=load_hourly_observations(stations,directories,args.output)
    with np.load(args.adjustments) as arrays:
        adjustments={int(fine):offset for fine,offset in zip(arrays['fine'],arrays['uniform_offset_c'])}
    published=published_dates(step.meta['chunks'])
    monthly_summary={(row['station'],int(row['month'])):row for row in csv.DictReader((args.observations/'station_monthly.csv').open())
        if row['role']=='evaluation' and row['period']=='2016-2025' and row['element']=='TMAX'}
    rows=[];selections={};raw_inputs=set();verification=[]
    for query in queries:
        slug=query['slug'];station=stations[slug]
        city_current=current.select(query['lat'],query['lon']);city_step=step.select(query['lat'],query['lon'])
        site_current=current.select(station['lat'],station['lon']);site_step=step.select(station['lat'],station['lon'])
        selections[slug]=dict(query=query,station=station,current_city=city_current,step_a_city=city_step,
            current_instrument=site_current,step_a_instrument=site_step)
        observations=instrument_daily_observations(args.observations/'daily'/f"{station['station']}.dly")
        hourly=station_daily_hourly(records.get(slug,{}),ZONES[slug])
        diagnostics,raw_files=diagnostics_for_station(station,site_current,site_step,records.get(slug,{}),step.meta['chunks'],args.raw_root,adjustments)
        raw_inputs.update(raw_files)
        verification.append(verify_replayed_facts(slug,site_step,diagnostics))
        station_city_distance=distance_km(query['lat'],query['lon'],station['lat'],station['lon'])
        source_ids=sorted({info['hourly_station'] for info in sources.get(slug,[]) if not any(key.startswith('rejected_') for key in info)})
        for month in range(1,13):
            month_dates={date for date in published if int(date[5:7])==month}
            instrument_dates=[date for date,values in observations.items() if date in month_dates and 'TMAX' in values]
            modern_instrument=[values['TMAX'] for date,values in observations.items() if 2016<=int(date[:4])<=2025 and int(date[5:7])==month and 'TMAX' in values]
            summary=monthly_summary[(station['station'],month)]
            row=dict(slug=slug,city=query['name'],month=month,query_kind=query['query_kind'],query_lat=query['lat'],query_lon=query['lon'],
                station=station['station'],station_name=station['name'],station_lat=station['lat'],station_lon=station['lon'],
                city_to_instrument_km=station_city_distance,station_geolocation_uncertain=station.get('geolocation_uncertain',False),
                station_note=station.get('note',station.get('setting','')),hourly_station_ids=';'.join(source_ids),
                hourly_proxy_note='SF Pier1 waterfront instrument about 4.3km from downtown daily station' if slug=='sf' and 'USL000PXOC1' in source_ids else '',
                published_days=len(month_dates),station_instrument_high_c=mean_available(observations[date]['TMAX'] for date in instrument_dates),
                station_instrument_high_days=len(instrument_dates),station_instrument_high_coverage=len(instrument_dates)/len(month_dates),
                station_instrument_high_modern_c=mean_available(modern_instrument),station_instrument_high_strict=summary['accepted']=='True',
                station_instrument_high_complete_month_years=int(summary['complete_years']),
                station_default_score=None,station_default_score_status='Unavailable: no complete validated station radiation/rain/weather inputs')
            for prefix,selection in [('current_city',city_current),('step_a_city',city_step),('current_instrument',site_current),('step_a_instrument',site_step)]:
                values=selection['months'][month-1]
                row.update({prefix+'_fine':selection['fine'],prefix+'_lat':selection['lat'],prefix+'_lon':selection['lon'],
                    prefix+'_selection_distance_km':selection['distance_km'],prefix+'_high_c':values['highC'],
                    prefix+'_felt21_c':values['feelsAt21C'],prefix+'_default_score':values['score']})
            for name,variable in [('sampled_high','sampled_high'),('daytime_high','daytime_high'),('air21','air21'),('felt21','felt21')]:
                dates=[date for date,values in hourly.items() if date in month_dates and values[variable] is not None]
                row['station_hourly_'+name+'_c']=mean_available(hourly[date][variable] for date in dates)
                row['station_hourly_'+name+'_days']=len(dates)
                row['station_hourly_'+name+'_coverage']=len(dates)/len(month_dates)
            for variable in ('current_air21_c','native_air21_c','step_a_air21_c','native_dew_mean_c',
                             'current_replayed_high_c','current_replayed_felt21_c','step_a_replayed_high_c','step_a_replayed_felt21_c',
                             'station_temperature_substituted_score','native_full_hour_score_same_dates',
                             'step_a_full_hour_score_same_dates','current_full_hour_score_same_dates'):
                dates=[date for date,values in diagnostics.items() if date in month_dates and variable in values]
                row[variable]=mean_available(diagnostics[date][variable] for date in dates)
                if variable=='station_temperature_substituted_score':
                    row['station_temperature_substituted_score_days']=len(dates)
                    row['station_temperature_substituted_score_coverage']=len(dates)/len(month_dates)
            for truth,dates,predictions in [
                    ('instrument_high',instrument_dates,('current_replayed_high_c','step_a_replayed_high_c')),
                    ('hourly_daytime_high',[date for date,value in hourly.items() if date in month_dates and value['daytime_high'] is not None],('current_replayed_high_c','step_a_replayed_high_c')),
                    ('hourly_air21',[date for date,value in hourly.items() if date in month_dates and value['air21'] is not None],('current_air21_c','step_a_air21_c')),
                    ('hourly_felt21',[date for date,value in hourly.items() if date in month_dates and value['felt21'] is not None],('current_replayed_felt21_c','step_a_replayed_felt21_c'))]:
                for prediction in predictions:
                    row[prediction+'_on_observed_'+truth+'_days']=mean_available(diagnostics[date][prediction] for date in dates if date in diagnostics)
            rows.append(row)
        print('Compared Step A',slug,flush=True)
    verification_report=dict(tolerance_c=.167+.034,points=verification,
        mismatches=sum(len(item['mismatches'])+int(not item['native_temperature_source']) for item in verification),
        method='Independent own-point raw hourly replay; native capped dew retained; encoded1/3C then displayed0.1C allows0.201C rounding error.')
    (args.output/'raw_replay_verification.json').write_text(json.dumps(verification_report,indent=2)+'\n')
    if verification_report['mismatches']:
        raise ValueError('Step A encoded facts disagree with raw weather replay; inspect raw_replay_verification.json')
    with (args.output/'monthly_step_a.csv').open('w') as stream:
        writer=csv.DictWriter(stream,fieldnames=list(rows[0]));writer.writeheader();writer.writerows(rows)
    metadata=dict(written_by='AI, GPT-6.1 Sol, 2026.10.17',seconds=time.monotonic()-started,current=current.provenance,
        step_a=step.provenance,live_verification=live,raw_replay_verification=verification_report,adjustments=dict(path=str(args.adjustments),sha256=sha256(args.adjustments)),
        queries=queries,selections=selections,raw_weather_files=sorted(raw_inputs),
        station_observation_inputs={str(path):sha256(path) for path in [args.observations/'evaluation_stations.json',
            args.observations/'station_monthly.csv',args.output/'hourly-observation-provenance.json']+
            [args.observations/'daily'/f"{stations[slug]['station']}.dly" for slug,_,_ in TARGETS]},
        observation_period='Daily QA observations 2015–2025 and hourly QA observations 2016–2025 intersect actual published chunk dates; hourly missing 2015 dates are explicit in per-month coverage',
        high_definition='Product facts: daily high 10–20. Instrument TMAX:24h extrema; hourly daytime_high uses at least 9 observed10–20 hours, sampled_high at least 18 observed hours.',
        evening_definition='Published facts 21 is felt temperature. Station air21 and felt21 are separate, using QA observations; felt 21 requires observed T,dew,wind.',
        score_definition='Native capped-dew humidity nuisance is retained in A and station-T diagnostic full-hour scores. Actual default score reads compact scores.bin. Station-temperature-substituted score is a full-hour diagnostic: observed T at every 10–21h hour, native gridded dew/wind/rain/radiation/daylight at the Step A selected instrument point. It is not a full station score.',
        step_a_definition='Uniform monthly air offset at every hour; native dew input; vapour pressure caps dew at the corrected air temperature as in the existing felt formula.',
        model_period_dates_sha256=hashlib.sha256('\n'.join(sorted(published)).encode()).hexdigest())
    (args.output/'comparison_step_a_metadata.json').write_text(json.dumps(metadata,indent=2)+'\n')
    write_markdown(rows,metadata,args.output)
    if args.private_report_output and args.private_report_output.resolve()!=args.output.resolve():
        args.private_report_output.mkdir(parents=True,exist_ok=True)
        for path in args.output.iterdir():
            if path.is_file() and path.suffix in ('.md','.csv','.json'):
                shutil.copy2(path,args.private_report_output/path.name)
        if (args.output/'live-verification').exists():
            shutil.copytree(args.output/'live-verification',args.private_report_output/'live-verification',dirs_exist_ok=True)
    print(json.dumps(dict(rows=len(rows),cities=len(queries),seconds=metadata['seconds'],output=str(args.output))),flush=True)


def write_markdown(rows,metadata,output):
    def fmt(value):return '—' if value is None else f'{float(value):.1f}'
    def score_fmt(value):return '—' if value is None else str(int(value))
    def mae(selected,prediction,truth):
        values=[abs(row[prediction]-row[truth]) for row in selected if row.get(truth) is not None and row.get(prediction) is not None]
        return statistics.mean(values) if values else None
    summaries=[]
    lines=['Written by AI on 2026.10.17 by model GPT-6.1 Sol.','','# Step A: actual product comparison','',
        'The complete twelve-month table for all thirteen requested places is [monthly_step_a.csv](monthly_step_a.csv). City values use current app search coordinates; the separate instrument-query columns apply the same exact card-selection policy at the physical weather station. The station is often an airport or coastal proxy, not the city centre.','',
        'All temperatures are °C. These are monthly-mean errors over available observations; coverage is retained in the CSV and station_summary.json. Air21 replay columns use raw hourly weather at the selected instrument-query point; they are not stored air21 card facts. High-error columns compare the product’s 10–20h daily high with instrument 24h TMAX on available published-period observation dates; their windows differ. Evening-error columns compare felt 21 with observed felt21. Missing observations remain blank. Actual compact default scores are read from scores.bin. The summary JSON also reports date-paired 10–20h high errors against hourly station samples with at least nine observed daytime hours; those avoid the 24h TMAX window difference.','',
        '| Place | Instrument km from city | High MAE current / A | Felt21 MAE current / A | Air21 replay MAE current / A | Months with felt21 | Minimum monthly coverage high / felt21 | Monthly score range current → A |',
        '|---|---:|---:|---:|---:|---:|---:|---:|']
    for slug,_,_ in TARGETS:
        selected=[row for row in rows if row['slug']==slug]
        current_scores=[row['current_city_default_score'] for row in selected if row['current_city_default_score'] is not None]
        step_scores=[row['step_a_city_default_score'] for row in selected if row['step_a_city_default_score'] is not None]
        summaries.append(dict(slug=slug,city=selected[0]['city'],instrument_distance_km=selected[0]['city_to_instrument_km'],
            high_mae_current_c=mae(selected,'current_instrument_high_c','station_instrument_high_c'),
            high_mae_step_a_c=mae(selected,'step_a_instrument_high_c','station_instrument_high_c'),
            felt21_mae_current_c=mae(selected,'current_instrument_felt21_c','station_hourly_felt21_c'),
            felt21_mae_step_a_c=mae(selected,'step_a_instrument_felt21_c','station_hourly_felt21_c'),
            air21_mae_current_c=mae(selected,'current_air21_c','station_hourly_air21_c'),
            air21_mae_step_a_c=mae(selected,'step_a_air21_c','station_hourly_air21_c'),
            paired_high_mae_current_c=mae(selected,'current_replayed_high_c_on_observed_instrument_high_days','station_instrument_high_c'),
            paired_high_mae_step_a_c=mae(selected,'step_a_replayed_high_c_on_observed_instrument_high_days','station_instrument_high_c'),
            paired_daytime_high_mae_current_c=mae(selected,'current_replayed_high_c_on_observed_hourly_daytime_high_days','station_hourly_daytime_high_c'),
            paired_daytime_high_mae_step_a_c=mae(selected,'step_a_replayed_high_c_on_observed_hourly_daytime_high_days','station_hourly_daytime_high_c'),
            paired_air21_mae_current_c=mae(selected,'current_air21_c_on_observed_hourly_air21_days','station_hourly_air21_c'),
            paired_air21_mae_step_a_c=mae(selected,'step_a_air21_c_on_observed_hourly_air21_days','station_hourly_air21_c'),
            paired_felt21_mae_current_c=mae(selected,'current_replayed_felt21_c_on_observed_hourly_felt21_days','station_hourly_felt21_c'),
            paired_felt21_mae_step_a_c=mae(selected,'step_a_replayed_felt21_c_on_observed_hourly_felt21_days','station_hourly_felt21_c'),
            high_coverage_min=min(row['station_instrument_high_coverage'] for row in selected),
            air21_coverage_min=min(row['station_hourly_air21_coverage'] for row in selected),
            felt21_coverage_min=min(row['station_hourly_felt21_coverage'] for row in selected),
            score_diagnostic_days=sum(row['station_temperature_substituted_score_days'] for row in selected),
            current_paired_temperature_diagnostic_score_mae=mae(selected,'current_full_hour_score_same_dates','station_temperature_substituted_score'),
            step_a_paired_temperature_diagnostic_score_mae=mae(selected,'step_a_full_hour_score_same_dates','station_temperature_substituted_score'),
            current_city_scores=current_scores,step_a_city_scores=step_scores))
        lines.append('| '+selected[0]['city']+' | '+fmt(selected[0]['city_to_instrument_km'])+' | '+
            fmt(mae(selected,'current_instrument_high_c','station_instrument_high_c'))+' / '+fmt(mae(selected,'step_a_instrument_high_c','station_instrument_high_c'))+' | '+
            fmt(mae(selected,'current_instrument_felt21_c','station_hourly_felt21_c'))+' / '+fmt(mae(selected,'step_a_instrument_felt21_c','station_hourly_felt21_c'))+' | '+
            fmt(mae(selected,'current_air21_c','station_hourly_air21_c'))+' / '+fmt(mae(selected,'step_a_air21_c','station_hourly_air21_c'))+' | '+
            str(sum(row['station_hourly_felt21_c'] is not None for row in selected))+' | '+
            f"{100*min(row['station_instrument_high_coverage'] for row in selected):.0f}% / {100*min(row['station_hourly_felt21_coverage'] for row in selected):.0f}%"+' | '+
            f'{min(current_scores)}–{max(current_scores)} → {min(step_scores)}–{max(step_scores)}'+' |')
    (output/'station_summary.json').write_text(json.dumps(summaries,indent=2)+'\n')
    lines+=['','Observed-temperature score diagnostics are separate CSV columns. They substitute observed hourly air temperature into native gridded non-temperature weather and require all twelve 10–21h observations on a day. They measure temperature sensitivity on those same days; they are **not station-derived comfort scores**. Full station scores are unavailable because complete validated radiation/rain inputs are absent.','',
        'The published periods contain discontinuous 360-day chunk samples. Daily station records cover 2015–2025; hourly records cover 2016–2025, so earlier 2015 published hourly dates and any missing reports reduce coverage. Each month retains observation day counts and coverage. Sparse/proxy sites and the selected grid-point coordinates remain explicit in the CSV and metadata.','']
    sf=metadata['selections']['sf']
    sf_mapping='San Francisco: the city query is '+f"{sf['query']['lat']:.5f},{sf['query']['lon']:.5f}, but the current card selects {sf['current_city']['lat']:.2f},{sf['current_city']['lon']:.2f} ({sf['current_city']['distance_km']:.1f}km away); Step A selects {sf['step_a_city']['lat']:.2f},{sf['step_a_city']['lon']:.2f} ({sf['step_a_city']['distance_km']:.1f}km away). "+\
        'The rounded-centre grid search resolves ties by row/column order. A corrected climate field does not itself remove this geographic selection error.'
    lines+=[sf_mapping,'',
        'Provenance, current API checks, input hashes, exact coordinates and definitions: [comparison_step_a_metadata.json](comparison_step_a_metadata.json).','',
        'Observation sources: [NOAA GHCNh](https://www.ncei.noaa.gov/products/global-historical-climatology-network-hourly) and [NOAA ISD](https://www.ncei.noaa.gov/products/land-based-station/integrated-surface-database). These station-derived comparison outputs remain private validation, outside the publication artifact.']
    monthly=['Written by AI on 2026.10.17 by model GPT-6.1 Sol.','','# Monthly Step A comparison','',
        'Each place leads with actual city-query temperatures and compact scores, alongside the named observed instrument. A separate table gives the matched instrument query. Observed high is instrument 24h TMAX; product high is 10–20h. The 21:00 columns are felt °C; observed air21 is separate. Diagnostic score substitutes observed air temperature in native other weather and requires all 12 daytime/evening hours. It is not a full station score. Missing values remain blank. Coverage and exact source identities are in the CSV.','']
    for slug,_,_ in TARGETS:
        selected=[row for row in rows if row['slug']==slug]
        first=selected[0]
        monthly+=['## '+first['city'],'',first['station_name']+' ('+first['station']+'), '+fmt(first['city_to_instrument_km'])+'km from city query. '+first['station_note']+' '+first['hourly_proxy_note'],'',
            f"City query {first['query_lat']:.5f},{first['query_lon']:.5f}; current card {first['current_city_lat']:.2f},{first['current_city_lon']:.2f} ({first['current_city_selection_distance_km']:.1f}km away); A card {first['step_a_city_lat']:.2f},{first['step_a_city_lon']:.2f} ({first['step_a_city_selection_distance_km']:.1f}km away).",'',
            '| Month | City high current / A / instrument | City felt21 current / A / instrument | Observed air21 | Instrument score current / A | City score current / A | Observed days high / felt21 (of total) | Station-T diagnostic |',
            '|---|---:|---:|---:|---:|---:|---:|---:|']
        for row in selected:
            values=[calendar.month_abbr[row['month']],
                ' / '.join(fmt(row[key]) for key in ('current_city_high_c','step_a_city_high_c','station_instrument_high_c')),
                ' / '.join(fmt(row[key]) for key in ('current_city_felt21_c','step_a_city_felt21_c','station_hourly_felt21_c')),
                fmt(row['station_hourly_air21_c']),
                ' / '.join(score_fmt(row[key]) for key in ('current_instrument_default_score','step_a_instrument_default_score')),
                ' / '.join(score_fmt(row[key]) for key in ('current_city_default_score','step_a_city_default_score')),
                f"{row['station_instrument_high_days']} / {row['station_hourly_felt21_days']} (of {row['published_days']})",
                fmt(row['station_temperature_substituted_score'])]
            monthly.append('| '+' | '.join(values)+' |')
        monthly+=['','Matched instrument-query temperatures (the point selected by querying the physical instrument):','',
            '| Month | High current / A | Felt21 current / A |', '|---|---:|---:|']
        for row in selected:
            monthly.append('| '+' | '.join([calendar.month_abbr[row['month']],
                ' / '.join(fmt(row[key]) for key in ('current_instrument_high_c','step_a_instrument_high_c')),
                ' / '.join(fmt(row[key]) for key in ('current_instrument_felt21_c','step_a_instrument_felt21_c'))])+' |')
        monthly.append('')
    (output/'MONTHLY_STEP_A.md').write_text('\n'.join(monthly)+'\n')
    july=['Written by AI on 2026.10.17 by model GPT-6.1 Sol.','','# July: actual city cards and station benchmarks','',
        'High and felt21 show current city card → Step A city card → observed instrument. Instruments can be airports or proxies away from the city; distances below are explicit. High is 10–20h in the product and 24h TMAX at the instrument. Observed felt21 requires measured air/dew/wind. A full station default score is unavailable; the last column is a separately labelled observed-air-temperature substitution diagnostic at the instrument query using native other weather.','',
        '| Place | Instrument km from city | High °C current → A → instrument | Felt21 °C current → A → instrument | City default score current → A | Observed days high / felt21 (of total) | Station-T diagnostic |',
        '|---|---:|---:|---:|---:|---:|---:|']
    for row in [row for row in rows if row['month']==7]:
        july.append('| '+' | '.join([row['city'],fmt(row['city_to_instrument_km']),
            ' → '.join(fmt(row[key]) for key in ('current_city_high_c','step_a_city_high_c','station_instrument_high_c')),
            ' → '.join(fmt(row[key]) for key in ('current_city_felt21_c','step_a_city_felt21_c','station_hourly_felt21_c')),
            ' → '.join(score_fmt(row[key]) for key in ('current_city_default_score','step_a_city_default_score')),
            f"{row['station_instrument_high_days']} / {row['station_hourly_felt21_days']} (of {row['published_days']})",
            fmt(row['station_temperature_substituted_score'])])+' |')
    july+=['','San Francisco hourly air is the Pier1 waterfront proxy; observed felt21 is unavailable. Full monthly definitions, observation counts, coverage and selected point coordinates are in monthly_step_a.csv. The temperature-substituted diagnostic is not an observed full-weather comfort score.']
    (output/'JULY_STEP_A.md').write_text('\n'.join(july)+'\n')
    lead=lines[:4]+['The first table compares the actual current and Step A city-card queries. All twelve months, city and instrument point coordinates, coverage and the separate instrument-query statistics are in [MONTHLY_STEP_A.md](MONTHLY_STEP_A.md) and [monthly_step_a.csv](monthly_step_a.csv).','',sf_mapping,'','## July city cards','']+july[4:]
    lead+=['','## Station-query error summary','']+lines[4:]
    (output/'STEP_A_COMPARISON.md').write_text('\n'.join(lead)+'\n')



def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--current-root',type=pathlib.Path)
    parser.add_argument('--step-a-root',type=pathlib.Path)
    parser.add_argument('--observations',type=pathlib.Path,default=PHASE1/'stations')
    parser.add_argument('--base-hourly',type=pathlib.Path,default=PHASE1/'hourly',help='Frozen initial hourly source cache')
    parser.add_argument('--output',type=pathlib.Path,default=PHASE1/'step-a-comparison')
    parser.add_argument('--base-url',default='https://nice.jjpcodes.com')
    parser.add_argument('--prepare-only',action='store_true')
    parser.add_argument('--workers',type=int,default=4)
    parser.add_argument('--queries',type=pathlib.Path)
    parser.add_argument('--raw-root',type=pathlib.Path,default=DATA_ROOT/'era5')
    parser.add_argument('--adjustments',type=pathlib.Path,default=DATA_ROOT/'fine-build/step-a/adjustment.npz')
    parser.add_argument('--skip-live-verification',action='store_true')
    parser.add_argument('--private-report-output',type=pathlib.Path,default=DATA_ROOT/'fine-validation/step-a')
    args=parser.parse_args();args.output.mkdir(parents=True,exist_ok=True)
    all_stations=read_stations(args.observations)
    stations={slug:all_stations[slug] for slug,_,_ in TARGETS}
    if args.prepare_only:
        resolve_city_queries(stations,args.output,args.base_url.rstrip('/'))
        acquire_missing_hourly(stations,args.output,args.workers)
        acquire_sf_hourly_proxy(args.output,args.workers)
        acquire_quintero_historical_alias(args.output,args.workers)
        return
    if args.current_root is None or args.step_a_root is None:
        parser.error('--current-root and --step-a-root are required for comparison')
    queries=json.loads((args.queries or args.output/'city_queries.json').read_text())
    compare_outputs(args,stations,queries)


if __name__=='__main__':
    main()
