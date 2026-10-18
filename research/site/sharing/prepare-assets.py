# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
# Export the existing logo and outlook geography for the shared image template.
import base64, gzip, io, json, pathlib, struct
from PIL import Image, ImageDraw
root=pathlib.Path(__file__).resolve().parents[3]
brand=json.loads(pathlib.Path('/tmp/nwf-agents/round5/sharing/brand.json').read_text())
logo=Image.frombytes('L',(brand['width'],brand['height']),bytes(brand['alpha']))
logo=logo.crop(logo.getbbox())
land=Image.new('L',(1120,338)); draw=ImageDraw.Draw(land)
def paths(name):
 data=memoryview((root/'outlook/assets'/name).read_bytes()); offset=4
 for _ in range(struct.unpack_from('<I',data)[0]):
  count=struct.unpack_from('<I',data,offset)[0]; offset+=4
  points=struct.unpack_from('<'+'hh'*count,data,offset); offset+=count*4
  yield [(1120*(points[i]/100+180)/360,338*(84-points[i+1]/100)/144) for i in range(0,len(points),2)]
for path in paths('land.bin'):
 if len(path)>2: draw.polygon(path,fill=1)
for path in paths('borders.bin'):
 if len(path)>1: draw.line(path,fill=2,width=1)
encode=lambda pixels:base64.b64encode(gzip.compress(pixels)).decode()
module='// Written by AI on 2026.10.18 by model GPT-6.1 Sol. Existing app wordmark and outlook geography.\n'
module+=f'export const shareLogo={{width:{logo.width},height:{logo.height},mask:"{encode(logo.tobytes())}"}};\n'
module+=f'export const shareLand={{width:1120,height:338,mask:"{encode(land.tobytes())}"}};\n'
rgba=Image.new('RGBA',logo.size,(20,20,19,0));rgba.putalpha(logo);stream=io.BytesIO();rgba.save(stream,format='PNG')
(root/'outlook/assets/nice-wordmark.png').write_bytes(stream.getvalue())
module+=f'export const shareWordmark="data:image/png;base64,{base64.b64encode(stream.getvalue()).decode()}";\n'
(root/'hosting/sites/share-assets.mjs').write_text(module)
print('Shared logo',logo.size,'geography',len(module),'bytes')
