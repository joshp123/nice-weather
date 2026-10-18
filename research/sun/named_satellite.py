"""Acquire independent monthly solar targets at named-place coordinates.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
Run through research/sun/run.sh with numpy. Raw responses stay on the SSD.
The explicit SARAH3 DRcalc metadata establishes the source. CSV TMY metadata
retains selected years and the satellite observation time offset; JSON prefixes
are also checked in the initial benchmark and independent validation sample.
"""
import argparse
import concurrent.futures
import csv
import datetime as dt
import gzip
import hashlib
import functools
import json
from pathlib import Path
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from zoneinfo import ZoneInfo

import numpy as np

from named_tmy_calendar import nominal_tmy_timestamps
from named_timezone_lookup import TargetTimezones

ROOT = Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/sun/named')
UTC = dt.timezone.utc
METHOD = ('Civil clock hours 10..21; SARAH snapshots shifted by the documented '
          'irradiance_time_offset, interpolated linearly and integrated at minute '
          'midpoints. Sunny share is the mean daily fraction of daylight clock '
          'hours after 10:00 with hour-mean DNI >=300 W/m2. Integer sunset hour '
          'and 1..11-hour denominator match Nice Weather. TMY months are selected '
          'months, not full-period monthly means.')


class NativeTimezones(TargetTimezones):
    """Use the shared place model's exact 1 km clock at each named point."""
    def __init__(self,path=None):
        super().__init__(ROOT/'target-timezones.json')


def atomic_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + '.partial')
    temporary.write_text(json.dumps(value, separators=(',', ':'), allow_nan=False) + '\n')
    temporary.replace(path)


class RequestPacer:
    def __init__(self, per_second):
        self.interval = 1 / per_second
        self.next = 0
        self.lock = threading.Lock()

    def set_rate(self,per_second):
        with self.lock:
            self.interval=1/per_second

    def wait(self):
        with self.lock:
            delay = max(0, self.next - time.monotonic())
            self.next = max(self.next, time.monotonic()) + self.interval
        if delay:
            time.sleep(delay)


def solar_sunset(date, latitude, longitude, offset):
    gamma = 2 * np.pi / 365 * (date.timetuple().tm_yday - 1)
    equation = 229.18 * (.000075 + .001868*np.cos(gamma) - .032077*np.sin(gamma)
                         - .014615*np.cos(2*gamma) - .040849*np.sin(2*gamma))
    declination = (.006918 - .399912*np.cos(gamma) + .070257*np.sin(gamma)
                   - .006758*np.cos(2*gamma) + .000907*np.sin(2*gamma)
                   - .002697*np.cos(3*gamma) + .00148*np.sin(3*gamma))
    latitude_radians = np.deg2rad(latitude)
    argument = (np.cos(np.deg2rad(90.833)) / (np.cos(latitude_radians)*np.cos(declination))
                - np.tan(latitude_radians)*np.tan(declination))
    sunset_utc = (720 - 4*longitude - equation + 4*np.rad2deg(np.arccos(np.clip(argument, -1, 1)))) / 60
    sunset_local = sunset_utc + offset - 24*np.rint((offset-longitude/15)/24)
    return int(np.clip(np.ceil(sunset_local)-10, 1, 11))


@functools.lru_cache(maxsize=1024)
def timezone_offset_weights(zone_name, month):
    zone=ZoneInfo(zone_name)
    counts={}
    for year in range(2005,2024):
        date=dt.date(year,month,1)
        while date.month==month:
            offset=dt.datetime.combine(date,dt.time(12),zone).utcoffset().total_seconds()/3600
            counts[offset]=counts.get(offset,0)+1
            date+=dt.timedelta(days=1)
    return tuple(counts.items())


