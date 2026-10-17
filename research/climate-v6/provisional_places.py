# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
import sys,json,pathlib,numpy as np
sys.path.insert(0,str(pathlib.Path(__file__).resolve().parent))
import validate,pipeline
root=validate.DEFAULT_DATA;cells,points,_=validate.validate_static(root);refs=validate.reference_months();latitudes=90-points['fine']//1440/4;longitudes=points['fine']%1440/4-180
months=np.array([d.month for d in pipeline.chunk_dates(82)[1]]);out={}
for name,(lat,lon) in validate.PLACES.items():
 distances=(latitudes-lat)**2+((longitudes-lon)*np.cos(np.deg2rad(lat)))**2;pi=int(np.argmin(distances));point=points[pi];r,c=divmod(int(point['fine']),1440);tile=f'r{r//12:02}c{((c+720)%1440)//12:03}.npz';path=root/'climate-v6-work/chunk82'/tile
 if not path.exists():out[name]={'status':'tile not available','tile':tile};continue
 with np.load(path) as saved:
  ci=list(saved['cells']).index(point['cell']);point_ix=list(saved['point_ids']).index(pi)
  f=saved['sums'][ci]/saved['counts'][ci];monthly=saved['monthly'][point_ix];daylight=saved['daylight'][point_ix]
 records=pipeline.encode_records(f);rows=[]
 for m in range(1,13):
  mask=months==m;v=f[mask];dl=daylight[mask];prefix=np.cumsum((v[:,:11]+v[:,11:22])*.5,axis=1);cell_felt=np.take_along_axis(prefix,dl[:,None].astype(int)-1,axis=1).sum()
  offset=(monthly[m-1,6]-cell_felt)/monthly[m-1,7];encoded_offset=(np.clip(np.rint(offset*10)+128,0,254)-128)/10
  compact=validate.score_records(records[mask]);order=np.argsort(compact,kind='stable');ranks=np.floor((np.arange(8)+.5)*mask.sum()/8).astype(int);selected=records[mask][order[ranks]]
  score=float(validate.score_records(selected,float(encoded_offset)).mean());ref=refs[name][m-1];mstats=monthly[m-1]
  rows.append({'month':m,'published_score':round(score,2),'reference':ref,'difference':round(score-ref,2),'offset_c':float(encoded_offset),'all_cell_days_score':round(float(compact.mean()),2),'sample_cell_score':round(float(validate.score_records(selected).mean()),2),'fine_air_high_c':round(float(mstats[0]/mask.sum()),2),'fine_felt_evening_c':round(float(mstats[1]/mask.sum()),2),'fine_dew_c':round(float(mstats[2]/mask.sum()),2),'fine_wet_days_percent':round(float(mstats[3]/mask.sum()*100),2),'fine_sunny_percent':round(float(mstats[4]/mstats[7]*100),2),'fine_wind_kmh':round(float(mstats[5]/mask.sum()),2),'sample_rain_mean_mm':round(float(((selected[:,11].astype(float)/50)**2).mean()),2),'sample_dew_mean_c':round(float((selected[:,10].astype(float)/3-30).mean()),2),'sample_wind_mean_kmh':round(float((selected[:,12].astype(float)/2).mean()),2)})
 out[name]={'status':'real chunk82 intermediate; reproduction of published selection/offset','tile':tile,'fine_lat':90-r/4,'fine_lon':c/4-180,'distance_km':round(float(np.sqrt(distances[pi])*111.2),2),'cell':int(point['cell']),'monthly':rows}
p=pathlib.Path('/tmp/nwf-agents/overnight/climate/PROVISIONAL-PLACES.json');p.write_text(json.dumps(out,indent=2,ensure_ascii=False)+'\n')
lines=['Written by AI on 2026.10.17 by model GPT-6.1 Sol.','','Provisional reproduction of published8-day selection and encoded fine-point offsets from real chunk82 intermediates. No production climate files were created.','','| Place | Jan | Feb | Mar | Apr | May | Jun | Jul | Aug | Sep | Oct | Nov | Dec |','|---|'+'---:|'*12]
for name,p in out.items():
 if 'monthly' not in p:continue
 for key,label in [('published_score','provisional'),('reference','reference'),('difference','difference')]:lines.append('| '+name+' '+label+' | '+' | '.join(str(row[key]) for row in p['monthly'])+' |')
lines+=['','JSON includes selected coordinates, monthly high/evening/dew/wet/sun/wind facts, cell scores and offsets. Months differing by10points or more require investigation, not assumption of sampling noise.','']
pathlib.Path('/tmp/nwf-agents/overnight/climate/PROVISIONAL-PLACES.md').write_text('\n'.join(lines))
for name,p in out.items():
 if 'monthly' not in p:print(name,p);continue
 print(name,[(m['month'],m['published_score'],m['reference'],m['difference']) for m in p['monthly']]);print('worst',max(p['monthly'],key=lambda m:abs(m['difference'])))
