// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Exercise the actual Worker against the compact shared function and legacy fallback.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import {localWorkerFixture} from './local_worker_fixture.mjs';
import {createNamedPlaceLoader,namedPlaceNumbers,namedPlaceMonths} from '../../hosting/sites/named-place.mjs';

const [overlay,queriesFile,output]=process.argv.slice(2);
if(!output)throw Error('Usage: named_worker_proof.mjs <active overlay> <queries.json> <report.json>');
await fs.mkdir(path.dirname(output),{recursive:true});
await fs.writeFile(output,JSON.stringify({passes:false,status:'running'},null,2)+'\n');
const queries=JSON.parse(await fs.readFile(queriesFile,'utf8'));
const configuration=JSON.parse(await fs.readFile(path.join(overlay,'v6/named/current.json'),'utf8'));
const storage=await fs.mkdtemp(path.join(os.tmpdir(),'nwf-named-worker-'));
for(const entry of await fs.readdir(overlay,{withFileTypes:true})) {
  if(entry.name==='v6')continue;
  await fs.symlink(path.resolve(overlay,entry.name),path.join(storage,entry.name),entry.isDirectory()?'dir':'file');
}
await fs.mkdir(path.join(storage,'v6/named'),{recursive:true});
for(const entry of await fs.readdir(path.join(overlay,'v6'),{withFileTypes:true})) {
  if(entry.name==='named'||entry.name==='fine')continue;
  await fs.symlink(path.resolve(overlay,'v6',entry.name),path.join(storage,'v6',entry.name),entry.isDirectory()?'dir':'file');
}
for(const entry of await fs.readdir(path.join(overlay,'v6/named'),{withFileTypes:true})) {
  if(entry.name==='current.json')continue;
  await fs.symlink(path.resolve(overlay,'v6/named',entry.name),path.join(storage,'v6/named',entry.name),entry.isDirectory()?'dir':'file');
}
const pointer=path.join(storage,'v6/named/current.json');
await fs.writeFile(pointer,JSON.stringify(configuration));
const read=async key=>{for(const root of [storage,path.resolve('artifacts')])try{return new Uint8Array(await fs.readFile(path.join(root,key)));}catch(error){if(error.code!=='ENOENT')throw error;}return null;};
const loader=createNamedPlaceLoader(read),request=localWorkerFixture(storage,()=>null);
const periods=[['july',{kind:'climate',months:[7]}],['dec-feb',{kind:'climate',months:[12,1,2]}],['year',{kind:'climate',months:[1,2,3,4,5,6,7,8,9,10,11,12]}],['next-14-days',{kind:'forecast'}],['day-3',{kind:'forecast',day:3}]];
const numeric=['score','niceDayCount','perfectDayCount','awfulDayCount','totalDays'];
const cases=[],fallback=[];
let elapsed=0;const originalNow=Date.now,base=originalNow();Date.now=()=>base+elapsed;
try {
  assert.equal((await(await request('https://nice.jjpcodes.com/api/named')).json()).enabled,true);
  const clickedLatitude=37.7751,clickedLongitude=-122.4181;
  assert.equal(await loader.load(clickedLatitude,clickedLongitude),null,'Compact membership must remain exact');
  assert.equal(await(await request(`https://nice.jjpcodes.com/api/named/place?lat=${clickedLatitude}&lon=${clickedLongitude}&kind=climate`)).json(),null);
  const clickedCard=await(await request(`https://nice.jjpcodes.com/api/place?lat=${clickedLatitude}&lon=${clickedLongitude}&period=july`)).json();
  const canonicalCard=await(await request('https://nice.jjpcodes.com/api/place?lat=37.77493&lon=-122.41942&period=july')).json();
  assert.equal(clickedCard.name,'San Francisco');assert.equal(clickedCard.latitude,37.77493);assert.equal(clickedCard.longitude,-122.41942);
  assert.deepEqual(clickedCard.months,canonicalCard.months);assert.deepEqual(clickedCard.judgement,canonicalCard.judgement);
  const coordinateSearch=await(await request(`https://nice.jjpcodes.com/api/search?q=${clickedLatitude},${clickedLongitude}`)).json();
  assert.equal(coordinateSearch[0].latitude,37.77493);assert.equal(coordinateSearch[0].longitude,-122.41942);
  const exactTown=await(await request('https://nice.jjpcodes.com/api/search?q=33.98162,-118.45371')).json();
  assert.equal(exactTown[0].name,'Marina del Rey');assert.equal(exactTown[0].latitude,33.98162);assert.equal(exactTown[0].longitude,-118.45371);
  const clickedHtml=await(await request(`https://nice.jjpcodes.com/@${clickedLatitude},${clickedLongitude}/july`)).text();
  assert.ok(clickedHtml.includes('San Francisco'));
  const canonicalHtml=await(await request('https://nice.jjpcodes.com/@37.77493,-122.41942/july')).text();
  const imageUrl=html=>html.match(/property="og:image" content="([^"]+)"/)?.[1].replaceAll('&amp;','&');
  assert.deepEqual(new Uint8Array(await(await request(imageUrl(clickedHtml))).arrayBuffer()),new Uint8Array(await(await request(imageUrl(canonicalHtml))).arrayBuffer()),'Clicked and canonical San Francisco preview images differ');
  const unnamedLatitude=-25,unnamedLongitude=135;
  assert.equal(await loader.load(unnamedLatitude,unnamedLongitude),null,'Nearby unnamed coordinate acquired a named score');
  assert.equal(await(await request(`https://nice.jjpcodes.com/api/named/place?lat=${unnamedLatitude}&lon=${unnamedLongitude}&kind=climate`)).json(),null);
  const unnamedCard=await(await request(`https://nice.jjpcodes.com/api/place?lat=${unnamedLatitude}&lon=${unnamedLongitude}&period=july`)).json();
  assert.ok(unnamedCard.months);assert.equal(Boolean(unnamedCard.namedDefault),false);
  assert.equal(unnamedCard.latitude,unnamedLatitude);assert.equal(unnamedCard.longitude,unnamedLongitude);
  fallback.push({lat:unnamedLatitude,lon:unnamedLongitude,word:'july',months:unnamedCard.months});
  for(const query of queries) {
    const lat=query.lat??query.latitude,lon=query.lon??query.longitude;
    for(const [word,period]of periods) {
      const response=await request(`https://nice.jjpcodes.com/api/place?lat=${lat}&lon=${lon}&period=${word}`);
      assert.equal(response.status,200);const actual=await response.json();
      const row=await loader.load(actual.latitude,actual.longitude,period),expected=row&&namedPlaceNumbers(row,period);
      if(expected) {
        assert.equal(actual.namedDefault,true);
        for(const field of numeric)assert.equal(actual.judgement[field],expected[field]===0?0:expected[field],`${query.name??lat}/${word}/${field}`);
        const climateRow=period.kind==='forecast'?await loader.load(actual.latitude,actual.longitude):row;
        if(climateRow)assert.deepEqual(actual.months,namedPlaceMonths(climateRow));
        else if(lat>=-60)assert.ok(actual.months,'Forecast-only fixture row lost its legacy climate card');
        if(word==='july') {
          const physical=await(await request(`https://nice.jjpcodes.com/api/named/place?lat=${actual.latitude}&lon=${actual.longitude}&kind=climate`)).json();
          assert.deepEqual(physical,{months:row.months});
        }
        cases.push({name:query.name,lat,lon,word,score:actual.judgement.score});
      } else {
        assert.equal(Boolean(actual.namedDefault),false);
        if(lat>=-60)assert.ok(actual.months,`Legacy card absent: ${lat},${lon}`);
        if(!await loader.load(actual.latitude,actual.longitude))fallback.push({lat,lon,word,months:actual.months});
      }
    }
  }
  let named;
  for(const query of queries)if(await loader.load(query.lat??query.latitude,query.lon??query.longitude)){named=query;break;}
  if(named)for(const word of ['july','next-14-days']) {
    const lat=named.lat??named.latitude,lon=named.lon??named.longitude;
    const html=await(await request(`https://nice.jjpcodes.com/@${lat},${lon}/${word}`)).text();
    const imageUrl=html.match(/property="og:image" content="([^"]+)"/)?.[1].replaceAll('&amp;','&');
    assert.ok(imageUrl?.includes('/preview/named.png'),`Named ${word} preview not dynamic`);
    const response=await request(imageUrl);assert.equal(response.status,200);
    const png=Buffer.from(await response.arrayBuffer());let metadata;
    for(let offset=8;offset<png.length;) {const length=png.readUInt32BE(offset);if(png.toString('ascii',offset+4,offset+8)==='tEXt')metadata=JSON.parse(png.toString('utf8',offset+8,offset+8+length).split('\0')[1]);offset+=length+12;}
    const api=await(await request(`https://nice.jjpcodes.com/api/place?lat=${lat}&lon=${lon}&period=${word}`)).json();
    for(const field of numeric)assert.equal(metadata[field],api.judgement[field]);
    await fs.mkdir(path.dirname(output),{recursive:true});await fs.writeFile(path.join(path.dirname(output),`named-${word}.png`),png);
  }
  const customPath=`/@${queries[0].lat??queries[0].latitude},${queries[0].lon??queries[0].longitude}/july?perfectFrom=24`;
  const customHtml=await(await request(`https://nice.jjpcodes.com${customPath}`)).text();
  assert.ok(customHtml.includes('with your own taste'));
  assert.ok(!customHtml.includes('property="og:image"'));
  assert.ok(customHtml.includes(`rel="canonical" href="https://nice.jjpcodes.com${customPath}"`));
  // Remove the pointer by moving it aside, then expire the same warm Worker's cache.
  await fs.rename(pointer,`${pointer}.inactive`);elapsed+=61000;
  assert.equal((await(await request('https://nice.jjpcodes.com/api/named')).json()).enabled,false);
  for(const query of queries.slice(0,13)) {
    const lat=query.lat??query.latitude,lon=query.lon??query.longitude;
    const actual=await(await request(`https://nice.jjpcodes.com/api/place?lat=${lat}&lon=${lon}&period=july`)).json();
    assert.equal(Boolean(actual.namedDefault),false);assert.ok(actual.months,`Inactive card absent: ${lat},${lon}`);
  }
  const inactiveClicked=await(await request(`https://nice.jjpcodes.com/api/place?lat=${clickedLatitude}&lon=${clickedLongitude}&period=july`)).json();
  const inactiveCanonical=await(await request('https://nice.jjpcodes.com/api/place?lat=37.77493&lon=-122.41942&period=july')).json();
  assert.deepEqual(inactiveClicked.months,inactiveCanonical.months);assert.equal(inactiveClicked.latitude,37.77493);assert.equal(inactiveClicked.longitude,-122.41942);
  for(const previous of fallback.slice(0,25)) {
    const actual=await(await request(`https://nice.jjpcodes.com/api/place?lat=${previous.lat}&lon=${previous.lon}&period=${previous.word}`)).json();
    assert.deepEqual(actual.months,previous.months,'Unnamed active and inactive 25 km facts differ');
  }
} finally {Date.now=originalNow;}
const sourceHashes=Object.fromEntries(await Promise.all(['hosting/sites/worker.mjs','hosting/sites/named-place.mjs','hosting/sites/fine-preview.mjs','research/fine/local_worker_fixture.mjs','research/fine/named_worker_proof.mjs'].map(async file=>[file,crypto.createHash('sha256').update(await fs.readFile(file)).digest('hex')])));
const report={written_by:'AI, GPT-6.1 Sol, 2026.10.18',passes:true,queries:queries.length,exactNamedComparisons:cases.length,legacyFallbackComparisons:fallback.length,namedPreviewClimateAndForecast:true,customTasteNeutralPreview:true,warmPointerRemoval:true,clickedNameUsesCanonicalCoordinates:true,compactMembershipRemainsExact:true,unnamedFallbackMatchesInactive:true,sourceHashes,cases};
await fs.mkdir(path.dirname(output),{recursive:true});await fs.writeFile(output,JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify({...report,cases:undefined}));