def profile_in_civil_hours(profile, source_shift, zone_name):
    monthly=[]
    for month in range(1,13):
        rows=[row for row in profile['outputs']['daily_profile'] if row['month']==month]
        hours=np.array([int(row['time'][:2])+int(row['time'][3:])/60 for row in rows])+source_shift
        dni=np.array([row['Gb(n)'] for row in rows])
        hours=np.concatenate((hours[-1:]-24,hours,hours[:1]+24))
        dni=np.concatenate((dni[-1:],dni,dni[:1]))
        sampled=[];weights=[]
        for offset,count in timezone_offset_weights(zone_name,month):
            sampled.append(np.interp((np.arange(10,22)[:,None]-offset+np.arange(60)/60+1/120)%24,hours,dni).mean(axis=1))
            weights.append(count)
        monthly.append(np.average(sampled,axis=0,weights=weights))
    return np.array(monthly)


def summarize_tmy(payload, latitude, longitude, zone_name, profile=None):
    if payload['inputs']['meteo_data']['radiation_db'] != 'PVGIS-SARAH3':
        raise ValueError('PVGIS returned non-independent radiation; never use ERA5 as satellite')
    source_rows = payload['outputs']['tmy_hourly']
    if len(source_rows) != 8760:
        raise ValueError(f'Expected 8760 TMY records, got {len(source_rows)}')
    selected_years = {row['month']: row['year'] for row in payload['outputs']['months_selected']}
    source_offset = payload['inputs']['location']['irradiance_time_offset'] * 3600
    # A nominal non-leap calendar joins the selected months into one continuous
    # series. Civil offsets still come from each source month's actual year.
    timestamps = nominal_tmy_timestamps(source_rows) + source_offset
    dni = np.array([row['Gb(n)'] for row in source_rows], dtype=float)
    if not np.isfinite(dni).all() or dni.min() < -.01 or dni.max() > 1500:
        raise ValueError('Invalid satellite DNI')
    # Wrap only the artificial TMY year boundary; no observed gap is filled.
    year_seconds = 365*86400
    timestamps = np.concatenate((timestamps[-24:]-year_seconds, timestamps, timestamps[:24]+year_seconds))
    dni = np.concatenate((dni[-24:], dni, dni[:24]))
    zone = ZoneInfo(zone_name)
    daily = [[] for _ in range(12)]
    nominal = dt.date(2001, 1, 1)
    while nominal.year == 2001:
        actual = nominal.replace(year=selected_years[nominal.month])
        offset = dt.datetime.combine(actual, dt.time(12), zone).utcoffset().total_seconds()/3600
        midnight = dt.datetime.combine(nominal, dt.time(), UTC).timestamp()
        minute_midpoints = midnight + (np.arange(10, 22)[:, None]-offset)*3600 + np.arange(60)*60 + 30
        hourly = np.interp(minute_midpoints, timestamps, dni).mean(axis=1)
        daylight = solar_sunset(actual, latitude, longitude, offset)
        share = float(np.count_nonzero(hourly[:daylight] >= 300)/daylight)
        daily[nominal.month-1].append((share, daylight, hourly))
        nominal += dt.timedelta(days=1)
    target=profile_in_civil_hours(profile,source_offset/3600,zone_name) if profile else None
    months=[]
    for month,days in enumerate(daily):
        hourly=np.array([day[2] for day in days]);denominator=np.array([day[1] for day in days])
        scale=np.ones(12)
        if target is not None:
            mean=hourly.mean(axis=0)
            scale=np.divide(target[month],mean,out=np.ones(12),where=mean>0)
            hourly*=scale
        mask=np.arange(12)[None,:]<denominator[:,None]
        sunny=((hourly>=300)&mask).sum(axis=1)/denominator
        months.append(dict(sunnyShare=float(sunny.mean()),daylightHours=float(denominator.mean()),
                           hourlyDni=hourly.mean(axis=0).tolist(),sourceDays=len(days),
                           selectedYear=selected_years[month+1],normalizationScale=scale.tolist()))
    return months


