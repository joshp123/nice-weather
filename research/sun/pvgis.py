"""Written by AI on 2026.10.17 by model GPT-6.1 Sol.

An independent SARAH-3 check of the Africa/Europe sunshine controls. SARAH
values are satellite snapshots: linearly interpolating DNI across snapshots
approximates hourly means; it is not measured hourly sunshine duration.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import datetime as dt
import hashlib
import json
from pathlib import Path
import subprocess
from urllib.parse import urlencode
from zoneinfo import ZoneInfo

import numpy as np

from era5_measurement import PLACES as PRODUCT_PLACES
from era5_measurement import pipeline

ROOT = Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/sun/pvgis')
LICENCE = 'https://joint-research-centre.ec.europa.eu/photovoltaic-geographical-information-system-pvgis/general-information/usage-conditions-data-protection_en'
PLACES = dict(PRODUCT_PLACES)
PLACES['swakopmund-era5-point'] = ('Swakopmund ERA5 point', -22.5, 14.5, 'Africa/Windhoek')
TARGETS = ['swakopmund', 'swakopmund-era5-point', 'cape-town', 'malaga', 'seville', 'rotterdam', 'tenerife-south']


def fetch(job):
    slug, year = job
    name, latitude, longitude, zone_name = PLACES[slug]
    parameters = dict(lat=latitude, lon=longitude, startyear=year, endyear=year,
                      raddatabase='PVGIS-SARAH3', pvcalculation=0, components=1,
                      outputformat='json', angle=0, usehorizon=0)
    url = 'https://re.jrc.ec.europa.eu/api/v5_3/seriescalc?' + urlencode(parameters)
    path = ROOT / f'{slug}-{year}.json'
    if not path.exists():
        subprocess.run(['curl', '-fLsS', '--retry', '2', '--connect-timeout', '20', '--max-time', '90', url, '-o', str(path)], check=True)
    payload = json.loads(path.read_text())
    if payload['inputs']['meteo_data']['radiation_db'] != 'PVGIS-SARAH3':
        raise ValueError(f'{path}: non-independent radiation source')
    data = payload['outputs']['hourly']
    times = np.array([dt.datetime.strptime(row['time'], '%Y%m%d:%H%M').replace(tzinfo=dt.timezone.utc).timestamp() for row in data])
    elevation = np.array([row['H_sun'] for row in data])
    beam = np.array([row['Gb(i)'] for row in data])
    dni = np.divide(beam, np.sin(np.deg2rad(elevation)), out=np.zeros(len(data)), where=elevation > 0)
    zone = ZoneInfo(zone_name)
    daily = []
    date = dt.date(year, 1, 1)
    while date.year == year:
        noon = dt.datetime.combine(date, dt.time(12), zone)
        offset = noon.utcoffset().total_seconds() / 3600
        _, equation, declination = pipeline.solar(date.timetuple().tm_yday, 12, latitude, longitude)
        lat = np.deg2rad(latitude)
        argument = np.cos(np.deg2rad(90.833))/(np.cos(lat)*np.cos(declination))-np.tan(lat)*np.tan(declination)
        sunset_utc = (720-4*longitude-equation+4*np.rad2deg(np.arccos(np.clip(argument,-1,1))))/60
        sunset_local = sunset_utc+offset-24*np.rint((offset-longitude/15)/24)
        daylight = int(np.clip(np.ceil(sunset_local)-10,1,11))
        hourly = []
        for hour in range(10, 21):
            start = dt.datetime.combine(date, dt.time(hour), zone).timestamp()
            minute_midpoints = start + np.arange(60) * 60 + 30
            hourly.append(float(np.interp(minute_midpoints, times, dni).mean()))
        sunny = int(np.sum(np.array(hourly[:daylight]) >= 300))
        daily.append(dict(place=name, slug=slug, date=str(date), month=date.month, sunny_share=sunny/daylight,
                          sunny_hours=sunny, daylight_hours=daylight, hourly_dni=hourly))
        date += dt.timedelta(days=1)
    provenance = dict(slug=slug, year=year, url=url, sha256=hashlib.sha256(path.read_bytes()).hexdigest(),
                      licence_source=LICENCE, method='DNI = Gb(i) / sin(H_sun); linear interpolation between satellite snapshots, minute-midpoint integration per civil clock hour; same integer daylight mask as v6. No temperature/wind data from PVGIS used.')
    return daily, provenance


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--years', type=int, nargs='+', default=[2020, 2022])
    arguments = parser.parse_args()
    ROOT.mkdir(exist_ok=True)
    with ThreadPoolExecutor(max_workers=4) as executor:
        results = list(executor.map(fetch, [(slug, year) for slug in TARGETS for year in arguments.years]))
    daily = [row for rows, _ in results for row in rows]
    monthly = []
    for slug in TARGETS:
        for month in range(1, 13):
            selected = [row for row in daily if row['slug'] == slug and row['month'] == month]
            monthly.append(dict(slug=slug, place=PLACES[slug][0], month=month, days=len(selected),
                                sunny_share_percent=100*float(np.mean([row['sunny_share'] for row in selected]))))
    (ROOT/'daily.json').write_text(json.dumps(daily, separators=(',', ':'))+'\n')
    (ROOT/'monthly.json').write_text(json.dumps(monthly, indent=2)+'\n')
    (ROOT/'provenance.json').write_text(json.dumps([info for _, info in results], indent=2)+'\n')
    print('\n'.join(f"{row['place']} {row['month']:02}: {row['sunny_share_percent']:.1f}%" for row in monthly))


if __name__ == '__main__':
    main()
