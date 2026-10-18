// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Export the exact target-clock pixels used by the shared named-place model.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import zlib from 'node:zlib';
import {parseArgs} from 'node:util';
import {finePixel,nativeFineIndex,createFinePlaceLoader} from '../../hosting/sites/fine-place.mjs';
import {targetFineZone} from '../../hosting/sites/fine-replay.mjs';
const {values}=parseArgs({options:{data:{type:'string'},coordinates:{type:'string'},queries:{type:'string'},output:{type:'string'},samples:{type:'string',default:'400'}}});
for(const key of ['data','coordinates','queries','output'])assert(values[key],`Missing --${key}`);
const nativeRoot='v6-fine-b1-20261018/replay',tileRoot='v6-fine-b1-20261018/temperature';
const manifest=JSON.parse(await fs.readFile(path.join(values.data,nativeRoot,'manifest.json')));
const canonical=JSON.parse(await fs.readFile(values.coordinates,'utf8'));
const queries=JSON.parse(await fs.readFile(values.queries,'utf8')).map(query=>[query.latitude??query.lat,query.longitude??query.lon]);
const key=([latitude,longitude])=>`${Math.round(latitude*1e5)},${Math.round(longitude*1e5)}`;
const coordinates=[...new Map([...canonical,...queries].map(point=>[key(point),point])).values()].sort((a,b)=>a[0]-b[0]||a[1]-b[1]);
const groups=new Map(),nativeGroups=new Map(),records=[];
for(const point of coordinates){const pixel=finePixel(...point),tile=`${String(Math.floor(pixel.row/256)).padStart(3,'0')}/${String(Math.floor(pixel.column/256)).padStart(3,'0')}`;if(!groups.has(tile))groups.set(tile,[]);groups.get(tile).push({point,pixel});}
let missingTiles=0,nullPixels=0;
for(const [tile,points]of groups){
 let bytes;
 try{bytes=zlib.gunzipSync(await fs.readFile(path.join(values.data,nativeRoot,'zones',tile+'.nwz.gz')));}catch(error){if(error.code!=='ENOENT')throw error;missingTiles++;}
 for(const {point,pixel}of points){
  const zone=bytes?targetFineZone(bytes,pixel,manifest.generation):null;
  if(zone===null)nullPixels++;else assert(manifest.timezoneNames[zone],`Missing zone name ${zone}`);
  records.push([...key(point).split(',').map(Number),zone]);
  const native=nativeFineIndex(90-(pixel.row+.5)/120,-180+(pixel.column+.5)/120);
  if(!nativeGroups.has(native))nativeGroups.set(native,[]);nativeGroups.get(native).push({point,zone,tile});
 }
}
records.sort((a,b)=>a[0]-b[0]||a[1]-b[1]);
const exported={writtenBy:'AI, GPT-6.1 Sol, 2026-10-18',generation:manifest.generation,nativeRoot,coordinateScale:100000,timezoneNames:manifest.timezoneNames,coordinates:records};
await fs.mkdir(path.dirname(values.output),{recursive:true});await fs.writeFile(values.output,JSON.stringify(exported)+'\n');
const exportedByCoordinate=new Map(records.map(([latitude,longitude,zone])=>[`${latitude},${longitude}`,zone]));
const boundaryGroups=[...nativeGroups.values()].filter(points=>new Set(points.filter(row=>row.zone!==null).map(row=>row.zone)).size>1);
const boundaryPoints=boundaryGroups.flatMap(points=>{const byZone=new Map();for(const row of points)if(row.zone!==null&&!byZone.has(row.zone))byZone.set(row.zone,row);return [...byZone.values()];});
// Spread checks across all boundary groups rather than one dense conurbation.
const wanted=Number(values.samples),chosen=Array.from({length:Math.min(wanted,boundaryPoints.length)},(_,index)=>boundaryPoints[Math.floor(index*boundaryPoints.length/Math.min(wanted,boundaryPoints.length))].point);
const samples=[...new Map([...queries,...chosen].map(point=>[key(point),point])).values()];
const configuration={nativeRoot,tileRoot,generation:manifest.generation,eveningModel:'uniform'};
const loader=createFinePlaceLoader(async address=>{if(address==='v6/fine/current.json')return Buffer.from(JSON.stringify(configuration));try{return await fs.readFile(path.join(values.data,address));}catch(error){if(error.code==='ENOENT')return null;throw error;}});
let checked=0,unsupported=0;
for(const point of samples){const packet=await loader.load(...point);if(!packet){unsupported++;continue;}const index=exportedByCoordinate.get(key(point));assert.equal(manifest.timezoneNames[index],packet.replay.targetTimezone,`Target timezone differs at ${point}`);checked++;}
const proof={writtenBy:exported.writtenBy,passes:true,generation:manifest.generation,canonicalCoordinates:canonical.length,exportedCoordinates:records.length,zoneTiles:groups.size,missingTiles,nullPixels,boundaryNativePoints:boundaryGroups.length,boundaryCandidates:boundaryPoints.length,boundarySamples:chosen.length,validationPlaces:queries.length,actualPacketsChecked:checked,unsupportedSamples:unsupported,bytes:(await fs.stat(values.output)).size};
await fs.writeFile(values.output+'.proof.json',JSON.stringify(proof,null,2)+'\n');console.log(JSON.stringify(proof));
