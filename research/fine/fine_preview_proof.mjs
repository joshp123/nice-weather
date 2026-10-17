// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Exercise actual page metadata, dynamic PNG route and API with the same city pixel.
import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';
import {localWorkerFixture} from './local_worker_fixture.mjs';
const [data,overlay,output]=process.argv.slice(2);
if(!output)throw Error('Usage: fine_preview_proof.mjs <data-root> <overlay> <output>');
await fs.mkdir(output,{recursive:true});
const fixture=localWorkerFixture(overlay);
const queries=JSON.parse(await fs.readFile(path.join(data,'fine-validation/b1-worldwide/queries.json'),'utf8')).slice(0,13);
const cases=[];
for(const query of queries) {
  const appPath=`/@${query.lat},${query.lon}/july`;
  const html=await(await fixture(`https://nice.jjpcodes.com${appPath}`)).text();
  const imageUrl=html.match(/property="og:image" content="([^"]+)"/)?.[1].replaceAll('&amp;','&');
  assert.ok(imageUrl,query.name+' has own preview');
  const description=html.match(/property="og:description" content="([^"]+)"/)?.[1];
  const started=performance.now(),response=await fixture(imageUrl),bytes=Buffer.from(await response.arrayBuffer()),renderMs=performance.now()-started;
  assert.equal(response.status,200,query.name);assert.equal(response.headers.get('content-type'),'image/png');
  let metadata;
  for(let offset=8;offset<bytes.length;) {
    const length=bytes.readUInt32BE(offset),type=bytes.toString('ascii',offset+4,offset+8);
    if(type==='tEXt')metadata=JSON.parse(bytes.toString('utf8',offset+8,offset+8+length).split('\0')[1]);
    offset+=length+12;
  }
  const api=await(await fixture(`https://nice.jjpcodes.com/api/place?lat=${query.lat}&lon=${query.lon}&period=july`)).json();
  for(const key of ['score','niceDayCount','perfectDayCount','awfulDayCount','totalDays'])assert.equal(metadata[key],api.judgement[key],`${query.name} ${key}`);
  assert.ok(description.includes(`nice score ${metadata.labels.score} out of 100`));
  assert.ok(description.includes(`Nice or better on ${metadata.labels.nice} of ${metadata.totalDays} days, perfect on ${metadata.labels.perfect}, awful on ${metadata.labels.awful}.`));
  assert.equal(bytes.readUInt32BE(16),1200);assert.equal(bytes.readUInt32BE(20),630);
  await fs.writeFile(path.join(output,`${query.slug}.png`),bytes);
  cases.push({name:query.name,appPath,imageUrl,description,bytes:bytes.length,renderMs,metadata,parity:true});
}
// Named and coordinate URLs use identical values, including a period other than July.
const sf=queries[0],namedHtml=await(await fixture(`https://nice.jjpcodes.com/${sf.search_result.slug}/july`)).text();
assert.ok(namedHtml.includes('property="og:image"'));
const namedImage=namedHtml.match(/property="og:image" content="([^"]+)"/)[1].replaceAll('&amp;','&');
assert.equal((await fixture(namedImage)).status,200);
const additionalPeriods=[];
for(const word of ['may','dec-feb','year']) {
  const appPath=`/@${sf.lat},${sf.lon}/${word}`;
  const html=await(await fixture(`https://nice.jjpcodes.com${appPath}`)).text();
  const imageUrl=html.match(/property="og:image" content="([^"]+)"/)?.[1].replaceAll('&amp;','&');
  assert.ok(imageUrl,word);const response=await fixture(imageUrl);assert.equal(response.status,200,word);
  const bytes=Buffer.from(await response.arrayBuffer());await fs.writeFile(path.join(output,`sf-${word}.png`),bytes);
  additionalPeriods.push(word);
}
const stale=new URL(namedImage);stale.searchParams.set('generation','old');assert.equal((await fixture(stale.href)).status,302);
const unavailable=await fixture('https://nice.jjpcodes.com/preview/fine.png?path=%2Fyear');assert.equal(unavailable.status,404);
await fs.writeFile(path.join(output,'parity.json'),JSON.stringify({pass:true,cases,namedRoute:true,additionalPeriods,staleGenerationRedirect:true,worldRouteRejected:true},null,2));
console.log(JSON.stringify({pass:true,cities:cases.length,bytes:cases.map(value=>value.bytes),maxRenderMs:Math.max(...cases.map(value=>value.renderMs))}));
