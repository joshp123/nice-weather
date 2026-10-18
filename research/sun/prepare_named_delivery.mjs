// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Stage a new climate generation and a separate activation; never publish here.
import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import {parseArgs} from 'node:util';

const {values}=parseArgs({options:{source:{type:'string'},output:{type:'string'},configuration:{type:'string'},attribution:{type:'string'},support:{type:'string'}}});
for(const name of ['source','output','configuration','attribution'])if(!values[name])throw Error(`Missing --${name}`);
const manifest=JSON.parse(await fs.readFile(path.join(values.source,'BUILD.json'),'utf8'));
if(!manifest.sourceHashes.sunshine)throw Error('This build has no satellite correction');
const previous=JSON.parse(await fs.readFile(values.configuration,'utf8'));
if(previous.generation!==manifest.temperatureGeneration)throw Error('The active temperature generation differs from this build');
const root=path.basename(values.source),files=[];
const sha=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
async function write(key,bytes,expected) {
  if(expected&&sha(bytes)!==expected)throw Error(`Source checksum differs: ${key}`);
  const destination=path.join(values.output,key);
  await fs.mkdir(path.dirname(destination),{recursive:true});await fs.writeFile(destination,bytes);
  files.push({key,bytes:bytes.length,sha256:sha(bytes)});
}
for(const [name,entry] of Object.entries(manifest.files))await write(`${root}/${name}`,await fs.readFile(path.join(values.source,name)),entry.sha256);
const attribution=await fs.readFile(values.attribution,'utf8');
await write(`${root}/ATTRIBUTION.txt`,Buffer.from(attribution));
if(values.support)await write(`${root}/SOURCE_SUPPORT.tsv.gz`,await fs.readFile(values.support));
const {workers,...publicManifest}=manifest;
await write(`${root}/MANIFEST.json`,Buffer.from(JSON.stringify(publicManifest,null,2)+'\n'));
const configuration={...previous,generation:manifest.generation,root,attribution:`${root}/ATTRIBUTION.txt`,sunshine:{method:'satellite-monthly',correctedPlaces:manifest.satellitePlaces}};
for(const [suffix,pointer] of [['activation',configuration],['rollback',previous]]) {
  const destination=`${values.output}-${suffix}/v6/named/current.json`;
  await fs.mkdir(path.dirname(destination),{recursive:true});await fs.writeFile(destination,JSON.stringify(pointer)+'\n');
}
const inventory={written_by:'AI, GPT-6.1 Sol, 2026.10.18',generation:manifest.generation,previousGeneration:previous.generation,files,totalBytes:files.reduce((sum,file)=>sum+file.bytes,0),totalFiles:files.length};
await fs.writeFile(`${values.output}.MANIFEST.json`,JSON.stringify(inventory,null,2)+'\n');
console.log(JSON.stringify({files:inventory.totalFiles,bytes:inventory.totalBytes,activation:`${values.output}-activation/v6/named/current.json`,rollback:`${values.output}-rollback/v6/named/current.json`}));
