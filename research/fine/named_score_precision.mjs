// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import {createFinePlaceLoader,placeNumbers,placeSampleScores} from '../../hosting/sites/fine-place.mjs';
const [data,queryPath,pointerPath,output]=process.argv.slice(2);
if(!output)throw Error('Usage: node named_score_precision.mjs data queries pointer output');
const pointer=await fs.readFile(pointerPath),queries=JSON.parse(await fs.readFile(queryPath));
const loader=createFinePlaceLoader(async key=>{if(key==='v6/fine/current.json')return new Uint8Array(pointer);try{return new Uint8Array(await fs.readFile(path.join(data,key)));}catch(e){if(e.code==='ENOENT')return null;throw e;}});
const periods=[...Array.from({length:12},(_,i)=>[i+1]),[1,2,3,4,5,6,7,8,9,10,11,12],[12,1,2],[3,4,5],[6,7,8],[9,10,11]],days=[31,28,31,30,31,30,31,31,30,31,30,31],thresholds=[0,25,40,55,70,85,100];
let comparisons=0,largestDailyFractionLost=0;
const started=performance.now();
for(const query of queries){
 const packet=await loader.load(query.lat,query.lon);assert(packet,`Missing ${query.slug}`);
 const raw=placeSampleScores(packet),scores=raw.map(values=>values.map(value=>{largestDailyFractionLost=Math.max(largestDailyFractionLost,value-Math.floor(value));return Math.floor(value);}));
 for(const months of periods){
  const expected=placeNumbers(packet,{months}),totalDays=months.reduce((sum,m)=>sum+days[m-1],0),shares=thresholds.map(()=>0);
  for(const month of months){const values=scores[month-1],weight=days[month-1]/totalDays/values.length;for(const score of values)for(let t=0;t<thresholds.length;t++)if(score>=thresholds[t])shares[t]+=weight;}
  let score=0;for(let band=5;band>=0;band--)if(shares[band]>=.5-1e-9){score=Math.round((band===5?Math.max(85,85+(shares[5]-.5)*30):Math.max(thresholds[band],thresholds[band]+(thresholds[band+1]-thresholds[band])*(shares[band]-.5)/(shares[band]-shares[band+1])))*10000)/10000;break;}
  const count=share=>Math.round(share*totalDays*1e9)/1e9;
  for(const [field,value]of Object.entries({score,niceDayCount:count(shares[3]),perfectDayCount:count(shares[5]),awfulDayCount:count(Math.max(0,1-shares[1]))})){assert.equal(value,expected[field],`${query.slug}/${months}/${field}`);comparisons++;}
 }
}
const report={written_by:'AI, GPT-6.1 Sol, 2026.10.18',passes:true,places:queries.length,periods:periods.length,comparisons,maximumScoreOrCountDifference:0,largestDailyFractionDiscarded:largestDailyFractionLost,definition:'Byte floors retain every integer band threshold of every representative climate day. Reconstructed weighted median band score and all three day counts equal unchanged full-replay placeNumbers exactly across twelve months, four seasons and year.',sha256:{queries:crypto.createHash('sha256').update(await fs.readFile(queryPath)).digest('hex'),pointer:crypto.createHash('sha256').update(pointer).digest('hex')},seconds:(performance.now()-started)/1000};
await fs.mkdir(path.dirname(output),{recursive:true});await fs.writeFile(output,JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify(report));
