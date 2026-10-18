// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Fit independent satellite targets to real replay, then read the actual packed product.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {execFileSync} from 'node:child_process';
import {parseArgs} from 'node:util';
import {createFinePlaceLoader,placeNumbers,placeMonths,placeSampleScores} from '../../hosting/sites/fine-place.mjs';
import {createNamedPlaceLoader,namedPlaceNumbers,namedPlaceMonths} from '../../hosting/sites/named-place.mjs';
import {fitNamedSunshine,sunshineCoordinateKey} from './named_correction.mjs';

const {values}=parseArgs({options:{data:{type:'string'},queries:{type:'string'},sunshine:{type:'string'},output:{type:'string'}}});
for(const name of ['data','queries','sunshine','output'])assert(values[name],`Missing --${name}`);
await fs.mkdir(values.output,{recursive:true});
const nativeRoot='v6-fine-b1-20261018/replay',tileRoot='v6-fine-b1-20261018/temperature';
const manifest=JSON.parse(await fs.readFile(path.join(values.data,nativeRoot,'manifest.json')));
const configuration={nativeRoot,tileRoot,generation:manifest.generation,eveningModel:'uniform',status:'sunshine-fitter-proof'};
const pointer=path.join(values.output,'fine-pointer.json');await fs.writeFile(pointer,JSON.stringify(configuration));
const read=async key=>{try{return await fs.readFile(path.join(values.data,key));}catch(error){if(error.code==='ENOENT')return null;throw error;}};
const loader=createFinePlaceLoader(key=>key==='v6/fine/current.json'?Promise.resolve(Buffer.from(JSON.stringify(configuration))):read(key));
const source=JSON.parse(await fs.readFile(values.sunshine,'utf8')),targets=new Map(source.records.map(record=>[sunshineCoordinateKey(record.latitude,record.longitude),record.months]));
const queries=JSON.parse(await fs.readFile(values.queries,'utf8')),periods=[...Array.from({length:12},(_,index)=>({months:[index+1]})),{kind:'year'},{months:[11,12,1,2,3]},...[[12,1,2],[3,4,5],[6,7,8],[9,10,11]].map(months=>({months}))];
const packets=new Map(),rows=[],unsupportedHeating=[];
let maximumSunshineError=0,maximumDniError=0,maximumSupportedDniError=0,maximumDniScale=0,maximumUnappliedHeatingC=0,maximumUnappliedScoredHeatingC=0,sunshineMonths=0,dniHours=0,unchangedPlaces=0,packedNumbers=0,packedFacts=0;
const compare=(actual,expected,tolerance,message)=>assert(Math.abs(actual-expected)<=tolerance,`${message}: ${actual} != ${expected}`);
for(const query of queries){
  const latitude=query.lat??query.latitude,longitude=query.lon??query.longitude,packet=await loader.load(latitude,longitude);
  assert(packet,`Missing native replay at ${query.slug}`);
  const months=targets.get(sunshineCoordinateKey(latitude,longitude)),eligible=months?.some(month=>month!==null);
  const corrected=eligible?{...packet,sunshine:fitNamedSunshine(packet,months)}:packet;
  const originalFacts=placeMonths(packet),correctedFacts=placeMonths(corrected),monthRows=[];
  for(let month=0;month<12;month++){
    for(const field of ['highC','feelsAt21C','dewPointC','wetDaysPercent','windKmh'])assert.equal(correctedFacts[month][field],originalFacts[month][field],`${query.slug}/${month+1}/${field} changed`);
    const target=eligible?months[month]:null;
    if(!target)continue;
    const result=placeNumbers(corrected,{months:[month+1]}),sunshineError=Math.abs(result.typical.sunnyShare-target.sunnyShare);
    maximumSunshineError=Math.max(maximumSunshineError,sunshineError);compare(result.typical.sunnyShare,target.sunnyShare,1e-12,`${query.slug}/${month+1} sampled sunny share`);
    compare(correctedFacts[month].sunnyPercent,100*target.sunnyShare,1e-10,`${query.slug}/${month+1} sunny fact`);sunshineMonths++;
    const indices=packet.replay.selectedIndices.filter(index=>Number(packet.replay.dates[index].slice(5,7))===month+1),profile=[];
    const nativeNumbers=placeNumbers(packet,{months:[month+1]});
    const expectedSun14=nativeNumbers.typical.feelsInSunAt14C+indices.reduce((sum,index)=>sum+(packet.replay.sunC[index*12+4]-packet.replay.shadeC[index*12+4])*(corrected.sunshine[month].dniScale[4]-1)/indices.length,0);
    compare(result.typical.feelsInSunAt14C,expectedSun14,1e-10,`${query.slug}/${month+1} shared daytime solar heating`);
    for(const field of ['feelsInShadeAt15C','feelsInShadeAt21C','dewPointC'])assert.equal(result.typical[field],nativeNumbers.typical[field],`${query.slug}/${month+1}/typical ${field} changed`);
    for(let hour=0;hour<12;hour++){
      const scale=corrected.sunshine[month].dniScale[hour];maximumDniScale=Math.max(maximumDniScale,scale);
      let native=0,adjusted=0,unappliedHeatingC=0,unappliedScoredHeatingC=0;
      for(const index of indices){
        const at=index*12+hour,air=packet.replay.airC[at],dew=Math.min(air,packet.replay.dewC[at]);
        const vapour=6.105*Math.exp(17.27*dew/(237.7+dew));
        const wind=(air+.33*vapour-4-packet.replay.shadeC[at])/.7;
        const heating=packet.replay.sunC[at]-packet.replay.shadeC[at];
        native+=Math.max(0,heating*(wind+10)/.105)/indices.length;
        adjusted+=Math.max(0,heating*scale*(wind+10)/.105)/indices.length;
        const targetHeating=.105*target.hourlyDni[hour]/(wind+10);
        unappliedHeatingC=Math.max(unappliedHeatingC,targetHeating);
        if(hour<Math.max(1,packet.replay.daylightHours[index]))unappliedScoredHeatingC=Math.max(unappliedScoredHeatingC,targetHeating);
      }
      const difference=Math.abs(adjusted-target.hourlyDni[hour]);maximumDniError=Math.max(maximumDniError,difference);dniHours++;
      if(native===0){
        assert.equal(scale,1,`${query.slug}/${month+1}/${hour+10} unsupported heating must remain native`);
        assert.equal(adjusted,0,`${query.slug}/${month+1}/${hour+10} multiplication must preserve zero radiation`);
        if(target.hourlyDni[hour]>0){
          maximumUnappliedHeatingC=Math.max(maximumUnappliedHeatingC,unappliedHeatingC);maximumUnappliedScoredHeatingC=Math.max(maximumUnappliedScoredHeatingC,unappliedScoredHeatingC);
          unsupportedHeating.push({place:query.slug,month:month+1,hour:hour+10,nativeDni:native,targetDni:target.hourlyDni[hour],scale,scoredDays:indices.filter(index=>hour<Math.max(1,packet.replay.daylightHours[index])).length,sampledDays:indices.length,unappliedHeatingC,unappliedScoredHeatingC});
        }
      } else {maximumSupportedDniError=Math.max(maximumSupportedDniError,difference);compare(adjusted,target.hourlyDni[hour],1e-3,`${query.slug}/${month+1}/${hour+10} fitted DNI`);}
      profile.push({hour:hour+10,nativeDni:native,targetDni:target.hourlyDni[hour],correctedDni:adjusted});
    }
    monthRows.push({month:month+1,sunnyShare:result.typical.sunnyShare,targetShare:target.sunnyShare,profile});
  }
  if(!eligible){assert.deepEqual(placeSampleScores(corrected),placeSampleScores(packet));assert.deepEqual(correctedFacts,originalFacts);unchangedPlaces++;}
  packets.set(query.slug,corrected);rows.push({place:query.slug,covered:!!eligible,months:monthRows});
}
const build=path.join(values.output,'packed');
execFileSync(process.execPath,['research/fine/build_named_places.mjs','--data',values.data,'--pointer',pointer,'--queries',values.queries,'--sunshine',values.sunshine,'--output',build,'--workers','2'],{stdio:'inherit'});
const buildManifest=JSON.parse(await fs.readFile(path.join(build,'BUILD.json')));
const namedLoader=createNamedPlaceLoader(async key=>{
  if(key==='v6/named/current.json')return Buffer.from(JSON.stringify({generation:buildManifest.generation,root:'packed',factsRows:buildManifest.factsRows}));
  if(key.startsWith('packed/'))return fs.readFile(path.join(build,key.slice(7)));
  return null;
});
for(const query of queries){
  const latitude=query.lat??query.latitude,longitude=query.lon??query.longitude,packet=packets.get(query.slug),row=await namedLoader.load(latitude,longitude,{kind:'year'});
  assert(row,`Packed row absent at ${query.slug}`);
  const expectedDays=placeSampleScores(packet).flat().map(Math.floor),actualDays=namedPlaceNumbers(row,{kind:'year'}).dayScores;
  assert.deepEqual(actualDays,expectedDays,`${query.slug} packed byte scores differ`);
  for(const period of periods){
    const actual=namedPlaceNumbers(row,period),expected=placeNumbers(packet,period);
    for(const field of ['score','meanScore','band','niceDayCount','perfectDayCount','awfulDayCount'])assert.equal(actual[field],expected[field],`${query.slug}/${JSON.stringify(period)}/${field}`);
    packedNumbers++;
  }
  const actualMonths=namedPlaceMonths(row),expectedMonths=placeMonths(packet);
  for(let month=0;month<12;month++)for(const field of ['highC','feelsAt21C','dewPointC','wetDaysPercent','sunnyPercent','windKmh']){
    compare(actualMonths[month][field],expectedMonths[month][field],1e-4,`${query.slug}/${month+1}/packed ${field}`);packedFacts++;
  }
}
const report={writtenBy:'AI, GPT-6.1 Sol, 2026-10-18',passes:true,places:queries.length,coveredPlaces:queries.length-unchangedPlaces,unchangedPlaces,sunshineMonths,dniHours,maximumSunshineError,maximumDniError,maximumSupportedDniError,maximumDniScale,maximumUnappliedHeatingC,maximumUnappliedScoredHeatingC,packedNumbers,packedFacts,unsupportedHeating,definition:'Actual independent satellite inputs and compact native replay. Sunshine offset checked through shared placeNumbers. DNI recovered from native felt-sun/felt-shade and inverted native wind; positive-support mean scaled DNI must equal target to0.001W/m2. Universal multiplicative policy preserves zero native direct-sun heating, including night and native cloud; unfitted satellite tails and their thermal implication are reported, not called exact fits. Builder bytes/facts read back through namedPlaceNumbers; floor packing preserves displayed scores and counts.',rows};
await fs.writeFile(path.join(values.output,'proof.json'),JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify({...report,rows:undefined}));
