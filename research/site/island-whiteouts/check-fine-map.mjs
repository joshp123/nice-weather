// Written by AI on 2026.10.26 by model GPT-6.
import fs from 'node:fs';
import assert from 'node:assert/strict';
import {fineMapTileAddress,fineMapNumbers,fineMapPointNumbers} from '../../../hosting/sites/fine-map.mjs';
import {SCORE_BANDS} from '../../../hosting/sites/score-bands.mjs';
const latitude=38.38,longitude=26.13,address=fineMapTileAddress(latitude,longitude),size=260,halo=2;
const tile={counts:new Uint8Array(size*size*36).fill(255),row:address.tileRow,column:address.tileColumn,size,halo,version:3};
const x=(longitude+180)*120-.5-tile.column*256+halo,y=(90-latitude)*120-.5-tile.row*256+halo;
const baseX=Math.floor(x),baseY=Math.floor(y),pixels=size*size;
const validPixels=[];
for(let row=baseY-1;row<=baseY+2;row++)for(let column=baseX-1;column<=baseX+2;column++) {
 const pixel=row*size+column;
 for(let month=0;month<12;month++)tile.counts.set([0x88,0x88,0x08],(month*pixels+pixel)*3);
 validPixels.push([pixel,fineMapNumbers(tile,pixel,{kind:'year'}).score]);
}
const complete=fineMapPointNumbers(tile,latitude,longitude,{kind:'year'});
assert(complete&&complete.score===100);
const first=(baseY-1)*size+baseX-1;
for(let month=0;month<12;month++)tile.counts[(month*pixels+first)*3]=255;
assert.equal(fineMapPointNumbers(tile,latitude,longitude,{kind:'year'}),null);
const fineSource=fs.readFileSync(new URL('../../../src/engine/fineMap.ts',import.meta.url),'utf8');
const shaders=fs.readFileSync(new URL('../../../src/engine/shaders.ts',import.meta.url),'utf8');
const spline=shaders.match(/export const splineFunctions = (`[\s\S]*?`);/)[1];
const template=fineSource.match(/const fragment=(`[\s\S]*?`);\nconst resolveVertex/)[1];
const fragment=Function('splineFunctions','BANDS',`return ${template}`)(Function(`return ${spline}`)(),SCORE_BANDS);
process.stdout.write(JSON.stringify({fragment,size,validPixels,first,latitude,longitude,pixelOrigin:[tile.column*256-halo,tile.row*256-halo],cpu:{completeScore:complete.score,partial:null}}));