def fetch_sarah(job, root, pacer, timezone_finder, normalize=False, verified_csv=False):
    latitude, longitude = job
    key = f'{round(latitude*1e5)},{round(longitude*1e5)}'
    digest = hashlib.sha256(key.encode()).hexdigest()
    directory = root/'pvgis'/digest[:2]
    raw_path=directory/f'{key}.csv.gz'
    result_path=directory/f'{key}.{"normalized" if normalize else "target"}.json'
    metadata_path = directory/f'{key}.metadata.json'
    zone = timezone_finder.timezone_at(lat=latitude, lng=longitude)
    if result_path.exists():
        cached = json.loads(result_path.read_text())
        if cached.get('timezone') == zone:
            return cached, 0, True
    directory.mkdir(parents=True, exist_ok=True)
    parameters = dict(lat=latitude, lon=longitude, startyear=2005, endyear=2023,
                      outputformat='csv', usehorizon=0)
    url = 'https://re.jrc.ec.europa.eu/api/v5_3/tmy?' + urllib.parse.urlencode(parameters)
    metadata_url = url.replace('outputformat=csv','outputformat=json')
    transferred = 0
    profile_path=root/'profiles'/digest[:2]/f'{key}.json.gz'
    profile=json.loads(gzip.decompress(profile_path.read_bytes())) if normalize else None
    if profile and profile['inputs']['meteo_data']['radiation_db']!='PVGIS-SARAH3':
        raise ValueError('Non-independent long-period profile')
    if metadata_path.exists():
        metadata = json.loads(metadata_path.read_text())
    elif verified_csv:
        if not profile or profile['requestedCoordinates']!=[latitude,longitude]:
            raise ValueError('CSV source selection requires independent exact-coordinate DRcalc verification')
        metadata=None
    else:
        pacer.wait()
        with urllib.request.urlopen(metadata_url, timeout=120) as response:
            prefix = response.read(8192)
        # The first JSON portion contains complete inputs and selected years;
        # the huge hourly array is deliberately not downloaded twice.
        metadata = json.loads(prefix.decode().split(',"tmy_hourly"')[0] + '}}')
        metadata['verification'] = dict(url=metadata_url, prefixSha256=hashlib.sha256(prefix).hexdigest(), prefixBytes=len(prefix))
        transferred += len(prefix)
        atomic_json(metadata_path, metadata)
    if metadata and metadata['inputs']['meteo_data']['radiation_db'] != 'PVGIS-SARAH3':
        result = dict(latitude=latitude, longitude=longitude, months=[None]*12, source='unavailable',
                      reason='Independent SARAH3 unavailable', url=metadata_url)
        atomic_json(result_path, result)
        return result, transferred, False
    if raw_path.exists():
        raw = gzip.decompress(raw_path.read_bytes())
    else:
        for attempt in range(5):
            pacer.wait()
            try:
                with urllib.request.urlopen(url, timeout=120) as response:
                    raw = response.read()
                break
            except urllib.error.HTTPError as error:
                if error.code == 400:
                    detail = error.read().decode()
                    result = dict(latitude=latitude, longitude=longitude, months=[None]*12,
                                  source='unavailable', reason=detail, url=url)
                    atomic_json(result_path, result)
                    return result, 0, False
                if error.code not in (429, 500, 502, 503, 504, 529) or attempt == 4:
                    raise
                time.sleep(2**attempt)
            except (OSError, TimeoutError):
                if attempt == 4:
                    raise
                time.sleep(2**attempt)
        transferred += len(raw)
        temporary = raw_path.with_suffix('.partial')
        temporary.write_bytes(gzip.compress(raw, compresslevel=6, mtime=0))
        temporary.replace(raw_path)
    lines = raw.decode().splitlines()
    header = next(index for index,line in enumerate(lines) if line.startswith('time(UTC),'))
    selected = next(index for index,line in enumerate(lines) if line == 'month,year')
    csv_years = [dict(month=int(line.split(',')[0]),year=int(line.split(',')[1])) for line in lines[selected+1:selected+13]]
    if metadata is None:
        source_offset=float(next(line.split(':',1)[1] for line in lines if line.startswith('Irradiance Time Offset (h):')))
        reported_latitude=float(next(line.split(':',1)[1] for line in lines if line.startswith('Latitude (decimal degrees):')))
        reported_longitude=float(next(line.split(':',1)[1] for line in lines if line.startswith('Longitude (decimal degrees):')))
        if abs(reported_latitude-latitude)>.001 or abs(reported_longitude-longitude)>.001:
            raise ValueError('CSV coordinates disagree with requested point')
        metadata=dict(inputs=dict(location=dict(latitude=latitude,longitude=longitude,irradiance_time_offset=source_offset),
                                  meteo_data=dict(radiation_db='PVGIS-SARAH3',year_min=2005,year_max=2023)),
                      outputs=dict(months_selected=csv_years),
                      verification=dict(method='TMY default satellite source established by exact-coordinate explicit SARAH3 DRcalc response and primary manual section3',
                                        profileUrl=profile['sourceUrl'],profileSha256=hashlib.sha256(profile_path.read_bytes()).hexdigest(),
                                        sourceRule='https://joint-research-centre.ec.europa.eu/photovoltaic-geographical-information-system-pvgis/using-pvgis-5/pvgis-5-user-manual_en'))
        atomic_json(metadata_path,metadata)
    if csv_years != metadata['outputs']['months_selected']:
        raise ValueError('JSON metadata and CSV selected years disagree')
    hourly = list(csv.DictReader(lines[header:header+8761]))
    payload = dict(inputs=metadata['inputs'],outputs=dict(months_selected=csv_years,
                   tmy_hourly=[{'time(UTC)':row['time(UTC)'],'Gb(n)':float(row['Gb(n)'])} for row in hourly]))
    radiation_source = payload['inputs']['meteo_data']['radiation_db']
    if radiation_source != 'PVGIS-SARAH3':
        result = dict(latitude=latitude, longitude=longitude, months=[None]*12,
                      source='unavailable', reason=f'Independent SARAH3 unavailable; returned {radiation_source}', url=url)
    elif zone is None:
        result = dict(latitude=latitude,longitude=longitude,months=[None]*12,timezone=None,
                      source='PVGIS-SARAH3 model unsupported',satelliteEligible=True,
                      reason='Existing shared place model has no supported 1 km clock at this point',url=url,
                      rawSha256=hashlib.sha256(raw).hexdigest())
    else:
        result = dict(latitude=latitude, longitude=longitude,
                      months=summarize_tmy(payload, latitude, longitude, zone, profile),
                      source='PVGIS-SARAH3 TMY normalized to2005–2023hourlyDNI' if normalize else 'PVGIS-SARAH3 TMY', version='PVGIS5.3 SARAH3', years=[2005, 2023],
                      selectedYears=[row['year'] for row in payload['outputs']['months_selected']],
                      selectedCoordinates=[payload['inputs']['location']['latitude'], payload['inputs']['location']['longitude']],
                      coordinateMeaning='API requested coordinates; native satellite pixel coordinates not supplied',
                      timezone=zone, url=url, rawSha256=hashlib.sha256(raw).hexdigest(), method=METHOD)
    atomic_json(result_path, result)
    return result, transferred, False


