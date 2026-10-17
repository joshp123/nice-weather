# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
import sys,pathlib,json,numpy as np
sys.path.insert(0,str(pathlib.Path(__file__).resolve().parent));import pipeline,validate
root=validate.DEFAULT_DATA;cells=np.fromfile(root/'v6/cells.bin',dtype='<u2').reshape(-1,2);points=np.fromfile(root/'v6/points.bin',dtype=pipeline.POINT_DTYPE,offset=4)
months=np.array([d.month for d in pipeline.chunk_dates(82)[1]]);mask=months==11;sums=np.zeros((len(cells),mask.sum(),29),np.float32);counts=np.zeros(len(cells),int)
for path in (root/'climate-v6-work/chunk82').glob('r*.npz'):
 if '.partial' in path.name:continue
 with np.load(path) as a:sums[a['cells']]+=a['sums'][:,mask];counts[a['cells']]+=a['counts']
f=sums/counts[:,None,None];records=pipeline.encode_records(f);full=pipeline.score_features(f).mean(1);compact=validate.score_records(records).mean(1);error=np.abs(full-compact);i=int(error.argmax());v=f[i];recon=pipeline.decode_records(records[i]);out={'month':11,'cell_ordinal':i,'cell_row_column':cells[i].tolist(),'center_latitude':89.5-float(cells[i,0]),'center_longitude':float(cells[i,1])-179.5,'fine_points':int(counts[i]),'full_mean':float(full[i]),'compact_mean':float(compact[i]),'absolute_error':float(error[i]),'shade_means_10_to_21':v[:,11:23].mean(0).tolist(),'compact_shade_means_10_to_21':recon[:,11:23].mean(0).tolist(),'sun_means_10_to_20':v[:,:11].mean(0).tolist(),'mean_daylight':float(v[:,27].mean()),'daily':[{'date':str(d),'full':float(a),'compact':float(b),'shade10':float(t[11]),'shade11':float(t[12]),'shade12':float(t[13])} for d,a,b,t in zip(pipeline.chunk_dates(82)[1][mask],pipeline.score_features(v),validate.score_records(records[i]),v)]}
ablations={}
for label,indices in [('restore_full_sun',list(range(11))),('restore_full_shade',list(range(11,23))),('restore_sun17',[7]),('restore_nuisance',list(range(23,28)))]:
 changed=recon.copy();changed[:,indices]=v[:,indices];ablations[label]=float(pipeline.score_features(changed).mean())
out['compact_ablations']=ablations
path=pathlib.Path('/tmp/nwf-agents/overnight/climate/GLOBAL-WORST-COMPACT.json');path.write_text(json.dumps(out,indent=2)+'\n');print(json.dumps({k:v for k,v in out.items() if k!='daily'},indent=2))
