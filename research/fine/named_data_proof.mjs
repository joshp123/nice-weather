// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import {pathToFileURL} from 'node:url';
import {createFinePlaceLoader,placeNumbers,placeMonths} from '../../hosting/sites/fine-place.mjs';
import {createNamedPlaceLoader,namedPlaceNumbers,namedPlaceMonths} from '../../hosting/sites/named-place.mjs';
const [data,build,queriesPath,output]=process.argv.slice(2);
if(!output)throw Error('Usage named_data_proof.mjs data private397build queries output');
const queries=JSON.parse(await fs.readFile(queriesPath)),finePointer=await fs.readFile(path.join(data,'fine-validation/b1-worldwide/overlay/v6/fine/current.json'));
const read=async filename=>{try{return new Uint8Array(await fs.readFile(filename));}catch(error){if(error.code==='ENOENT')return null;throw error;}};
const fine=createFinePlaceLoader(key=>key==='v6/fine/current.json'?Promise.resolve(new Uint8Array(finePointer)):read(path.join(data,key)));
const pointer={version:1,generation:'validation-only',root:'named-proof',climate:'climate.bin.gz',factsRows:16384,defaultTasteOnly:true};
const current=JSON.parse(await fs.readFile('artifacts/v6/forecast/current.json'));
const named=createNamedPlaceLoader(key=>{
 if(key==='v6/named/current.json')return Promise.resolve(new TextEncoder().encode(JSON.stringify(pointer)));
 if(key.startsWith('named-proof/'))return read(path.join(build,key.slice('named-proof/'.length)));
 if(key===`v6/forecast/${current.run}/named.bin.gz`)return read(path.join(build,'forecast',key));
 return read(path.join('artifacts',key));
});
let source=await fs.readFile('hosting/sites/worker.mjs','utf8');source=source.replace(/from '\.\/([^']+)'/g,(_,name)=>`from '${pathToFileURL(path.resolve('hosting/sites',name)).href}'`);source+='\nexport {defaultPeriodJudgement as proofJudgement};\n';
const {proofJudgement}=await import('data:text/javascript;base64,'+Buffer.from(source).toString('base64'));
const cache=new Map(),bucket={async get(key,options){if(key==='v6/fine/current.json'||key==='v6/named/current.json')return null;if(!cache.has(key))cache.set(key,await read(path.join('artifacts',key)));const all=cache.get(key);if(!all)return null;const bytes=options?.range?all.subarray(options.range.offset,options.range.offset+options.range.length):all;return{arrayBuffer:async()=>bytes.buffer.slice(bytes.byteOffset,bytes.byteOffset+bytes.byteLength),json:async()=>JSON.parse(new TextDecoder().decode(bytes)),text:async()=>new TextDecoder().decode(bytes)};}};
const periods=[...Array.from({length:12},(_,i)=>({months:[i+1]})),{months:[1,2,3,4,5,6,7,8,9,10,11,12]},...[[12,1,2],[3,4,5],[6,7,8],[9,10,11]].map(months=>({months}))];
const visibleFactDifferences=[];
let climateComparisons=0,forecastComparisons=0,physicalComparisons=0,visibleComparisons=0,maxFacts=0,maxForecastCount=0,unsupportedForecast=0;
const started=performance.now();
function visibleNumbers(actual,expected,label){
 const decimal={rainMmPerDay:1,dewPointC:1};
 for(const field of Object.keys(expected.typical)){const a=actual.typical[field],b=expected.typical[field];maxFacts=Math.max(maxFacts,Math.abs(a-b));assert(Math.abs(a-b)<1e-4,label+'/'+field);let left,right;if(decimal[field]!==undefined){left=a.toFixed(decimal[field]);right=b.toFixed(decimal[field]);}else if(['wetDayShare','sunnyShare'].includes(field)){left=Math.round(a*100);right=Math.round(b*100);}else{left=Math.round(a);right=Math.round(b);}if(left!==right)visibleFactDifferences.push({label,field,actual:a,expected:b,displayedActual:left,displayedExpected:right});visibleComparisons++;}
}
for(const query of queries){
 const packet=await fine.load(query.lat,query.lon);assert(packet);const row=await named.load(query.lat,query.lon,{kind:'climate',months:[1]});assert(row);
 for(const period of periods){const expected=placeNumbers(packet,period),actual=namedPlaceNumbers(row,period);assert(actual);for(const field of['score','niceDayCount','perfectDayCount','awfulDayCount']){assert.equal(actual[field],expected[field],`${query.slug}/climate/${period.months}/${field}`);climateComparisons++;}visibleNumbers(actual,expected,`${query.slug}/${period.months}`);}
 const expectedMonths=placeMonths(packet),actualMonths=namedPlaceMonths(row);assert(actualMonths);
 for(let month=0;month<12;month++)for(const field of['highC','feelsAt21C','dewPointC','wetDaysPercent','sunnyPercent','windKmh']){const a=actualMonths[month][field],b=expectedMonths[month][field];maxFacts=Math.max(maxFacts,Math.abs(a-b));assert(Math.abs(a-b)<1e-4,`${query.slug}/month/${field}`);if(Math.round(a)!==Math.round(b))visibleFactDifferences.push({label:`${query.slug}/month/${month+1}`,field,actual:a,expected:b,displayedActual:Math.round(a),displayedExpected:Math.round(b)});physicalComparisons++;}
 for(const period of[...Array.from({length:14},(_,day)=>({kind:'forecast',day})),{kind:'forecast'}]){
  const expected=await proofJudgement(bucket,query.lat,query.lon,period),row=await named.load(query.lat,query.lon,period),actual=row&&namedPlaceNumbers(row,period);
  assert.equal(Boolean(actual),Boolean(expected),`${query.slug}/forecast availability`);if(!actual){unsupportedForecast++;continue;}
  assert.equal(actual.score,expected.score,`${query.slug}/forecast/${period.day??'all'}/score`);forecastComparisons++;
  for(const field of['niceDayCount','perfectDayCount','awfulDayCount']){const difference=Math.abs(actual[field]-expected[field]);maxForecastCount=Math.max(maxForecastCount,difference);assert(difference<1e-6,`${query.slug}/forecast/${field}: ${difference}`);assert.equal(Math.round(actual[field]),Math.round(expected[field]),`${query.slug}/forecast/${period.day??'all'}/${field} visible`);forecastComparisons++;}
 }
}
const sourceSha256=Object.fromEntries(await Promise.all(['hosting/sites/fine-place.mjs','hosting/sites/named-place.mjs','hosting/sites/worker.mjs','internal/placenumbers/numbers.go','outlook/named_forecast.go','research/fine/named_data_proof.mjs'].map(async name=>[name,crypto.createHash('sha256').update(await fs.readFile(name)).digest('hex')])));
const tableFiles=[...(await fs.readdir(build)).filter(name=>name==='climate.bin.gz'||name.startsWith('facts-')),`forecast/v6/forecast/${current.run}/named.bin.gz`];
const tableSha256=Object.fromEntries(await Promise.all(tableFiles.map(async name=>[name,crypto.createHash('sha256').update(await fs.readFile(path.join(build,name))).digest('hex')])));
const report={sourceSha256,tableSha256,written_by:'AI, GPT-6.1 Sol, 2026.10.18',passes:true,places:queries.length,climateComparisons,forecastComparisons,physicalComparisons,visibleComparisons,maximumClimateScoreOrCountDifference:0,maximumForecastScoreDifference:0,maximumFactsDifference:maxFacts,maximumForecastCountDifference:maxForecastCount,visibleFactDifferences,unsupportedForecast,seconds:(performance.now()-started)/1000,definition:'Actual compressed NWNP tables through shared production loader and aggregate. Climate scores/counts equal exact-pixel full replay for 17 periods. All physical facts differ by less than 0.0001. Rounded fact differences at numerical ties are enumerated. Forecast scores equal existing Worker and independently generated Go numbers for all 14 days and the total; float32 count error below 1e-6 with every rounded day count identical. Private random validation rows are not public named membership.'};
await fs.mkdir(path.dirname(output),{recursive:true});await fs.writeFile(output,JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify(report));
