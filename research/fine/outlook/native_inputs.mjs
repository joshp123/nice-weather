// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Exercise the Go binary decoder on the same gzip packets read by the JS loader.
import fs from 'node:fs';
import path from 'node:path';
const [inputPath,nativeRoot,outputPath]=process.argv.slice(2);
const manifest=JSON.parse(fs.readFileSync(path.join(nativeRoot,'manifest.json')));
const records=fs.readFileSync(inputPath,'utf8').trim().split('\n').filter(line=>line.startsWith('{')).map(JSON.parse);
for(const record of records){
 const replay=record.packet.replay,zone=manifest.timezoneNames.indexOf(replay.targetTimezone);
 if(zone<0)throw new Error(`No native timezone ${replay.targetTimezone}`);
 record.nativeFile=path.join(nativeRoot,'native',String(replay.fineIndex),`${zone}.nwfp.gz`);
 fs.accessSync(record.nativeFile);
}
fs.writeFileSync(outputPath,records.map(record=>JSON.stringify(record)).join('\n')+'\n');
console.log(JSON.stringify({packets:records.length}));
