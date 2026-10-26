// Written by AI on 2026.10.19 by model GPT-6.1 Sol.
// Independent measurement inputs; never writes to a production fine build.
import fs from 'node:fs/promises';
import path from 'node:path';
import {createFinePlaceLoader,finePixel,nativeFineIndex} from '../../hosting/sites/fine-place.mjs';

const [data,output]=process.argv.slice(2);
if(!data||!output)throw new Error('Usage: fine_truth_prepare.mjs DATA OUTPUT');
const release=path.join(data,'releases/climate-25/fine');
const loader=createFinePlaceLoader(async key=> {
  const filename=key==='v6/fine/current.json'?path.join(release,'fine-current.json'):path.join(data,key);
  try{return new Uint8Array(await fs.readFile(filename));}catch(error){if(error.code==='ENOENT')return null;throw error;}
});
const regions=[['Málaga',36.7213,-4.4214],['Funchal',32.65,-16.91],['San Diego',32.7157,-117.1611],['San Francisco',37.7749,-122.4194],['Tenerife south',28.12,-16.73],['Cape Town',-33.9258,18.4232],['Lisbon',38.7223,-9.1393],['Rotterdam',51.9244,4.4777],['Singapore',1.3521,103.8198],['Cairo',30.0444,31.2357],['Austin',30.2672,-97.7431],['Seville',37.3891,-5.9845],['Los Angeles',34.0522,-118.2437],['Lima',-12.0464,-77.0428],['Valparaiso',-33.0472,-71.6127],['Bangkok',13.7563,100.5018],['Nairobi',-1.2864,36.8172],['Mexico City',19.4326,-99.1332],['Sydney',-33.8688,151.2093],['Melbourne',-37.8136,144.9631],['Tokyo',35.6762,139.6503],['Mumbai',19.076,72.8777],['Reykjavik',64.1466,-21.9426],['Bergen',60.3913,5.3221],['New York',40.7128,-74.006],['Miami',25.7617,-80.1918],['Madrid',40.4168,-3.7038],['Chania',35.5138,24.018],['Honolulu',21.3099,-157.8581],['Rio de Janeiro',-22.9068,-43.1729],['Addis Ababa',9.03,38.74],['Wellington',-41.2866,174.7756]];
const points=[],native=new Map(),missing=[];
for(const [name,lat,lon]of regions){
  const anchor=finePixel(lat,lon),target=nativeFineIndex(90-(anchor.row+.5)/120,-180+(anchor.column+.5)/120);
  let accepted=0;
  for(const [dr,dc]of [[0,0],[-6,-6],[-6,0],[-6,6],[0,-6],[0,6],[6,-6],[6,0],[6,6],[-3,-3],[-3,0],[-3,3],[0,-3],[0,3],[3,-3],[3,0],[3,3]]){
    const latitude=90-(anchor.row+dr+.5)/120,longitude=-180+(anchor.column+dc+.5)/120;
    if(nativeFineIndex(latitude,longitude)!==target)continue;
    const packet=await loader.load(latitude,longitude);if(!packet)continue;
    const key=`${packet.replay.fineIndex}-${packet.replay.targetTimezone}`;
    if(!native.has(key))native.set(key,{key,fine:packet.replay.fineIndex,zone:packet.replay.targetTimezone,dates:packet.replay.physicalResponse.dates,selectedIndices:packet.replay.selectedDateIndices});
    points.push({name:`${name}${accepted?' '+accepted:''}`,region:name,latitude,longitude,key,corrections:packet.corrections,eveningModel:packet.eveningModel,sunshine:packet.sunshine,selectedIndices:packet.replay.selectedDateIndices,pixel:packet.pixel});
    if(++accepted===8)break;
  }
  if(accepted<8)missing.push({name,accepted});
  console.log(name,accepted);
}
await fs.mkdir(output,{recursive:true});
await fs.writeFile(path.join(output,'inputs.json'),JSON.stringify({written_by:'AI, GPT-6.1 Sol, 2026.10.19',scope:'Deterministic geographically stratified city regions, up to eight supported 1km pixels in each region; not a random worldwide sample',points,native:[...native.values()],missing},null,2)+'\n');
console.log(JSON.stringify({points:points.length,native:native.size,missing}));
