// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';
import zlib from 'node:zlib';
import {createFinePlaceLoader,placeNumbers,finePixel} from '../../hosting/sites/fine-place.mjs';
import {localWorkerFixture} from './local_worker_fixture.mjs';
const [data,overlay,output]=process.argv.slice(2);
if(!output)throw Error('Usage: node research/fine/canonical_coordinates_proof.mjs <data-root> <overlay> <report.json>');
const sha=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
const configuration=JSON.parse(await fs.readFile(path.join(overlay,'v6/fine/current.json')));
configuration.labelsRoot=configuration.nativeRoot.replace(/\/replay$/,'/labels');
const queryBytes=await fs.readFile(path.join(data,'fine-validation/b1-worldwide/queries.json')),queries=JSON.parse(queryBytes).slice(0,13);
const placeBytes=await fs.readFile('artifacts/places.tsv'),placeRows=placeBytes.toString('utf8').trim().split('\n').map(row=>row.split('\t')).sort((a,b)=>Number(b[5])-Number(a[5]));
const kebab=value=>value.normalize('NFD').replace(/[^\x00-\x7f]/g,'').toLowerCase().replace(/[^a-z0-9]+/g,'-').replace(/^-|-$/g,'');
const bySlug=new Map();for(const row of placeRows){const slug=`${kebab(row[2].split(',').at(-1))}/${kebab(row[1])}`;if(!bySlug.has(slug))bySlug.set(slug,row);}
let current=null,elapsed=0;const originalNow=Date.now,base=originalNow(),request=localWorkerFixture(overlay,()=>current),routeChecks=[];
Date.now=()=>base+elapsed;
try {
 for(const [stage,value]of [['before',null],['active',configuration],['rollback',null]]) {
  current=value;elapsed+=61000;
  for(const file of ['labels-1.json','labels-2.json']){
   const response=await request(`http://127.0.0.1:5198/api/fine/${file}`);assert.equal(response.status,200);assert.equal(response.headers.get('Cache-Control'),'public, max-age=60');
   const bytes=zlib.gunzipSync(new Uint8Array(await response.arrayBuffer()));
   const expected=await fs.readFile(stage==='active'?path.join(data,configuration.labelsRoot,file):path.join('artifacts/v6',file));
   assert.deepEqual(bytes,expected);const labels=JSON.parse(bytes);
   if(stage==='active')for(const label of labels){const canonical=bySlug.get(label[1]);assert(canonical,`Missing canonical ${label[1]}`);assert.equal(label[2],Number(canonical[3]));assert.equal(label[3],Number(canonical[4]));}
   routeChecks.push({stage,file,rows:labels.length,sha256:sha(bytes)});
  }
 }
} finally {Date.now=originalNow;}
const loader=createFinePlaceLoader(async key=>{if(key==='v6/fine/current.json')return new TextEncoder().encode(JSON.stringify(configuration));try{return await fs.readFile(path.join(data,key));}catch(error){if(error.code==='ENOENT')return null;throw error;}});
const oldLabels=[...JSON.parse(await fs.readFile('artifacts/v6/labels-1.json')),...JSON.parse(await fs.readFile('artifacts/v6/labels-2.json'))];
const newLabels=[...JSON.parse(await fs.readFile(path.join(data,configuration.labelsRoot,'labels-1.json'))),...JSON.parse(await fs.readFile(path.join(data,configuration.labelsRoot,'labels-2.json')))];
assert.equal(newLabels.length,15000);
for(let i=0;i<newLabels.length;i++)for(const field of [0,1,4])assert.equal(newLabels[i][field],oldLabels[i][field],`Catalogue changed row${i}/field${field}`);
async function compare(name,oldPoint,canonicalPoint){
 const before=await loader.load(...oldPoint),after=await loader.load(...canonicalPoint);assert(before&&after,`Missing native weather for ${name}`);
 const months=Array.from({length:12},(_,index)=>{const month=index+1,a=placeNumbers(before,{kind:'month',month}),b=placeNumbers(after,{kind:'month',month});return {month,oldScore:a.score,canonicalScore:b.score,scoreDifference:b.score-a.score,niceDifference:b.niceDayCount-a.niceDayCount,perfectDifference:b.perfectDayCount-a.perfectDayCount,awfulDifference:b.awfulDayCount-a.awfulDayCount};});
 return {name,oldPoint,canonicalPoint,oldPixel:finePixel(...oldPoint),canonicalPixel:finePixel(...canonicalPoint),maxScoreDifference:Math.max(...months.map(row=>Math.abs(row.scoreDifference))),maxNiceDifference:Math.max(...months.map(row=>Math.abs(row.niceDifference))),months};
}
const cities=[];
for(const query of queries){const old=oldLabels.find(row=>row[1]===query.search_result?.slug),now=newLabels.find(row=>row[1]===query.search_result?.slug);if(!old){cities.push({name:query.name,labelAbsent:true});continue;}assert.deepEqual(now.slice(2,4),[query.lat,query.lon]);cities.push(await compare(query.name,old.slice(2,4),now.slice(2,4)));}
const homeBytes=await fs.readFile('src/ui/home.ts'),homes=[];
for(const [,zone,name,region,latitude,longitude]of homeBytes.toString().matchAll(/'([^']+)': city\('([^']+)', '([^']+)', ([^,]+), ([^)]+)\)/g)){
 const old=[Number(latitude),Number(longitude)],candidates=placeRows.filter(row=>row[1]===name).sort((a,b)=>(Number(a[3])-old[0])**2+(Number(a[4])-old[1])**2-((Number(b[3])-old[0])**2+(Number(b[4])-old[1])**2));
 assert(candidates.length,`No canonical ${name}`);homes.push({zone,region,...await compare(name,old,[Number(candidates[0][3]),Number(candidates[0][4])])});
}
const report={written_by:'AI, GPT-6.1 Sol, 2026.10.18',passes:true,definition:'All 15,000 versioned label rows preserve names, slugs, population and order, with coordinates identical to the Worker places.tsv. Actual label API responds with legacy data before activation and after rollback, versioned coordinates while active. Twelve months measured at twelve named validation cities and all eighteen hardcoded home/reference cities; Tenerife south is an explicit airport point absent from the city catalogue.',routeChecks,cities,homes,
 source_sha256:{'artifacts/places.tsv':sha(placeBytes),'src/ui/home.ts':sha(homeBytes),'hosting/sites/worker.mjs':sha(await fs.readFile('hosting/sites/worker.mjs')),'research/fine/CANONICAL_COORDINATES.patch':sha(await fs.readFile('research/fine/CANONICAL_COORDINATES.patch'))}};
await fs.mkdir(path.dirname(output),{recursive:true});await fs.writeFile(output,JSON.stringify(report,null,2)+'\n');
console.log(JSON.stringify({passes:true,cities:cities.length,homes:homes.length,routeChecks,changedPixels:newLabels.filter((row,i)=>JSON.stringify(finePixel(...row.slice(2,4)))!==JSON.stringify(finePixel(...oldLabels[i].slice(2,4)))).length}));