def coordinates(path):
    rows = json.loads(path.read_text())
    return [(row['latitude'], row['longitude']) if isinstance(row, dict) else tuple(row) for row in rows]


def watching_results(args,allowed,pacer,timezone_finder):
    """Follow complete profile records while bounded workers fetch their TMY."""
    seen=set();pending=set();profile_complete=False;at_end=False
    with args.profiles.open() as profiles,concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        while not(profile_complete and at_end and not pending):
            while len(pending)<args.workers*3:
                position=profiles.tell();line=profiles.readline()
                if not line or not line.endswith('\n'):
                    profiles.seek(position);at_end=True
                    break
                at_end=False
                profile=json.loads(line)
                point=tuple(profile['requestedCoordinates'])
                if point in seen or point not in allowed or profile.get('inputs',{}).get('meteo_data',{}).get('radiation_db')!='PVGIS-SARAH3':
                    continue
                seen.add(point)
                pending.add(pool.submit(fetch_sarah,point,args.root,pacer,timezone_finder,True,True))
            progress_path=args.profiles.with_suffix('.progress.json')
            if not profile_complete and progress_path.exists():
                progress=json.loads(progress_path.read_text())
                if progress['complete']==progress['total']:
                    profile_complete=True
                    pacer.set_rate(args.finished_requests_per_second)
            if pending:
                done,pending=concurrent.futures.wait(pending,timeout=.5,return_when=concurrent.futures.FIRST_COMPLETED)
                for future in done:
                    yield future.result()
            else:
                time.sleep(.5)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--coordinates', type=Path, required=True)
    parser.add_argument('--root', type=Path, default=ROOT)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--limit', type=int)
    parser.add_argument('--offset', type=int, default=0)
    parser.add_argument('--workers', type=int, default=12)
    parser.add_argument('--requests-per-second', type=float, default=15)
    parser.add_argument('--normalize',action='store_true')
    parser.add_argument('--profiles',type=Path)
    parser.add_argument('--verified-csv',action='store_true')
    parser.add_argument('--watch-profiles',action='store_true')
    parser.add_argument('--finished-requests-per-second',type=float,default=25)
    args = parser.parse_args()
    if not 0 < args.requests_per_second <= 25:
        raise ValueError('Leave headroom below the documented 30 requests/s per-IP ceiling')
    jobs = [point for point in coordinates(args.coordinates) if -60 <= point[0] <= 65 and -65 <= point[1] <= 65]
    if args.watch_profiles and not(args.profiles and args.normalize and args.verified_csv):
        raise ValueError('Watching requires profiles, normalization and verified CSV')
    if not 0<args.finished_requests_per_second<=25:
        raise ValueError('Finished profile rate must leave API headroom')
    if args.profiles and not args.watch_profiles:
        eligible=set()
        for line in args.profiles.read_text().splitlines():
            profile=json.loads(line)
            if profile.get('inputs',{}).get('meteo_data',{}).get('radiation_db')=='PVGIS-SARAH3':
                eligible.add(tuple(profile['requestedCoordinates']))
        jobs=[point for point in jobs if tuple(point) in eligible]
    jobs = jobs[args.offset: args.offset+args.limit if args.limit else None]
    timezone_finder = NativeTimezones(Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/v6-fine-b1-20261018/temperature/timezones/lookup.npz'))
    pacer = RequestPacer(args.requests_per_second)
    started = time.monotonic()
    records, transferred, cached, fetched = [], 0, 0, 0
    args.output.parent.mkdir(parents=True, exist_ok=True)
    stream_path=args.output.with_suffix('.ndjson')
    with stream_path.open('w') as stream,concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        if args.watch_profiles:
            responses=watching_results(args,set(jobs),pacer,timezone_finder)
        else:
            futures = [pool.submit(fetch_sarah, job, args.root, pacer, timezone_finder,args.normalize,args.verified_csv) for job in jobs]
            responses=(future.result() for future in concurrent.futures.as_completed(futures))
        for record, byte_count, was_cached in responses:
            records.append(record)
            stream.write(json.dumps(record,separators=(',',':'))+'\n')
            transferred += byte_count
            cached += was_cached
            fetched += byte_count>0
            if len(records) % 25 == 0 or len(records) == len(jobs):
                elapsed = time.monotonic()-started
                stream.flush()
                progress=dict(complete=len(records),total=len(jobs),transferredBytes=transferred,cached=cached,
                              fetchedTargets=fetched,fetchesPerSecond=fetched/elapsed,
                              requestsPerSecond=1/pacer.interval,
                              seconds=elapsed,etaSeconds=elapsed/fetched*(len(jobs)-len(records)) if fetched else None)
                print(json.dumps(progress),flush=True)
                atomic_json(args.output.with_suffix('.progress.json'),progress)
    atomic_json(args.output,dict(records=records,method=METHOD,incomplete=False,
                                transferredBytes=transferred,seconds=time.monotonic()-started))
    atomic_json(args.output.with_suffix('.progress.json'),dict(complete=len(records),total=len(records),
                cached=cached,fetchedTargets=fetched,transferredBytes=transferred,seconds=time.monotonic()-started,
                finished=True,etaSeconds=0))


if __name__ == '__main__':
    main()
