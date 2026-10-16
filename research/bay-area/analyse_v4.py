# Written by AI on 2026.10.17 by model GPT-6 (Codex). Research only.
import calendar, datetime as dt, importlib.util, json, pathlib, zoneinfo
import numpy as np
ROOT=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('v3soft','/tmp/nwf-agents/round3/opus/v3soft.py'); scoring=importlib.util.module_from_spec(spec);spec.loader.exec_module(scoring)
import sys; sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent)); import v4shim; scoring.score = v4shim.score
TZ=zoneinfo.ZoneInfo('America/Los_Angeles')
def station_observations(slug):
    out={}
    for line in (ROOT/(slug+'.dly')).read_text().splitlines():
        year,month,element=int(line[11:15]),int(line[15:17]),line[17:21]
        if not 2016<=year<=2025 or element not in ('TMAX','TMIN'):continue
        for day in range(1,calendar.monthrange(year,month)[1]+1):
            value=line[21+(day-1)*8:29+(day-1)*8]
            if int(value[:5])==-9999 or value[6]!=' ':continue
            date=dt.date(year,month,day).isoformat();out.setdefault(date,{})[element]=int(value[:5])/10
    return out
if __name__=='__main__':
    out={}
    for s in json.loads((ROOT/'stations.json').read_text()):
        obs=station_observations(s['slug']);pairs={d:v for d,v in obs.items() if len(v)==2 and v['TMAX']>v['TMIN']}
        print(s['slug'],len(pairs),[sum(d[5:7]==f'{m:02d}' for d in pairs) for m in range(1,13)],flush=True)
        path=ROOT/(s['slug']+'_era5.json')
        if not path.exists():continue
        raw=json.loads(path.read_text());h=raw['hourly'];t=np.array(h['temperature_2m'],float)
        ix,months=scoring.local_index(h['time'][0],len(t),str(TZ),(2016,2025))
        epoch=dt.datetime.fromisoformat(h['time'][0]).replace(tzinfo=dt.timezone.utc)
        dates=[(epoch+dt.timedelta(hours=int(i[0]))).astimezone(TZ).date() for i in ix]
        valid=np.array([d.isoformat() in pairs for d in dates]);ix=ix[valid];months=months[valid];dates=[d for d,v in zip(dates,valid) if v]
        daily_min=[];daily_max=[]
        for day in dates:
            start=int((dt.datetime.combine(day,dt.time(),TZ).astimezone(dt.timezone.utc)-epoch).total_seconds()/3600)
            end=int((dt.datetime.combine(day+dt.timedelta(days=1),dt.time(),TZ).astimezone(dt.timezone.utc)-epoch).total_seconds()/3600)
            daily_min.append(np.min(t[start:end]));daily_max.append(np.max(t[start:end]))
        lo,hi=np.array(daily_min),np.array(daily_max)
        observed_low=np.array([pairs[d.isoformat()]['TMIN'] for d in dates]);observed_high=np.array([pairs[d.isoformat()]['TMAX'] for d in dates])
        station_t=observed_low[:,None]+(t[ix]-lo[:,None])/(hi-lo)[:,None]*(observed_high-observed_low)[:,None]
        a={k:np.array(h[k],float) for k in ['dew_point_2m','wind_speed_10m','direct_normal_irradiance','precipitation']}
        def scores(temp):return scoring.score(temp,a['dew_point_2m'][ix],a['wind_speed_10m'][ix],np.nan_to_num(a['direct_normal_irradiance'][ix+1]),np.nan_to_num(a['precipitation'][ix+1]).sum(1))[0]
        methods={'era5':scores(t[ix]),'station':scores(station_t)}
        prism=json.loads((ROOT/'prism_final_normals.json').read_text())
        # Inspect structure before choosing baseline.
        result=dict(station=s,days=len(dates),coverage=round(len(dates)/3653,5),monthly_high_station=[float(observed_high[months==m].mean()) for m in range(1,13)],monthly_high_era5=[float(hi[months==m].mean()) for m in range(1,13)],monthly_low_era5=[float(lo[months==m].mean()) for m in range(1,13)],methods={})
        prism_key=s['slug']
        baseline_path=ROOT/'prism_era5_1991_2020_normals.json'
        if baseline_path.exists():
            baseline=json.loads(baseline_path.read_text())[s['slug']]
            normal=prism[prism_key]
            high_offset=np.array(normal['tmax'])-np.array(baseline['tmax'])
            low_offset=np.array(normal['tmin'])-np.array(baseline['tmin'])
            mean_offset=(high_offset+low_offset)/2
            methods['prism']=scores(t[ix]+mean_offset[months-1,None])
            weight=(t[ix]-lo[:,None])/(hi-lo)[:,None]
            methods['prism_extrema']=scores(t[ix]+low_offset[months-1,None]+weight*(high_offset-low_offset)[months-1,None])
            result['prism_mean_offset']=mean_offset.tolist()
            result['prism_high_offset']=high_offset.tolist()
            result['prism_low_offset']=low_offset.tolist()
        assert all(np.isfinite(score).all() for score in methods.values())
        np.savez_compressed(ROOT/(s['slug']+'_daily_scores_v4.npz'),dates=np.array([d.isoformat() for d in dates]),months=months,**methods)
        result['monthly_valid_days']=[int((months==m).sum()) for m in range(1,13)]
        result['era5_grid']={k:raw[k] for k in ['latitude','longitude','elevation']}
        result['monthly_low_station']=[float(observed_low[months==m].mean()) for m in range(1,13)]
        for name,score in methods.items():
            counts=[float((score[months==m]>=55).mean())*calendar.monthrange(2023,m)[1] for m in range(1,13)]
            counts[1]=float((score[months==2]>=55).mean())*28.3
            result['methods'][name]=dict(annual=sum(counts),monthly=counts)
        out[s['slug']]=result
    (ROOT/'results_v4.json').write_text(json.dumps(out,indent=2))
