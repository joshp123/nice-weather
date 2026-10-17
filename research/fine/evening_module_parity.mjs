// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Compare the actual shared loader/model with an independent Python replay.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';
import {parseArgs} from 'node:util';
import {createFinePlaceLoader,placeMonths,placeNumbers} from '../../hosting/sites/fine-place.mjs';

const {values}=parseArgs({options:{bundle:{type:'string'},expected:{type:'string'},output:{type:'string'}}});
for(const name of ['bundle','expected','output'])if(!values[name])throw new Error(`Missing --${name}`);
const hash=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
const inputBytes=new Map();
async function readOriginal(key) {
  if(!inputBytes.has(key))inputBytes.set(key,await fs.readFile(path.join(values.bundle,key)));
  return inputBytes.get(key);
}
const pointerKey='v6/fine/current.json',originalPointer=JSON.parse(await readOriginal(pointerKey));
const manifestKey=`${originalPointer.nativeRoot}/manifest.json`,manifest=JSON.parse(await readOriginal(manifestKey));
const expectedBytes=await fs.readFile(values.expected),expected=JSON.parse(expectedBytes);
const fields=['highC','feelsAt21C','score','niceDayCount','perfectDayCount','awfulDayCount','sampleDays'];
const maximumDifference=Object.fromEntries(fields.map(field=>[field,0]));
const expectedByKey=new Map(expected.map(row=>[`${row.slug}/${row.month}/${row.model}`,row]));
assert.equal(expectedByKey.size,expected.length,'Expected rows must have unique addresses');
const rows=[],highs=new Map();
for(const model of ['ordered','uniform','native']) {
  const loader=createFinePlaceLoader(async key=> {
    if(key===pointerKey)return new TextEncoder().encode(JSON.stringify({...originalPointer,eveningModel:model}));
    if(key===manifestKey)return new TextEncoder().encode(JSON.stringify({...manifest,eveningModel:model}));
    return new Uint8Array(await readOriginal(key));
  });
  for(const query of manifest.queries) {
    const packet=await loader.load(query.lat,query.lon);
    assert(packet,`Unavailable delivered pixel: ${query.slug}`);
    assert.equal(packet.eveningModel,model,'The loader must carry the declared global policy');
    const months=placeMonths(packet);
    for(const facts of months) {
      const judgement=placeNumbers(packet,{kind:'month',month:facts.month});
      const actual={highC:facts.highC,feelsAt21C:facts.feelsAt21C,...Object.fromEntries(fields.slice(2).map(field=>[field,judgement[field]]))};
      const key=`${query.slug}/${facts.month}/${model}`,reference=expectedByKey.get(key);
      assert(reference,`Missing independent row: ${key}`);
      const differences={};
      for(const field of fields) {
        assert(Number.isFinite(actual[field])&&Number.isFinite(reference[field]),`Nonfinite ${key}/${field}`);
        const difference=Math.abs(actual[field]-reference[field]);
        maximumDifference[field]=Math.max(maximumDifference[field],difference);
        differences[field]=difference;
        assert(difference<=1e-5,`${key}/${field}: shared=${actual[field]}, independent=${reference[field]}`);
      }
      assert.equal(actual.sampleDays,reference.sampleDays,`Representative-day count differs: ${key}`);
      const highKey=`${query.slug}/${facts.month}`;
      if(highs.has(highKey))assert.equal(actual.highC,highs.get(highKey),`Evening choice changed a daytime high: ${key}`);
      else highs.set(highKey,actual.highC);
      rows.push({slug:query.slug,month:facts.month,model,...actual,differences});
    }
  }
}
assert.equal(rows.length,expected.length,'Every independent row must be checked');
const consumed=[];
for(const [key,bytes]of inputBytes) {
  const filename=path.join(values.bundle,key),fresh=await fs.readFile(filename);
  assert.equal(hash(fresh),hash(bytes),`Input changed during comparison: ${key}`);
  consumed.push({key,path:await fs.realpath(filename),bytes:bytes.length,sha256:hash(bytes)});
}
const report={written_by:'AI, GPT-6.1 Sol, 2026.10.18',passes:true,
  definition:'Actual createFinePlaceLoader → placeMonths/placeNumbers, original delivered replay and NWFT tile bytes. Only the in-memory pointer and native manifest eveningModel fields differ by model; no on-disk input is changed.',
  bundle:path.resolve(values.bundle),generation:manifest.generation,
  source_sha256:{shared_module:hash(await fs.readFile(new URL('../../hosting/sites/fine-place.mjs',import.meta.url))),runner:hash(await fs.readFile(new URL(import.meta.url)))},
  expected:{path:path.resolve(values.expected),sha256:hash(expectedBytes)},
  places:manifest.queries.length,monthly_rows:rows.length,field_comparisons:rows.length*fields.length,
  unchanged_daytime_high_months:highs.size,maximum_absolute_difference:maximumDifference,
  correction_tiles_verified_unchanged:consumed.filter(file=>file.key.endsWith('.nwf.gz')).length,
  consumed,rows};
await fs.mkdir(path.dirname(values.output),{recursive:true});
await fs.writeFile(values.output,JSON.stringify(report,null,2)+'\n');
console.log(JSON.stringify({passes:report.passes,places:report.places,monthly_rows:report.monthly_rows,
  field_comparisons:report.field_comparisons,maximum_absolute_difference:maximumDifference,
  unchanged_daytime_high_months:report.unchanged_daytime_high_months,
  correction_tiles_verified_unchanged:report.correction_tiles_verified_unchanged}));
