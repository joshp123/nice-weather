// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Read actual sealed fine data and save production transport packets for the GPU check.
import fs from 'node:fs/promises';
import path from 'node:path';
import zlib from 'node:zlib';
import {createFinePlaceLoader,encodeFinePlacePacket} from '../../../hosting/sites/fine-place.mjs';
const [data,output]=process.argv.slice(2);
if(!data||!output)throw Error('Usage: node make-fine-fixture.mjs <data-root> <output-folder>');
const release='v6-fine-b1-20261018',manifest=JSON.parse(await fs.readFile(path.join(data,release,'replay/manifest.json'),'utf8'));
const pointer={nativeRoot:`${release}/replay`,tileRoot:`${release}/temperature`,generation:manifest.generation,eveningModel:'uniform',status:'ready'};
const loader=createFinePlaceLoader(async key=>{
 if(key==='v6/fine/current.json')return new TextEncoder().encode(JSON.stringify(pointer));
 try{return new Uint8Array(await fs.readFile(path.join(data,key)));}catch(error){if(error.code==='ENOENT')return null;throw error;}
});
const queries=[["Chania", 35.5138, 24.018], ["Málaga", 36.7202, -4.4203], ["Tenerife south", 28.05, -16.72], ["Cape Town", -33.9249, 18.4241], ["San Diego", 32.7157, -117.1611], ["Rotterdam", 51.9225, 4.4792], ["Sydney", -33.8688, 151.2093], ["Bangkok", 13.7563, 100.5018], ["London", 51.5074, -0.1278], ["New York", 40.7128, -74.006], ["San Francisco", 37.7749, -122.4194], ["Lisbon", 38.7223, -9.1393], ["Agadir", 30.4278, -9.5981], ["Funchal", 32.6669, -16.9241], ["Santiago", -33.4489, -70.6693], ["Valparaíso", -33.0472, -71.6127], ["Buenos Aires", -34.6037, -58.3816], ["Rio de Janeiro", -22.9068, -43.1729], ["Melbourne", -37.8136, 144.9631], ["Perth", -31.9505, 115.8605], ["Tokyo", 35.6762, 139.6503], ["Singapore", 1.3521, 103.8198], ["Hong Kong", 22.3193, 114.1694], ["Taipei", 25.033, 121.5654], ["Auckland", -36.8485, 174.7633], ["Honolulu", 21.3099, -157.8581], ["Los Angeles", 34.0522, -118.2437], ["Athens", 37.9838, 23.7275], ["Karachi", 24.8607, 67.0011], ["Nicosia", 35.1856, 33.3823]];
await fs.mkdir(output,{recursive:true});
const rows=[];
for(const [name,latitude,longitude]of queries){
 const packet=await loader.load(latitude,longitude);if(!packet)throw Error(`No actual fine packet for ${name}`);
 const filename=`${rows.length}.nwpl.gz`;await fs.writeFile(path.join(output,filename),zlib.gzipSync(encodeFinePlacePacket(packet)));
 rows.push({name,latitude,longitude,filename,pixel:packet.pixel,generation:manifest.generation,eveningModel:'uniform'});
}
await fs.writeFile(path.join(output,'index.json'),JSON.stringify(rows,null,2));
console.log(JSON.stringify({packets:rows.length,generation:manifest.generation,output}));
