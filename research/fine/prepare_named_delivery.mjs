// Written by AI (GPT-6.1 Sol), 2026-10-18.
// Copy only the compact named-place inputs; never publish or touch the full replay.
import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import {parseArgs} from 'node:util';

const {values}=parseArgs({options:{data:{type:'string'},output:{type:'string'},forecast:{type:'string'},run:{type:'string'}}});
for(const name of ['data','output','forecast','run'])if(!values[name])throw Error(`Missing --${name}`);
const root='v6-named-b1-20261018',source=path.join(values.data,root);
const manifest=JSON.parse(await fs.readFile(path.join(source,'BUILD.json'),'utf8'));
const files=[];
const sha=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
async function write(key,bytes,expected) {
  if(expected&&sha(bytes)!==expected)throw Error(`Source checksum differs: ${key}`);
  const destination=path.join(values.output,key);
  await fs.mkdir(path.dirname(destination),{recursive:true});await fs.writeFile(destination,bytes);
  files.push({key,bytes:bytes.length,sha256:sha(bytes)});
}
for(const [name,entry] of Object.entries(manifest.files))await write(`${root}/${name}`,await fs.readFile(path.join(source,name)),entry.sha256);
const full=path.join(values.data,'v6-fine-b1-20261018');
for(const name of ['labels-1.json','labels-2.json','manifest.json','ATTRIBUTION.txt'])await write(`${root}/labels/${name}`,await fs.readFile(path.join(full,'labels',name)));
const attribution=await fs.readFile(path.join(full,'ATTRIBUTION.txt'),'utf8');
await write(`${root}/ATTRIBUTION.txt`,Buffer.from(`${attribution}\nCOMPACT NAMED-PLACE DELIVERY\nDefault taste only. Monthly eight-day score samples and card facts are precomputed\nfrom the same exact 30 arc-second pixel and uniform evening model.\nUnsupported coordinates and changed tastes use the existing overview data.\nThe same source licences and attribution apply to these derived values.\n`));
await write(`v6/forecast/${values.run}/named.bin.gz`,await fs.readFile(values.forecast));
const publicManifest={...manifest,files:manifest.files};
delete publicManifest.workers;
await write(`${root}/MANIFEST.json`,Buffer.from(JSON.stringify(publicManifest,null,2)+'\n'));
const configuration={version:1,generation:manifest.generation,root,climate:'climate.bin.gz',factsRows:manifest.factsRows,labelsRoot:`${root}/labels`,attribution:`${root}/ATTRIBUTION.txt`,defaultTasteOnly:true,eveningModel:'uniform'};
for(const [folder,pointer] of [['activation',configuration],['rollback',null]]) {
  const destination=`${values.output}-${folder}/v6/named/current.json`;
  await fs.mkdir(path.dirname(destination),{recursive:true});await fs.writeFile(destination,JSON.stringify(pointer)+'\n');
}
const inventory={written_by:'AI, GPT-6.1 Sol, 2026.10.18',forecastRun:values.run,generation:manifest.generation,files,totalBytes:files.reduce((sum,file)=>sum+file.bytes,0),totalFiles:files.length};
await fs.writeFile(`${values.output}.MANIFEST.json`,JSON.stringify(inventory,null,2)+'\n');
console.log(JSON.stringify({files:inventory.totalFiles,bytes:inventory.totalBytes,activation:`${values.output}-activation/v6/named/current.json`,rollback:`${values.output}-rollback/v6/named/current.json`}));
