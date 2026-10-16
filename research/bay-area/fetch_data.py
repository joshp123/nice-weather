# Written by AI on 2026.10.17 by model GPT-6 (Codex). Research only.
import concurrent.futures, datetime as dt, json, pathlib, time, urllib.request, urllib.parse
ROOT=pathlib.Path(__file__).resolve().parent
STATIONS=[('coast','Half Moon Bay','USC00043714'),('sf','San Francisco Downtown','USW00023272'),('sfo','SFO','USW00023234'),('oakland','Oakland Airport','USW00023230'),('berkeley','Berkeley','USC00040693'),('palo_alto','Palo Alto area (Redwood City)','USC00047339'),('san_jose','San Jose','USW00023293'),('livermore','Livermore Airport','USW00023285'),('oceanside','SF Oceanside','USC00047767')]
def stations():
    rows={l[:11]:dict(lat=float(l[12:20]),lon=float(l[21:30]),elevation=float(l[31:37]),name=l[41:71].strip()) for l in (ROOT/'ghcnd-stations.txt').read_text().splitlines()}
    return [dict(slug=s,place=p,station=i,**rows[i]) for s,p,i in STATIONS]
def fetch(url,path):
    if path.exists(): return
    for attempt in range(8):
        try:
            with urllib.request.urlopen(url,timeout=180) as response: raw=response.read()
            if b'limit exceeded' in raw.lower(): raise RuntimeError('limit exceeded')
            path.write_bytes(raw);return
        except Exception as e:
            print(str(path.name),str(e),flush=True);time.sleep(45+attempt*10)
    raise RuntimeError(url)
def noaa(station):
    fetch('https://www.ncei.noaa.gov/pub/data/ghcn/daily/all/'+station['station']+'.dly',ROOT/(station['slug']+'.dly'))
def era(station):
    query=dict(latitude=station['lat'],longitude=station['lon'],start_date='2016-01-01',end_date='2025-12-31',hourly='temperature_2m,dew_point_2m,precipitation,cloud_cover,wind_speed_10m,direct_normal_irradiance',models='era5',timezone='GMT')
    path=ROOT/(station['slug']+'_era5.json')
    if path.exists():return
    url='https://archive-api.open-meteo.com/v1/archive?'+urllib.parse.urlencode(query)
    (ROOT/(station['slug']+'_request.txt')).write_text(url+'\n')
    fetch(url,path);print('ERA5 fetched '+station['slug'],flush=True);time.sleep(42)
if __name__=='__main__':
    import sys
    ss=stations();(ROOT/'stations.json').write_text(json.dumps(ss,indent=2))
    if sys.argv[1]=='noaa':
        with concurrent.futures.ThreadPoolExecutor(max_workers=5) as pool:list(pool.map(noaa,ss))
    else:
        for s in ss[:8]:era(s)
