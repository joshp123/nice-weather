// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Test satellite targets against a separate 19-year time series at six control cities.
import fs from 'node:fs/promises';
import path from 'node:path';
import {parseArgs} from 'node:util';
import {createFinePlaceLoader} from '../../hosting/sites/fine-place.mjs';
const {values}=parseArgs({options:{targets:{type:'string'},normal:{type:'string'},native:{type:'string'},tiles:{type:'string'},output:{type:'string'},'since-year':{type:'string'}}});
for(const key of ['targets','normal','native','tiles','output'])if(!values[key])throw Error(`Missing --${key}`);
const targets=JSON.parse(await fs.readFile(values.targets)),normal=JSON.parse(await fs.readFile(values.normal));
if(values['since-year'])for(const control of normal.places){
  const since=Number(values['since-year']);
  control.monthly=Array.from({length:12},(_,month)=>{
    const rows=control.annualMonthly.filter(row=>row.month===month+1&&row.year>=since),days=rows.reduce((sum,row)=>sum+row.days,0);
    if(!days)throw Error(`No source reference days since ${since}`);
    return {month:month+1,days,sunnyShare:rows.reduce((sum,row)=>sum+row.sunnyShare*row.days,0)/days,hourlyDni:Array.from({length:12},(_,hour)=>rows.reduce((sum,row)=>sum+row.hourlyDni[hour]*row.days,0)/days)};
  });
}
const records=targets.records??targets,coordinates=place=>`${Math.round(place.latitude*1e5)},${Math.round(place.longitude*1e5)}`;
const indexed=new Map(records.map(place=>[coordinates(place),place]));
const manifest=JSON.parse(await fs.readFile(path.join(values.native,'manifest.json')));
const loader=createFinePlaceLoader(async key=>{
  if(key==='v6/fine/current.json')return Buffer.from(JSON.stringify({nativeRoot:'replay',tileRoot:'temperature',generation:manifest.generation,eveningModel:'uniform'}));
  const prefix=key.startsWith('replay/')?'replay/':'temperature/',root=prefix==='replay/'?values.native:values.tiles;
  try{return await fs.readFile(path.join(root,key.slice(prefix.length)));}catch(error){if(error.code==='ENOENT')return null;throw error;}
});
const result=[];
for(const control of normal.places){
  const target=indexed.get(coordinates(control.place));if(!target)throw Error(`Missing target ${control.place.slug}`);
  const packet=await loader.load(control.place.latitude,control.place.longitude);if(!packet)throw Error(`Missing native replay ${control.place.slug}`);
  const replay=packet.replay,months=[];
  for(let month=0;month<12;month++){
    const indices=replay.selectedIndices.filter(index=>Number(replay.dates[index].slice(5,7))===month+1);
    const nativeSun=indices.reduce((sum,index)=>sum+replay.sunnyHours[index]/Math.max(1,replay.daylightHours[index]),0)/indices.length;
    const nativeDni=Array.from({length:12},(_,hour)=>indices.reduce((sum,index)=>{
      const at=index*12+hour,air=replay.airC[at],dew=Math.min(air,replay.dewC[at]),vapour=6.105*Math.exp(17.27*dew/(237.7+dew));
      const wind=(air+.33*vapour-4-replay.shadeC[at])/.7;
      return sum+Math.max(0,(replay.sunC[at]-replay.shadeC[at])*(wind+10)/.105);
    },0)/indices.length);
    const reference=control.monthly[month],satellite=target.months[month];
    months.push({month:month+1,nativeSun,satelliteSun:satellite.sunnyShare,referenceSun:reference.sunnyShare,nativeDni,satelliteDni:satellite.hourlyDni,referenceDni:reference.hourlyDni});
  }
  const mae=(left,right)=>months.reduce((sum,row)=>sum+Math.abs(row[left]-row[right]),0)/12;
  const dniMae=field=>months.reduce((sum,row)=>sum+row[field].slice(0,11).reduce((hourSum,value,index)=>hourSum+Math.abs(value-row.referenceDni[index]),0),0)/132;
  const entry={place:control.place,sunShareMonthlyMaeBefore:mae('nativeSun','referenceSun'),sunShareMonthlyMaeAfter:mae('satelliteSun','referenceSun'),dniHourlyMonthlyMaeBefore:dniMae('nativeDni'),dniHourlyMonthlyMaeAfter:dniMae('satelliteDni'),months};
  entry.improvedSun=entry.sunShareMonthlyMaeAfter<=entry.sunShareMonthlyMaeBefore;
  entry.improvedDni=entry.dniHourlyMonthlyMaeAfter<=entry.dniHourlyMonthlyMaeBefore;
  result.push(entry);
}
await fs.writeFile(values.output,JSON.stringify({writtenBy:'AI, GPT-6.1 Sol, 2026-10-18',passes:result.every(row=>row.improvedSun&&row.improvedDni),referencePeriod:values['since-year']?[Number(values['since-year']),2023]:[2005,2023],caveat:'The TMY source uses months from its climatology, so the 19-year series is a separate full-time-series check, not an independent satellite instrument. It diagnoses a poor typical-year choice and differences from ERA5, not satellite retrieval error.',places:result},null,2)+'\n');
console.log(JSON.stringify(result.map(({place,...row})=>({name:place.validationName,...Object.fromEntries(Object.entries(row).filter(([key])=>key!=='months'))})),null,2));
