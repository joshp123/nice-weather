// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Compare two releases through the shared default-place function and one ranking rule.
import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import {parseArgs} from 'node:util';
import {gunzipSync} from 'node:zlib';
import ts from 'typescript';
import {decodeNamedTable,namedPlaceNumbers,createNamedPlaceLoader,namedPlaceMonths} from '../../hosting/sites/named-place.mjs';

const {values}=parseArgs({options:{before:{type:'string'},after:{type:'string'},places:{type:'string'},labels:{type:'string'},output:{type:'string'},satellite:{type:'string'},live:{type:'string'}}});
for(const key of ['before','after','places','labels','output'])if(!values[key])throw Error(`Missing --${key}`);
await fs.mkdir(values.output,{recursive:true});
const sourceUrl=source=>`data:text/javascript;base64,${Buffer.from(ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText).toString('base64')}`;
const countryUrl=sourceUrl(await fs.readFile('src/ui/countries.ts','utf8'));
const rankingSource=await fs.readFile('src/ui/ranking.ts','utf8');
const ranking=await import(sourceUrl(rankingSource.replace("'./countries'",JSON.stringify(countryUrl))));
await fs.writeFile(path.join(values.output,'ranking.ts'),rankingSource);
const coordinates=(latitude,longitude)=>`${Math.round(latitude*1e5)},${Math.round(longitude*1e5)}`;
async function open(root){
  const table=decodeNamedTable(gunzipSync(await fs.readFile(path.join(root,'climate.bin.gz'))));
  const points=new Map();
  for(let index=0;index<table.count;index++)points.set(`${table.view.getInt32(32+index*8,true)},${table.view.getInt32(36+index*8,true)}`,{climate:table,climateIndex:index,forecastIndex:-1});
  const loader=createNamedPlaceLoader(async key=>{
    if(key==='v6/named/current.json')return Buffer.from(JSON.stringify({root:'release',generation:root,factsRows:16384}));
    if(!key.startsWith('release/'))return null;
    try{return await fs.readFile(path.join(root,key.slice(8)));}catch(error){if(error.code==='ENOENT')return null;throw error;}
  });
  return {points,loader,count:table.count};
}
const before=await open(values.before),after=await open(values.after),places=JSON.parse(await fs.readFile(values.places));
const rows=[];
for(const place of places){
  const key=coordinates(place.latitude,place.longitude);
  const previous=await before.loader.load(place.latitude,place.longitude),corrected=await after.loader.load(place.latitude,place.longitude);
  if(!previous||!corrected)throw Error(`Missing named row ${place.slug}`);
  const row={place,before:{year:namedPlaceNumbers(previous,{kind:'year'}),months:namedPlaceMonths(previous)},after:{year:namedPlaceNumbers(corrected,{kind:'year'}),months:namedPlaceMonths(corrected)}};
  if(values.live){
    const live=JSON.parse(await fs.readFile(path.join(values.live,place.slug.replace('/','-')+'.json'))).year;
    for(const field of ['score','niceDayCount','perfectDayCount','awfulDayCount'])if(row.before.year[field]!==live.judgement[field])throw Error(`Baseline differs from captured live ${place.slug}/${field}`);
  }
  for(let month=0;month<12;month++)for(const field of ['highC','feelsAt21C','dewPointC','wetDaysPercent','windKmh'])if(row.before.months[month][field]!==row.after.months[month][field])throw Error(`Sunshine altered physical ${place.slug}/${month+1}/${field}`);
  rows.push(row);
}
const labels=(await Promise.all([1,2].map(async level=>JSON.parse(await fs.readFile(path.join(values.labels,`labels-${level}.json`),'utf8'))))).flat();
function rank(release,period){
  const candidates=labels.map(([name,slug,latitude,longitude,population])=>{
    const row=release.points.get(coordinates(latitude,longitude)),numbers=row&&namedPlaceNumbers(row,period);
    return numbers?{name,slug,latitude,longitude,population,score:numbers.score,niceDayCount:numbers.niceDayCount,perfectDayCount:numbers.perfectDayCount}:null;
  }).filter(place=>place&&place.population>=ranking.WORLD_POPULATION&&ranking.inOutlookRegion(place,'the world'));
  return {candidates:candidates.length,places:ranking.rankPlaces(candidates,ranking.WORLD_POPULATION,10)};
}
const rankings={};
for(const [name,period]of [['year',{kind:'year'}],['nov-mar',{kind:'months',startMonth:11,endMonth:3}]])rankings[name]={before:rank(before,period),after:rank(after,period)};
const report={writtenBy:'AI, GPT-6.1 Sol, 2026-10-18',passes:true,comparison:'Both releases use the same current shared named-place and ranking functions; no ranking-rule change is included in this before/after.',rankingSha256:crypto.createHash('sha256').update(rankingSource).digest('hex'),beforeRoot:values.before,afterRoot:values.after,places:rows,rankings};
await fs.writeFile(path.join(values.output,'comparison.json'),JSON.stringify(report,null,2)+'\n');
console.log(JSON.stringify({passes:true,places:rows.map(row=>({name:row.place.validationName,before:[row.before.year.niceDayCount,row.before.year.perfectDayCount],after:[row.after.year.niceDayCount,row.after.year.perfectDayCount]})),rankings},null,2));
