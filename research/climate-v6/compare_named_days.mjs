// Written by AI on 2026.10.19 by model GPT-6.1 Sol.
// Read the production named-place decoder and count rules; never change data.
import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import zlib from 'node:zlib';
import {parseArgs} from 'node:util';
import {build as bundle} from 'esbuild';
import {decodeNamedTable,namedPlaceNumbers} from '../../hosting/sites/named-place.mjs';
import {createFinePlaceLoader,placeSampleScores,finePixel} from '../../hosting/sites/fine-place.mjs';
import {AWFUL_MAXIMUM} from '../../hosting/sites/score-bands.mjs';

const {values}=parseArgs({options:{data:{type:'string'},named:{type:'string'},'climate-meta':{type:'string'},'fine-pointer':{type:'string'},places:{type:'string',default:'artifacts/places.tsv'},output:{type:'string'},baseline:{type:'string'},report:{type:'string'},'expected-root':{type:'string'},'verify-live':{type:'boolean',default:false}}});
for(const field of ['data','named','climate-meta','fine-pointer','output'])if(!values[field])throw Error(`Missing --${field}`);
const hash=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
const rankingModule=await bundle({entryPoints:['src/ui/ranking.ts'],bundle:true,write:false,platform:'node',format:'esm'});
const {rankPlaces,inOutlookRegion,WORLD_POPULATION}=await import('data:text/javascript;base64,'+Buffer.from(rankingModule.outputFiles[0].text).toString('base64'));
const kebab=value=>value.normalize('NFD').replace(/[^\x00-\x7f]/g,'').toLowerCase().replace(/[^a-z0-9]+/g,'-').replace(/^-|-$/g,'');
const sources={};
async function read(file){const bytes=await fs.readFile(file);sources[file]={bytes:bytes.length,sha256:hash(bytes)};return bytes;}
const metadata=JSON.parse(await read(values['climate-meta']));
const build=JSON.parse(await read(path.join(values.named,'BUILD.json')));
const expectedNamedRoot=values['expected-root']??path.basename(values.named);
let liveNamedConfiguration;
if(values['verify-live']){
  const response=await fetch('https://nice.jjpcodes.com/api/named');if(!response.ok)throw Error(`Live named configuration: HTTP ${response.status}`);
  liveNamedConfiguration=await response.json();
  if(!liveNamedConfiguration.enabled||liveNamedConfiguration.configuration.generation!==build.generation||liveNamedConfiguration.configuration.root!==expectedNamedRoot)throw Error(`Local compact build is not the active live named configuration: expected generation ${build.generation}, root ${expectedNamedRoot}`);
}
const compressed=await read(path.join(values.named,'climate.bin.gz'));
if(hash(compressed)!==build.files['climate.bin.gz'].sha256)throw Error('Named table differs from its build inventory');
const table=decodeNamedTable(new Uint8Array(zlib.gunzipSync(compressed)));
const catalogue=(await read(values.places)).toString('utf8').trim().split('\n').map(line=>{
  const [name,english,region,latitude,longitude,population]=line.split('\t');
  return {name,english,region,slug:`${kebab(region.split(',').at(-1))}/${kebab(english)}`,latitude:Number(latitude),longitude:Number(longitude),population:Number(population)};
}).sort((a,b)=>b.population-a.population);
const indexByCoordinate=new Map(Array.from({length:table.count},(_,index)=>[`${table.view.getInt32(32+index*8,true)},${table.view.getInt32(36+index*8,true)}`,index]));
const coordinateKey=(latitude,longitude)=>`${Math.round(latitude*1e5)},${Math.round(longitude*1e5)}`;
const periods={year:{kind:'year'},novemberMarch:{kind:'months',startMonth:11,endMonth:3}};
function numbers(climate,index,period){
  const value=namedPlaceNumbers({climate,climateIndex:index},period);
  if(!value)return null;
  const {score,niceDayCount,perfectDayCount,awfulDayCount,totalDays,dayScores,dayWeights}=value;
  return {score,niceDayCount,perfectDayCount,awfulDayCount,totalDays,dayScores,dayWeights};
}
function placeEvidence(place,climate=table,index=indexByCoordinate.get(coordinateKey(place.latitude,place.longitude))){
  if(index===undefined)throw Error(`Coordinate absent from named table: ${place.english}`);
  return {...place,periods:Object.fromEntries(Object.entries(periods).map(([name,period])=>[name,numbers(climate,index,period)])),months:Array.from({length:12},(_,month)=>numbers(climate,index,{kind:'month',month:month+1}))};
}
const requested=['San Diego','Los Angeles','San Francisco','Cape Town','Malaga','Seville','Lisbon','Cairo','Austin','Rotterdam','Singapore','Arona'].map(name=>{
  const place=catalogue.find(row=>row.english===name);if(!place)throw Error(`Missing canonical ${name}`);return placeEvidence(place);
});
// This requested coordinate is absent from the public named catalogue. Use the
// same accepted fine model and compact score packing, without naming it Arona.
const finePointer=await read(values['fine-pointer']);
const fineInputSources={};
const loader=createFinePlaceLoader(async key=>{
  if(key==='v6/fine/current.json')return finePointer;
  try{const bytes=await fs.readFile(path.join(values.data,key));fineInputSources[key]={bytes:bytes.length,sha256:hash(bytes)};return bytes;}
  catch(error){if(error.code==='ENOENT')return null;throw error;}
});
const exact={name:'Tenerife south coast (exact point)',english:'Tenerife south coast (exact point)',latitude:28.12,longitude:-16.73,region:'Canary Islands, Spain'};
const packet=await loader.load(exact.latitude,exact.longitude);
if(!packet)throw Error('Exact Tenerife point lacks accepted fine weather');
const scores=placeSampleScores(packet).flat().map(Math.floor);
if(scores.length!==96)throw Error('Expected eight fine sample days for each month');
const pointBytes=new Uint8Array(32+104),pointView=new DataView(pointBytes.buffer);
pointBytes.set(new TextEncoder().encode('NWNP'));pointView.setUint16(4,1,true);pointBytes[6]=1;pointBytes[7]=96;pointView.setUint32(8,1,true);
pointView.setInt32(32,Math.round(exact.latitude*1e5),true);pointView.setInt32(36,Math.round(exact.longitude*1e5),true);pointBytes.set(scores,40);
requested.unshift({...placeEvidence(exact,decodeNamedTable(pointBytes),0),pixel:finePixel(exact.latitude,exact.longitude),publicNamedCoordinate:false});
const ranking=Object.fromEntries(Object.keys(periods).map(name=>[name,[]]));
const seen=new Set();let supported=0,unsupported=0;
for(const place of catalogue){
  const key=coordinateKey(place.latitude,place.longitude);if(seen.has(key))continue;seen.add(key);
  const index=indexByCoordinate.get(key);if(index===undefined){unsupported++;continue;}
  const results=Object.fromEntries(Object.entries(periods).map(([name,period])=>[name,numbers(table,index,period)]));
  if(Object.values(results).some(value=>value===null)){unsupported++;continue;}supported++;
  for(const [name,value]of Object.entries(results)){
    const {dayScores,dayWeights,...compact}=value;
    ranking[name].push({...place,...compact});
  }
}
for(const rows of Object.values(ranking))rows.sort((a,b)=>b.niceDayCount-a.niceDayCount||b.score-a.score||b.perfectDayCount-a.perfectDayCount||b.population-a.population||a.english.localeCompare(b.english));
const worldwideHeadline=Object.fromEntries(Object.entries(ranking).map(([name,rows])=>[name,rankPlaces(rows.filter(row=>row.niceDayCount>0&&inOutlookRegion(row,'the world')),WORLD_POPULATION,3)]));
const report={writtenBy:'AI on 2026.10.19 by model GPT-6.1 Sol',capturedAt:new Date().toISOString(),metadata,namedGeneration:build.generation,namedPlaces:table.count,sourceHashes:sources,fineInputSources,definition:`Default taste from the production compact decoder. Nice means daily score >=55, perfect >=85, awful <${AWFUL_MAXIMUM}. Each of eight sampled days per month carries calendar month length / 8; counts round once over the complete period. Year=365 days; November through March=151 days. Ranking covers distinct named catalogue coordinates, sorted by rounded nice-day count, then typical score, perfect-day count, population and name. No population cutoff or geographic deduplication.`,headlineDefinition:'Separately, worldwideHeadline uses the production rankPlaces and inOutlookRegion functions: nice plus perfect days (perfect counts twice), population at least 300000, 250 km separation and its nearby-city naming rule. A cropped map or a different zoom can show different winners.',requested,rankingCoverage:{catalogueRows:catalogue.length,distinctCoordinates:seen.size,supported,unsupported},topNamed:Object.fromEntries(Object.entries(ranking).map(([name,rows])=>[name,rows.slice(0,20)])),worldwideHeadline,scoringSourceHashes:{}};
if(liveNamedConfiguration){report.liveNamedConfiguration=liveNamedConfiguration;report.expectedNamedRoot=expectedNamedRoot;}
for(const file of ['hosting/sites/named-place.mjs','hosting/sites/day-counts.mjs','hosting/sites/score-bands.mjs','hosting/sites/awful-maximum.json','hosting/sites/fine-place.mjs','hosting/sites/fine-replay.mjs','src/ui/ranking.ts','src/ui/countries.ts'])report.scoringSourceHashes[file]=hash(await fs.readFile(file));
await fs.writeFile(values.output,JSON.stringify(report,null,2)+'\n');
const baseline=values.baseline?JSON.parse(await fs.readFile(values.baseline)):null;
const lines=['Written by AI on 2026.10.19 by model GPT-6.1 Sol.','',baseline?'# Five versus twenty-five years: default nice days':'# Five-year baseline: default nice days','',report.definition,'','| Place | Year'+(baseline?' 5y | Year 25y | Change | Nov–Mar 5y | Nov–Mar 25y | Change':' | Nov–Mar')+' |','| --- |'+(baseline?' ---: | ---: | ---: | ---: | ---: | ---:':' ---: | ---:')+' |'];
for(const place of requested){
  const current=place.periods,before=baseline?.requested.find(row=>row.english===place.english)?.periods;
  const delta=value=>value>0?`+${value}`:String(value);
  lines.push(before?`| ${place.name} | ${before.year.niceDayCount} | ${current.year.niceDayCount} | ${delta(current.year.niceDayCount-before.year.niceDayCount)} | ${before.novemberMarch.niceDayCount} | ${current.novemberMarch.niceDayCount} | ${delta(current.novemberMarch.niceDayCount-before.novemberMarch.niceDayCount)} |`:`| ${place.name} | ${current.year.niceDayCount} | ${current.novemberMarch.niceDayCount} |`);
}
lines.push('','The exact Tenerife point is 28.12, −16.73. Arona is the catalogue city at 28.09962, −16.68102. They are different 1 km pixels. The exact point uses the same accepted fine model locally; it is not silently replaced by the named Arona row. Public named cards resolve to their canonical catalogue coordinate.','');
lines.push('## Worldwide headline checks','',report.headlineDefinition,'','| Period | Place | Nice days | Perfect days |','| --- | --- | ---: | ---: |');
for(const [period,places]of Object.entries(worldwideHeadline))for(const place of places)lines.push(`| ${period==='year'?'Year':'Nov–Mar'} | ${place.name} | ${place.niceDayCount} / ${place.totalDays} | ${place.perfectDayCount} |`);
lines.push('');
for(const [name,rows]of Object.entries(report.topNamed)){
  lines.push(`## Top named places: ${name==='year'?'year':'November–March'}`,'','| Rank | Place | Region | Nice days | Perfect days | Typical score |','| ---: | --- | --- | ---: | ---: | ---: |');
  rows.forEach((row,index)=>lines.push(`| ${index+1} | ${row.name} | ${row.region} | ${row.niceDayCount} / ${row.totalDays} | ${row.perfectDayCount} | ${Math.round(row.score)} |`));lines.push('');
}
if(baseline&&JSON.stringify(report.scoringSourceHashes)!==JSON.stringify(baseline.scoringSourceHashes))lines.push('Scoring source files changed between captures; inspect the recorded source hashes before attributing every change solely to the number of years.','');
const markdown=lines.join('\n')+'\n';
if(values.report)await fs.writeFile(values.report,markdown);
console.log(markdown);
