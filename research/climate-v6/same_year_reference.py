# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
import sys,json,pathlib,datetime as dt,numpy as np
sys.path.insert(0,str(pathlib.Path(__file__).resolve().parent));import validate,pipeline
rule=validate.load_rule();places,where=rule.load_places();dates=pipeline.chunk_dates(82)[1];ids=np.array([(d-dt.date(2016,1,1)).days for d in dates]);months=np.array([d.month for d in dates]);outdir=pathlib.Path('/tmp/nwf-agents/overnight/climate');diagnostics=json.loads((outdir/'PROVISIONAL-PLACES.json').read_text());same={}
for name,published in diagnostics.items():
 if 'monthly' not in published:continue
 p=places[name];scores,parts=rule.day_scores(p['T'][ids],p['dew'][ids],p['wind'][ids],p['dni'][ids],p['rain'][ids],p['daylight'][ids]);rows=[]
 for m in range(1,13):
  mask=months==m;pub=published['monthly'][m-1];score=float(scores[mask].mean());r={'month':m,'fable_same_days_full_score':round(score,2),'provisional_score':pub['published_score'],'difference_to_same_days':round(pub['published_score']-score,2),'ten_year_reference':pub['reference'],'fable_same_days_air_high_c':round(float(p['T'][ids][mask,:11].max(1).mean()),2),'fable_same_days_dew_c':round(float(p['dew'][ids][mask].mean()),2),'fable_same_days_wet_percent':round(float((p['rain'][ids][mask]>=1).mean()*100),2),'fable_same_days_rain_mm':round(float(p['rain'][ids][mask].mean()),2),'fable_same_days_wind_kmh':round(float(p['wind'][ids][mask].mean()),2),'fable_same_days_sunny_percent':round(float((((p['dni'][ids][mask,:11]>=300)&(np.arange(11)<p['daylight'][ids][mask,None])).sum(1)/p['daylight'][ids][mask]).mean()*100),2)};rows.append(r)
 same[name]={'reference_grid':where[name],'same_days':rows};print(name,[(r['month'],r['fable_same_days_full_score'],r['provisional_score'],r['difference_to_same_days']) for r in rows]);print('largest same-days diff',max(rows,key=lambda r:abs(r['difference_to_same_days'])))
(outdir/'SAME-YEAR-REFERENCE.json').write_text(json.dumps(same,indent=2,ensure_ascii=False)+'\n')
