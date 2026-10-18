// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Exact-date radiation substitution: the same weather samples and shared score rule.
import fs from 'node:fs/promises';
import path from 'node:path';
import {execFileSync} from 'node:child_process';
import {parseArgs} from 'node:util';
import {fileURLToPath} from 'node:url';
import {createFinePlaceLoader,placeNumbers} from '../../hosting/sites/fine-place.mjs';
import {fitNamedSunshine,sunshineCoordinateKey} from './named_correction.mjs';
const {values}=parseArgs({options:{targets:{type:'string'},normal:{type:'string'},native:{type:'string'},tiles:{type:'string'},output:{type:'string'},python:{type:'string'},'goes-root':{type:'string'}}});
for(const key of ['targets','normal','native','tiles','output','python'])if(!values[key])throw Error(`Missing --${key}`);
const targets=JSON.parse(await fs.readFile(values.targets)),normal=JSON.parse(await fs.readFile(values['goes-root']?values.normal:path.join(values.normal,'summary.json')));
const indexed=new Map(targets.records.map(place=>[sunshineCoordinateKey(place.latitude,place.longitude),place]));
const manifest=JSON.parse(await fs.readFile(path.join(values.native,'manifest.json')));
const loader=createFinePlaceLoader(async key=>{
  if(key==='v6/fine/current.json')return Buffer.from(JSON.stringify({nativeRoot:'replay',tileRoot:'temperature',generation:manifest.generation,eveningModel:'uniform'}));
  const prefix=key.startsWith('replay/')?'replay/':'temperature/',root=prefix==='replay/'?values.native:values.tiles;
  try{return await fs.readFile(path.join(root,key.slice(prefix.length)));}catch(error){if(error.code==='ENOENT')return null;throw error;}
});
const inputs=[];
for(const control of normal.places){
  const place=control.place,packet=await loader.load(place.latitude,place.longitude);if(!packet)throw Error('Missing replay '+place.slug);
  inputs.push({place,packet,query:{slug:place.slug,raw:path.join(values.normal,place.slug.replace('/','-')+'-2005-2023.json'),...(values['goes-root']?{goesRoot:values['goes-root'],latitude:place.latitude,longitude:place.longitude}:{}),timezone:packet.replay.targetTimezone,dates:packet.replay.dates}});
}
const days=JSON.parse(execFileSync(values.python,[fileURLToPath(new URL('./named_satellite_days.py',import.meta.url))],{input:JSON.stringify(inputs.map(row=>row.query)),maxBuffer:10*1024*1024}));
const result=[],fields=['score','niceDayCount','perfectDayCount'];
for(const input of inputs){
  const {place,packet}=input,satellite=days.find(row=>row.slug===place.slug),target=indexed.get(sunshineCoordinateKey(place.latitude,place.longitude));
  const selected=packet.replay.selectedIndices.filter(index=>satellite.days[index]);
  const before={...packet,replay:{...packet.replay,selectedIndices:selected}};
  const after={...before,sunshine:fitNamedSunshine(packet,target.months)};
  const sunFactorOnly={...after,sunshine:after.sunshine.map(month=>month?{...month,dniScale:Array(12).fill(1)}:null)};
  const radiationWarmthOnly={...after,sunshine:after.sunshine.map(month=>month?{...month,sunnyShareOffset:0}:null)};
  const additive={...sunFactorOnly,replay:{...sunFactorOnly.replay,sunC:Array.from(sunFactorOnly.replay.sunC)}};
  const additiveTargets=[];
  for(let month=0;!values['goes-root']&&month<12;month++)for(let hour=0;hour<12;hour++){
    const indices=packet.replay.selectedIndices.filter(index=>Number(packet.replay.dates[index].slice(5,7))===month+1);
    const native=indices.map(index=>{
      const at=index*12+hour,air=packet.replay.airC[at],dew=Math.min(air,packet.replay.dewC[at]),vapour=6.105*Math.exp(17.27*dew/(237.7+dew));
      const wind=(air+.33*vapour-4-packet.replay.shadeC[at])/.7;
      return {index,wind,dni:Math.max(0,(packet.replay.sunC[at]-packet.replay.shadeC[at])*(wind+10)/.105)};
    });
    const targetMean=target.months[month].hourlyDni[hour],supported=native.some(row=>row.dni>0);
    let lower=-Math.max(0,...native.map(row=>row.dni)),upper=Math.max(1,targetMean*native.length);
    const meanAt=offset=>native.reduce((sum,row)=>sum+(row.dni>0?Math.max(0,row.dni+offset):0),0)/native.length;
    if(supported)for(let iteration=0;iteration<60;iteration++){
      const offset=(lower+upper)/2;if(meanAt(offset)<targetMean)lower=offset;else upper=offset;
    }
    const offset=supported?(lower+upper)/2:0;
    for(const row of native){
      const at=row.index*12+hour,dni=row.dni>0?Math.max(0,row.dni+offset):0;
      additive.replay.sunC[at]=packet.replay.shadeC[at]+.105*dni/(row.wind+10);
    }
    additiveTargets.push({month:month+1,hour:hour+10,nativeMean:native.reduce((sum,row)=>sum+row.dni,0)/native.length,targetMean,achievedMean:meanAt(offset),supported,offset});
  }
  const reference={...before,replay:{...before.replay,sunC:Array.from(before.replay.sunC),sunnyHours:Array.from(before.replay.sunnyHours)}};
  for(const index of selected){
    const hourly=satellite.days[index],daylight=Math.max(1,packet.replay.daylightHours[index]);
    reference.replay.sunnyHours[index]=hourly.slice(0,daylight).filter(value=>value>=300).length;
    for(let hour=0;hour<12;hour++){
      const at=index*12+hour,air=packet.replay.airC[at],dew=Math.min(air,packet.replay.dewC[at]),vapour=6.105*Math.exp(17.27*dew/(237.7+dew));
      const wind=(air+.33*vapour-4-packet.replay.shadeC[at])/.7;
      reference.replay.sunC[at]=packet.replay.shadeC[at]+.105*hourly[hour]/(wind+10);
    }
  }
  const monthlyRetainedSamples=Array.from({length:12},(_,month)=>selected.filter(index=>Number(packet.replay.dates[index].slice(5,7))===month+1).length),availableMonths=monthlyRetainedSamples.flatMap((count,index)=>count?[index+1]:[]);
  const periods=[{months:availableMonths},...Array.from({length:12},(_,month)=>monthlyRetainedSamples[month]?{kind:'month',month:month+1}:null)];
  const scenarios=Object.fromEntries(Object.entries({before,after,reference,sunFactorOnly,radiationWarmthOnly,...(!values['goes-root']?{additive}:{})}).map(([name,packet])=>[name,periods.map(period=>period?placeNumbers(packet,period):null)]));
  const errors=Object.fromEntries(['before','after',...(!values['goes-root']?['additive']:[])].map(name=>[name,Object.fromEntries(fields.map(field=>[field,availableMonths.reduce((sum,month)=>sum+Math.abs(scenarios[name][month][field]-scenarios.reference[month][field]),0)/availableMonths.length]))]));
  result.push({place,retainedSamples:selected.length,excludedSamples:packet.replay.selectedIndices.length-selected.length,monthlyRetainedSamples,availableMonths,annualCoversAllMonths:availableMonths.length===12,retainedDates:selected.map(index=>packet.replay.dates[index]),excludedDates:packet.replay.selectedIndices.filter(index=>!satellite.days[index]).map(index=>packet.replay.dates[index]),scenarios,monthlyMeanAbsoluteErrors:errors,additiveTargets});
}
const report={writtenBy:'AI, GPT-6.1 Sol, 2026-10-18',purpose:'Radiation diagnostic, not a full observed-day weather validation. At each retained ERA5 representative date, substitute exact-date satellite DNI and its sunny-hour threshold while keeping temperature anchors, native shade weather, humidity, rain and wind. Unavailable satellite dates are excluded identically from all scenarios; remaining samples are reweighted within month through the shared function. Annual aggregates cover only months with retained samples; the monthly sample counts are explicit. The satellite day-by-day cloud and weather relationship can differ from ERA5 and cannot be reconstructed by a monthly correction.',places:result};
await fs.writeFile(values.output,JSON.stringify(report,null,2)+'\n');
console.log(JSON.stringify(result.map(row=>({name:row.place.validationName,samples:row.retainedSamples,availableMonths:row.availableMonths,aggregateDays:row.scenarios.before[0].totalDays,aggregate:Object.fromEntries(Object.entries(row.scenarios).map(([name,values])=>[name,fields.map(field=>values[0][field])])),monthlyMeanAbsoluteErrors:row.monthlyMeanAbsoluteErrors})),null,2));
