// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Actual Worker cards and link-preview PNGs at the fourteen canonical city points.
import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';
import {parseArgs} from 'node:util';
import {localWorkerFixture} from '../fine/local_worker_fixture.mjs';
const {values}=parseArgs({options:{fixture:{type:'string'},comparison:{type:'string'},output:{type:'string'}}});
for(const key of ['fixture','comparison','output'])assert(values[key],`Missing --${key}`);
await fs.mkdir(values.output,{recursive:true});
const fixture=localWorkerFixture(values.fixture),comparison=JSON.parse(await fs.readFile(values.comparison)),cases=[];
for(const row of comparison.places){
  const place=row.place,appPath=`/${place.slug}/year`,html=await(await fixture(`https://nice.jjpcodes.com${appPath}`)).text();
  const imageUrl=html.match(/property="og:image" content="([^"]+)"/)?.[1]?.replaceAll('&amp;','&');assert(imageUrl,`Missing image ${place.slug}`);
  const description=html.match(/property="og:description" content="([^"]+)"/)?.[1];
  const response=await fixture(imageUrl);assert.equal(response.status,200);assert.equal(response.headers.get('content-type'),'image/png');
  const bytes=Buffer.from(await response.arrayBuffer());let metadata;
  for(let offset=8;offset<bytes.length;){const length=bytes.readUInt32BE(offset),type=bytes.toString('ascii',offset+4,offset+8);if(type==='tEXt')metadata=JSON.parse(bytes.toString('utf8',offset+8,offset+8+length).split('\0')[1]);offset+=length+12;}
  assert(metadata,`Missing PNG metadata ${place.slug}`);
  const api=await(await fixture(`https://nice.jjpcodes.com/api/place?lat=${place.latitude}&lon=${place.longitude}&period=year`)).json();assert.equal(api.namedDefault,true);
  const numbers=metadata.numbers;assert(numbers,`Missing shared PNG numbers ${place.slug}`);
  for(const key of ['score','niceDayCount','perfectDayCount','awfulDayCount','totalDays']){assert.equal(numbers[key],api.judgement[key],`${place.slug} PNG/API ${key}`);assert.equal(api.judgement[key],row.after.year[key],`${place.slug} API/release ${key}`);}
  assert(description.includes(`nice score ${Math.floor(numbers.score)} out of 100`));assert(description.includes(`Nice or better on ${numbers.niceDayCount} of ${numbers.countedDays??numbers.totalDays} days, perfect on ${numbers.perfectDayCount}, awful on ${numbers.awfulDayCount}.`));
  assert.equal(bytes.readUInt32BE(16),1200);assert.equal(bytes.readUInt32BE(20),630);
  await fs.writeFile(path.join(values.output,place.slug.replace('/','-')+'.png'),bytes);cases.push({place:place.slug,appPath,imageUrl,bytes:bytes.length,description,metadata,passes:true});
}
await fs.writeFile(path.join(values.output,'proof.json'),JSON.stringify({writtenBy:'AI, GPT-6.1 Sol, 2026-10-18',passes:true,scope:'Actual Worker HTML description, named-default API, generated preview PNG data and exact shared-release numbers agree at all fourteen named points.',cases},null,2)+'\n');
console.log(JSON.stringify({passes:true,places:cases.length,totalPngBytes:cases.reduce((sum,row)=>sum+row.bytes,0)}));
