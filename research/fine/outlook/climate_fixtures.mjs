// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Use actual correction tiles and native replay packets as cross-runtime proof inputs.
import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import {parseArgs} from 'node:util';
import {createFinePlaceLoader,placeNumbers,placeMonths} from '../../../hosting/sites/fine-place.mjs';
const {values}=parseArgs({options:{native:{type:'string'},tiles:{type:'string'},queries:{type:'string'},output:{type:'string'}}});
for(const name of ['native','tiles','queries','output'])if(!values[name])throw new Error(`Missing --${name}`);
await fs.mkdir(values.output,{recursive:true});
const manifest=JSON.parse(await fs.readFile(path.join(values.native,'manifest.json')));
const pointer={nativeRoot:'replay',tileRoot:'temperature',generation:manifest.generation,eveningModel:'uniform',status:'local-validation'};
const consumed=new Map(),sha=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
const loader=createFinePlaceLoader(async key=>{
 if(key==='v6/fine/current.json')return new TextEncoder().encode(JSON.stringify(pointer));
 const prefix=key.startsWith('replay/')?'replay/':'temperature/',root=prefix==='replay/'?values.native:values.tiles;
 const file=path.join(root,key.slice(prefix.length));
 try{const bytes=await fs.readFile(file);consumed.set(file,{bytes:bytes.length,sha256:sha(bytes)});return bytes;}catch(error){if(error.code==='ENOENT')return null;throw error;}
});
const periods=[...Array.from({length:12},(_,index)=>({months:[index+1]})),{months:Array.from({length:12},(_,index)=>index+1)},...[ [12,1,2],[3,4,5],[6,7,8],[9,10,11] ].map(months=>({months}))];
const queries=JSON.parse(await fs.readFile(values.queries)),unavailable=[];let available=0;
const input=path.join(values.output,'input.jsonl'),expected=path.join(values.output,'expected.jsonl');
await fs.writeFile(input,'');await fs.writeFile(expected,'');
const replacer=(_,value)=>ArrayBuffer.isView(value)?Array.from(value):value;
for(const query of queries){
 const packet=await loader.load(query.lat,query.lon);
 if(!packet){unavailable.push(query);continue;}
 const id=query.slug??query.id,output={id,numbers:periods.map(period=>placeNumbers(packet,period)),months:placeMonths(packet)};
 await fs.appendFile(input,JSON.stringify({id,packet,periods},replacer)+'\n');
 await fs.appendFile(expected,JSON.stringify(output)+'\n');available++;
}
const report={written_by:'AI, GPT-6.1 Sol, 2026.10.18',generation:manifest.generation,queries:queries.length,available,unavailable,periodsPerPlace:periods.length,physicalMonthsPerPlace:12,
 nativeRoot:values.native,tileRoot:values.tiles,queriesSha256:sha(await fs.readFile(values.queries)),moduleSha256:sha(await fs.readFile(new URL('../../../hosting/sites/fine-place.mjs',import.meta.url))),
 inputSha256:sha(await fs.readFile(input)),expectedSha256:sha(await fs.readFile(expected)),consumed:Object.fromEntries(consumed)};
await fs.writeFile(path.join(values.output,'fixtures-provenance.json'),JSON.stringify(report,null,2)+'\n');
console.log(JSON.stringify({queries:queries.length,available,unavailable:unavailable.length,periodsPerPlace:periods.length}));
