// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Check actual published-format daily tables and the card's whole-day counts.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {execFileSync} from 'node:child_process';
import {pathToFileURL} from 'node:url';
import {createNamedPlaceLoader,namedPlaceNumbers,namedPeriodMonths} from '../../../hosting/sites/named-place.mjs';
import {blendPlaceNumbers} from '../../../hosting/sites/fine-place.mjs';
const [data,output]=process.argv.slice(2);
if(!output)throw Error('Usage: check.mjs DATA_ROOT OUTPUT_JSON');
const configuration=JSON.parse(await fs.readFile(path.join(data,'fine-named-upload-b1-20261018-activation/v6/named/current.json')));
const read=async key=> {
  if(key==='v6/named/current.json')return new TextEncoder().encode(JSON.stringify(configuration));
  for(const root of [path.join(data,'fine-named-upload-b1-20261018'),path.resolve('artifacts')])try{return new Uint8Array(await fs.readFile(path.join(root,key)));}catch(error){if(error.code!=='ENOENT')throw error;}
  return null;
};
const loader=createNamedPlaceLoader(read);
// The preceding committed function gives a direct check that scores and bands did not move.
let oldSource=execFileSync('git',['show','a4c8a54^:hosting/sites/named-place.mjs'],{encoding:'utf8'});
oldSource=oldSource.replaceAll("'./fine-place.mjs'",JSON.stringify(pathToFileURL(path.resolve('hosting/sites/fine-place.mjs')).href));
const old=await import('data:text/javascript;base64,'+Buffer.from(oldSource).toString('base64'));
const queries=['Chania','Santa Cruz de Tenerife','Arona','Málaga','Cape Town','San Diego','Rotterdam','Sydney','Bangkok','London','New York','San Francisco','Lisbon','Agadir','Funchal','Santiago','Valparaíso','Buenos Aires','Rio de Janeiro','Melbourne','Perth','Tokyo','Singapore','Hong Kong','Taipei','Auckland','Honolulu','Los Angeles','Athens','Karachi'];
const periods=[{kind:'months',startMonth:11,endMonth:3},{kind:'year'},{kind:'month',month:7},{kind:'forecast'}];
const cases=[];
for(const name of queries) {
  const places=await(await fetch('http://127.0.0.1:5198/api/search?q='+encodeURIComponent(name))).json();
  const place=places.find(place=>place.name.toLowerCase()===name.toLowerCase())??places[0];
  assert(place,`No place for ${name}`);
  for(const period of periods) {
    const row=await loader.load(place.latitude,place.longitude,period);assert(row,`${name}: no row`);
    const numbers=namedPlaceNumbers(row,period);assert(numbers,`${name}: no numbers`);
    const preceding=old.namedPlaceNumbers(row,period);
    assert.equal(numbers.meanScore,preceding.meanScore,`${name}: changed score`);
    assert.equal(numbers.band,preceding.band,`${name}: changed band`);
    const daily=period.kind==='forecast'?Array.from({length:14},(_,day)=>namedPlaceNumbers(row,{kind:'forecast-day',day}).meanScore):numbers.dayScores;
    assert.deepEqual(numbers.dayScores,daily,`${name}: aggregate/daily sources differ`);
    const monthDays=[31,28,31,30,31,30,31,31,30,31,30,31];
    const weights=period.kind==='forecast'?daily.map(()=>1):namedPeriodMonths(period).flatMap(month=>Array(8).fill(monthDays[month-1]/8));
    assert.equal(numbers.countedDays,Math.round(weights.reduce((sum,weight)=>sum+weight,0)));
    for(const [field,predicate]of [['niceDayCount',score=>score>=55],['perfectDayCount',score=>score>=85],['awfulDayCount',score=>score<25]]) {
      assert(Number.isInteger(numbers[field]));
      assert.equal(numbers[field],Math.round(daily.reduce((sum,score,index)=>sum+(predicate(score)?weights[index]:0),0)),`${name}/${field}`);
    }
    cases.push({name:place.name,latitude:place.latitude,longitude:place.longitude,period,score:numbers.meanScore,band:numbers.band,nice:numbers.niceDayCount,perfect:numbers.perfectDayCount,awful:numbers.awfulDayCount,countedDays:numbers.countedDays,countBasis:numbers.countBasis,daily});
  }
}
// An explicit interpolation case protects the actual issue: 0.5 of two area counts is not a day count.
const base={meanScore:70,totalDays:2,sampleDays:2,niceDayShare:.5,perfectDayShare:.5,awfulDayShare:0,countBasis:'forecast'};
const blended=blendPlaceNumbers([{numbers:{...base,dayScores:[54,86]},weight:.5},{numbers:{...base,dayScores:[58,82]},weight:.5}]);
assert.deepEqual(blended.dayScores,[56,84]);assert.equal(blended.niceDayCount,2);assert.equal(blended.perfectDayCount,0);
await fs.writeFile(output,JSON.stringify({written_by:'AI, GPT-6.1 Sol, 2026.10.18',passes:true,places:queries.length,comparisons:cases.length,scoresAndBandsUnchanged:true,cases},null,2)+'\n');
console.log(JSON.stringify({passes:true,places:queries.length,comparisons:cases.length,chania:cases.find(entry=>entry.name==='Chania'&&entry.period.kind==='forecast'),tenerife:cases.find(entry=>entry.name==='Santa Cruz de Tenerife'&&entry.period.kind==='forecast')}));
