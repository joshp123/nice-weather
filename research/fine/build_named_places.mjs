// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Default-taste named-place products from the unchanged exact-pixel shared model.
import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import zlib from 'node:zlib';
import {Worker,isMainThread,parentPort,workerData} from 'node:worker_threads';
import {parseArgs} from 'node:util';
import {createFinePlaceLoader,placeNumbers,placeMonths,placeSampleScores,finePixel} from '../../hosting/sites/fine-place.mjs';
import {fitNamedSunshine,sunshineCoordinateKey} from '../sun/named_correction.mjs';
const PARTS=['warmth','evening','rainFactor','windFactor','humidityFactor','sunFactor'];
const TYPICAL=['feelsInSunAt14C','feelsInShadeAt15C','feelsInShadeAt21C','dewPointC','rainMmPerDay','wetDayShare','windKmh','sunnyShare','daylightHours'];
const PHYSICAL=['highC','feelsAt21C','dewPointC','wetDaysPercent','sunnyPercent','windKmh'];
const sha=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
function header(kind,count,rowStart=0){const bytes=Buffer.alloc(32);bytes.write('NWNP');bytes.writeUInt16LE(1,4);bytes[6]=kind;bytes[7]=kind===1?96:0;bytes.writeUInt32LE(count,8);bytes.writeUInt32LE(rowStart,12);bytes.writeUInt16LE(kind===3?21:0,16);return bytes;}
async function compute({data,pointer,coordinates,groups,scoresBuffer,factsBuffer,worker,workers,sunshineFile}){
 const scores=new Uint8Array(scoresBuffer),facts=new Float32Array(factsBuffer),count=coordinates.length;
 const sunshine=sunshineFile?new Map(JSON.parse(await fs.readFile(sunshineFile,'utf8')).records.map(row=>[sunshineCoordinateKey(row.latitude,row.longitude),row.months])):null;
 const loader=createFinePlaceLoader(async key=>{if(key==='v6/fine/current.json')return new Uint8Array(pointer);try{return new Uint8Array(await fs.readFile(path.join(data,key)));}catch(error){if(error.code==='ENOENT')return null;throw error;}});
 let done=0,unsupported=0,satellitePlaces=0;const start=performance.now();
 const sunshineDiagnostics={scoredHoursWithoutNativeSun:0,maximumUnappliedScoredHeatingC:0};
 for(let groupIndex=worker;groupIndex<groups.length;groupIndex+=workers){
  const cached=new Map();
  for(const row of groups[groupIndex]){
   const [lat,lon]=coordinates[row],pixel=finePixel(lat,lon),key=sunshine?sunshineCoordinateKey(lat,lon):`${pixel.row},${pixel.column}`;
   let record=cached.get(key);
   if(record===undefined){
    const packet=await loader.load(lat,lon);
    if(!packet)record=null;
    else{
     const satelliteMonths=sunshine?.get(sunshineCoordinateKey(lat,lon));
     if(satelliteMonths?.some(month=>month!==null)){packet.sunshine=fitNamedSunshine(packet,satelliteMonths,sunshineDiagnostics);satellitePlaces++;}
     const samples=placeSampleScores(packet),months=placeMonths(packet);
     if(samples.some(days=>days.length!==8))throw Error(`Expected eight representative days per month at ${lat},${lon}`);
     const judgement=Array.from({length:12},(_,month)=>placeNumbers(packet,{months:[month+1]}));
     record={scores:samples.flat().map(Math.floor),facts:judgement.map((value,month)=>[...PARTS.map(field=>value.parts[field]),...TYPICAL.map(field=>value.typical[field]),...PHYSICAL.map(field=>months[month][field])])};
    }
    cached.set(key,record);
   }
   if(!record)unsupported++;
   else{
    for(let sample=0;sample<96;sample++)scores[sample*count+row]=record.scores[sample];
    for(let month=0;month<12;month++)for(let field=0;field<21;field++)facts[(month*21+field)*count+row]=record.facts[month][field];
   }
   done++;
  }
  if(done>=1000&&(groupIndex<workers||done%1000<groups[groupIndex].length))parentPort?.postMessage({worker,done,unsupported,seconds:(performance.now()-start)/1000});
 }
 return {worker,done,unsupported,satellitePlaces,sunshineDiagnostics,seconds:(performance.now()-start)/1000};
}
if(!isMainThread){try{parentPort.postMessage({complete:await compute(workerData)});}catch(error){throw error;}}
else{
 const {values}=parseArgs({options:{data:{type:'string'},pointer:{type:'string'},places:{type:'string',default:'artifacts/places.tsv'},labels:{type:'string'},output:{type:'string'},workers:{type:'string',default:'4'},queries:{type:'string'},sunshine:{type:'string'}}});
 for(const field of ['data','pointer','output'])if(!values[field])throw Error(`Missing --${field}`);
 const started=performance.now(),pointer=await fs.readFile(values.pointer),configuration=JSON.parse(pointer),sourceHashes={pointer:sha(pointer)},rows=[];
 if(values.sunshine)sourceHashes.sunshine=sha(await fs.readFile(values.sunshine));
 if(values.queries){const bytes=await fs.readFile(values.queries);sourceHashes.queries=sha(bytes);for(const query of JSON.parse(bytes))rows.push([query.lat??query.latitude,query.lon??query.longitude]);}
 else{
  const bytes=await fs.readFile(values.places);sourceHashes.places=sha(bytes);for(const line of bytes.toString().trim().split('\n')){const fields=line.split('\t');rows.push([Number(fields[3]),Number(fields[4])]);}
  const labels=values.labels??path.join(values.data,configuration.labelsRoot);
  for(const level of[1,2]){const bytes=await fs.readFile(path.join(labels,`labels-${level}.json`));sourceHashes[`labels-${level}`]=sha(bytes);for(const row of JSON.parse(bytes))rows.push([row[2],row[3]]);}
 }
 const coordinateMap=new Map(rows.map(row=>[row.join(','),row])),coordinates=[...coordinateMap.values()].sort((a,b)=>a[0]-b[0]||a[1]-b[1]),count=coordinates.length,groupsMap=new Map();
 const indexKeys=new Set();for(let row=0;row<count;row++){const [lat,lon]=coordinates[row],key=`${Math.round(lat*1e5)},${Math.round(lon*1e5)}`;if(indexKeys.has(key))throw Error(`Coordinate quantization collision ${key}`);indexKeys.add(key);const p=finePixel(lat,lon),tile=Math.floor(p.row/256)*169+Math.floor(p.column/256);if(!groupsMap.has(tile))groupsMap.set(tile,[]);groupsMap.get(tile).push(row);}
 const groups=[...groupsMap].sort((a,b)=>a[0]-b[0]).map(([,rows])=>rows),scoresBuffer=new SharedArrayBuffer(count*96),factsBuffer=new SharedArrayBuffer(count*12*21*4);
 new Uint8Array(scoresBuffer).fill(255);new Float32Array(factsBuffer).fill(NaN);
 const workers=Number(values.workers),results=await Promise.all(Array.from({length:workers},(_,worker)=>new Promise((resolve,reject)=>{const process=new Worker(new URL(import.meta.url),{workerData:{data:values.data,pointer,coordinates,groups,scoresBuffer,factsBuffer,worker,workers,sunshineFile:values.sunshine}});let done=false;process.on('message',message=>{if(message.complete){done=true;resolve(message.complete);}else console.log(JSON.stringify(message));});process.on('error',reject);process.on('exit',code=>{if(code||!done)reject(Error(`Worker ${worker} exited ${code}`));});})));
 await fs.mkdir(values.output,{recursive:true});const files={};
 async function write(name,plain){const bytes=zlib.gzipSync(plain,{level:9});await fs.writeFile(path.join(values.output,name),bytes);files[name]={bytes:bytes.length,uncompressedBytes:plain.length,sha256:sha(bytes)};}
 const coordinateBytes=Buffer.alloc(count*8);for(let row=0;row<count;row++){coordinateBytes.writeInt32LE(Math.round(coordinates[row][0]*1e5),row*8);coordinateBytes.writeInt32LE(Math.round(coordinates[row][1]*1e5),row*8+4);}
 await write('climate.bin.gz',Buffer.concat([header(1,count),coordinateBytes,Buffer.from(scoresBuffer)]));
 const facts=new Float32Array(factsBuffer),factsRows=16384;
 for(let start=0,shard=0;start<count;start+=factsRows,shard++){const length=Math.min(factsRows,count-start),bytes=Buffer.alloc(32+length*12*21*4);header(3,length,start).copy(bytes);for(let field=0;field<12*21;field++)for(let row=0;row<length;row++)bytes.writeFloatLE(facts[field*count+start+row],32+(field*length+row)*4);await write(`facts-${String(shard).padStart(3,'0')}.bin.gz`,bytes);}
 await fs.writeFile(path.join(values.output,'coordinates.json'),JSON.stringify(coordinates)+'\n');
 const generation=sourceHashes.sunshine?sha(Buffer.from(configuration.generation+sourceHashes.sunshine)).slice(0,32):configuration.generation;
 const manifest={written_by:'AI, GPT-6.1 Sol, 2026.10.18',format:'nwnp-v1',generation,temperatureGeneration:configuration.generation,eveningModel:'uniform',defaultTasteOnly:true,sourceRows:rows.length,places:count,unsupported:results.reduce((sum,r)=>sum+r.unsupported,0),satellitePlaces:results.reduce((sum,r)=>sum+r.satellitePlaces,0),factsRows,partsFields:PARTS,typicalFields:TYPICAL,physicalFields:PHYSICAL,sourceHashes,sourceModuleHashes:Object.fromEntries(await Promise.all(['hosting/sites/fine-place.mjs','hosting/sites/fine-replay.mjs','research/fine/build_named_places.mjs','research/sun/named_correction.mjs'].map(async name=>[name,sha(await fs.readFile(name))]))),files,outputFiles:Object.keys(files).length,outputBytes:Object.values(files).reduce((sum,f)=>sum+f.bytes,0),workers:results,seconds:(performance.now()-started)/1000};
 await fs.writeFile(path.join(values.output,'BUILD.json'),JSON.stringify(manifest,null,2)+'\n');console.log(JSON.stringify({complete:true,...manifest,files:undefined,sourceModuleHashes:undefined}));
}
