# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
import sys,pathlib,json,numpy as np
from PIL import Image
sys.path.insert(0,str(pathlib.Path(__file__).resolve().parent));import validate
root=validate.DEFAULT_DATA;points=np.fromfile(root/'v6/points.bin',dtype=validate.POINT_DTYPE,offset=4);N=(root/'v6/cells.bin').stat().st_size//4;metadata=json.loads((root/'v6/climate/meta.json').read_text());directory=root/'climate-v6-work/assemblies'/metadata['buildId']/'v6/climate';pub=json.load(open('/tmp/nwf-agents/overnight/climate/PROVISIONAL-PLACES.json'));same=json.load(open('/tmp/nwf-agents/overnight/climate/SAME-YEAR-REFERENCE.json'));out={}
for name in ['Rotterdam','Las Palmas']:
 p=pub[name];fine=int(round((90-p['fine_lat'])*4))*1440+int(round((p['fine_lon']+180)*4));rows=[]
 for month in range(1,13):
  records=np.fromfile(directory/f'month-{month:02}.bin',dtype=np.uint8).reshape(N,8,15)[p['cell']];pixel=np.array(Image.open(directory/f'offsets-{month:02}.png')).ravel()[fine];offset=(float(pixel)-128)/10;score=float(validate.score_records(records,offset).mean());rows.append({'month':month,'published_score':score,'offset_c':offset,**same[name]['same_days'][month-1]})
 out[name]={'fine_lat':p['fine_lat'],'fine_lon':p['fine_lon'],'monthly':rows}
out['Las Palmas']['source_height_m']=366.22855;out['Las Palmas']['reference_api_elevation_m']=0;out['Las Palmas']['reference_temperature_correction_c']=366.22855*.0065
pathlib.Path('/tmp/nwf-agents/overnight/climate/REMAINING-PLACES.json').write_text(json.dumps(out,indent=2)+'\n');print([(k,[round(r['published_score'],2) for r in v['monthly']]) for k,v in out.items()])
