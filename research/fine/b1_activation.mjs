// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Exercise activation and rollback on the same warm Worker and read-only dataset.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import {localWorkerFixture} from './local_worker_fixture.mjs';

const [overlay,output]=process.argv.slice(2);
if(!overlay||!output)throw Error('Usage: node research/fine/b1_activation.mjs <overlay> <report>');
const configuration=JSON.parse(await fs.readFile(path.join(overlay,'v6/fine/current.json')));
const storage=await fs.mkdtemp(path.join(os.tmpdir(),'nwf-b1-activation-'));
for(const prefix of new Set([configuration.nativeRoot,configuration.tileRoot,configuration.labelsRoot].map(key=>key.split('/')[0]))) {
  await fs.symlink(path.resolve(overlay,prefix),path.join(storage,prefix),'dir');
}
const pointerFile=path.join(storage,'v6/fine/current.json');
await fs.mkdir(path.dirname(pointerFile),{recursive:true});
let elapsed=0;
const request=localWorkerFixture(storage),originalNow=Date.now;
const baseTime=originalNow();
Date.now=()=>baseTime+elapsed;
const results=[];
try {
  for(const [stage,value]of [['before',null],['activated',configuration],['rolled-back',null]]) {
    if(stage!=='before')await fs.writeFile(pointerFile,JSON.stringify(value)+'\n');
    elapsed+=61000;
    const api=await request('http://127.0.0.1:5198/api/place?lat=32.6669&lon=-16.9241&period=july');
    assert.equal(api.status,200);
    const numbers=await api.json();
    assert.equal(Boolean(numbers.fineReplay),stage==='activated',`${stage}: warm Worker did not refresh the pointer`);
    const method=await request('http://127.0.0.1:5198/method').then(response=>response.text());
    assert.equal(method.includes(`/data/${configuration.attribution}`),stage==='activated',`${stage}: method attribution differs`);
    results.push({stage,fineReplay:Boolean(numbers.fineReplay),score:numbers.judgement?.meanScore,
      attributionActive:method.includes(`/data/${configuration.attribution}`)});
  }
} finally {Date.now=originalNow;}
await fs.mkdir(path.dirname(output),{recursive:true});
await fs.writeFile(output,JSON.stringify({written_by:'AI, GPT-6.1 Sol, 2026.10.18',passes:true,
  definition:'The same actual Worker bucket instance reads an absent pointer, the active JSON file and a stored literal-null rollback file after each 60-second cache expiry. Clock advanced in this isolated check; all weather reads use the actual bundle.',results},null,2)+'\n');
console.log(JSON.stringify({passes:true,results}));
