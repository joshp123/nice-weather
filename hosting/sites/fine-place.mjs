// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Shared exact-pixel climate replay for the Worker, app, and offline posters.
import {decodeFineReplay,fineGeneration,targetFineZone} from './fine-replay.mjs';
const MONTH_DAYS = [31,28,31,30,31,30,31,31,30,31,30,31];
const THRESHOLDS = [0,25,40,55,70,85,100];
const BANDS = ['Awful','Poor','Fair','Nice','Beautiful','Perfect'];
export const DEFAULT_FINE_TASTE = {warmShift:0,heatShift:0,eveningShift:0,humidityShift:0,windShift:0,rainScale:1};
const COLD=[[9,0],[11,3],[13,10],[15,22],[17,40],[19,62],[21,85],[23,100]];
const HEAT=[[28,100],[31,85],[34,60],[37,30],[40,10],[43,0]];
const EVENING=[[4,0],[8,30],[12,65],[16,90],[19,100]];
const RAIN=[[0,1],[.5,.95],[1,.88],[2,.74],[4,.5],[8,.22],[15,.05]];
const WIND=[[15,1],[20,.97],[25,.92],[30,.85],[40,.65],[50,.4],[65,.15]];
const HUMIDITY=[[16,1],[18,.95],[20,.85],[22,.65],[24,.4],[26,.2],[28,.1]];
const SUN=[[0,.75],[.3,.85],[.6,.95],[.8,1]];
const canonical = value => Math.round(value*10000)/10000;
const wrap = (value,width) => (value%width+width)%width;
function curve(points,value) {
  if(value<=points[0][0])return points[0][1];
  for(let i=1;i<points.length;i++)if(value<=points[i][0])return points[i-1][1]+(points[i][1]-points[i-1][1])*(value-points[i-1][0])/(points[i][0]-points[i-1][0]);
  return points.at(-1)[1];
}
const vapour = temperature => 6.105*Math.exp(17.27*temperature/(237.7+temperature));
function orderedOffset(air,high,low,highOffset,lowOffset) {
  const range=high-low;
  return range>0&&range+highOffset-lowOffset>0?lowOffset+(highOffset-lowOffset)*(air-low)/range:(highOffset+lowOffset)/2;
}
function eveningModelFor(current,manifest) {
  if(current.eveningModel&&manifest.eveningModel&&current.eveningModel!==manifest.eveningModel)throw new Error('Fine evening models differ');
  const model=current.eveningModel??manifest.eveningModel??'ordered';
  if(!['ordered','uniform','native'].includes(model))throw new Error('Fine evening model is invalid');
  return model;
}
export function finePixel(latitude,longitude) {
  if(!Number.isFinite(latitude)||!Number.isFinite(longitude)||latitude<-90||latitude>90)throw new RangeError('Invalid fine-pixel coordinates');
  return {row:Math.min(21599,Math.floor((90-latitude)*120)),column:Math.floor(wrap(longitude+180,360)*120)};
}
export function nativeFineIndex(latitude,longitude) {
  return Math.max(0,Math.min(720,Math.floor((90-latitude)*4+.5)))*1440+wrap(Math.floor((longitude+180)*4+.5),1440);
}
function monthsForPeriod(period) {
  if(period.months)return period.months;
  if(period.kind==='month')return [period.month];
  if(period.kind==='season')return {'Dec-Feb':[12,1,2],'Mar-May':[3,4,5],'Jun-Aug':[6,7,8],'Sep-Nov':[9,10,11]}[period.season];
  if(period.kind==='months')return Array.from({length:(period.endMonth-period.startMonth+12)%12+1},(_,i)=>(period.startMonth-1+i)%12+1);
  if(period.kind==='year')return Array.from({length:12},(_,i)=>i+1);
  throw new Error('Fine-place replay supports typical-year periods; native forecast replay is not available');
}
function correctedDay(packet,index) {
  const {replay,corrections}=packet,month=Number(replay.dates[index].slice(5,7));
  const [highOffset,lowOffset]=corrections[month-1],high=replay.highC[index],low=replay.lowC[index],range=high-low;
  const ordered=range>0&&range+highOffset-lowOffset>0;
  const sun=[],shade=[],air=[];let cappedDewSum=0,nativeDewSum=0;
  for(let hour=0;hour<12;hour++) {
    const at=index*12+hour,nativeAir=replay.airC[at],dew=replay.dewC[at];
    let delta=ordered?lowOffset+(highOffset-lowOffset)*(nativeAir-low)/range:(highOffset+lowOffset)/2;
    if(hour===11&&packet.eveningModel==='uniform')delta=(highOffset+lowOffset)/2;
    if(hour===11&&packet.eveningModel==='native')delta=0;
    air.push(nativeAir+delta);cappedDewSum+=Math.min(dew,nativeAir+delta);nativeDewSum+=dew;
    const feltDelta=delta+.33*(vapour(Math.min(dew,nativeAir+delta))-vapour(Math.min(dew,nativeAir)));
    sun.push(replay.sunC[at]+feltDelta);shade.push(replay.shadeC[at]+feltDelta);
  }
  return {sun,shade,air,dew:cappedDewSum/12,nativeDew:nativeDewSum/12,rain:replay.rainMm[index],wind:replay.windKmh[index],sunny:replay.sunnyHours[index],daylight:Math.max(1,replay.daylightHours[index])};
}
function judgeDay(day,taste) {
  let daytime=0;
  for(let hour=0;hour<day.daylight;hour++)daytime+=Math.min(curve(COLD,day.sun[hour]-taste.warmShift),curve(HEAT,day.shade[hour]+taste.heatShift));
  const evening=Math.min(curve(EVENING,day.shade[11]-taste.eveningShift),curve(HEAT,day.shade[11]+taste.heatShift));
  const warmth=(daytime+2*evening)/(day.daylight+2),rainFactor=curve(RAIN,day.rain*taste.rainScale),windFactor=curve(WIND,day.wind-taste.windShift),humidityFactor=curve(HUMIDITY,day.dew-taste.humidityShift),sunFactor=curve(SUN,day.sunny/day.daylight);
  return {score:canonical(warmth*rainFactor*windFactor*humidityFactor*sunFactor),warmth,evening,rainFactor,windFactor,humidityFactor,sunFactor};
}
function typicalScore(shares) {
  for(let band=5;band>=0;band--)if(shares[band]>=.5-1e-9)return canonical(band===5?Math.max(85,85+(shares[5]-.5)*30):Math.max(THRESHOLDS[band],THRESHOLDS[band]+(THRESHOLDS[band+1]-THRESHOLDS[band])*(shares[band]-.5)/(shares[band]-shares[band+1])));
  return 0;
}
/** Every named climate number is derived here, using the exact pixel and own native point. */
export function placeNumbers(packet,period,taste=DEFAULT_FINE_TASTE) {
  if(packet.source==='forecast')return forecastNumbers(packet,taste);
  const months=monthsForPeriod(period),totalDays=months.reduce((sum,month)=>sum+MONTH_DAYS[month-1],0),shares=THRESHOLDS.map(()=>0);
  const parts={warmth:0,evening:0,rainFactor:0,windFactor:0,humidityFactor:0,sunFactor:0};
  const typical={feelsInSunAt14C:0,feelsInShadeAt15C:0,feelsInShadeAt21C:0,dewPointC:0,rainMmPerDay:0,wetDayShare:0,windKmh:0,sunnyShare:0,daylightHours:0};
  let sampleDays=0;
  for(const month of months) {
    const indices=packet.replay.selectedIndices.filter(index=>Number(packet.replay.dates[index].slice(5,7))===month);
    if(!indices.length)throw new Error(`Fine replay has no representative days for month ${month}`);
    const weight=MONTH_DAYS[month-1]/totalDays/indices.length;
    for(const index of indices) {
      const day=correctedDay(packet,index),judgement=judgeDay(day,taste);sampleDays++;
      for(let threshold=0;threshold<THRESHOLDS.length;threshold++)if(judgement.score>=THRESHOLDS[threshold])shares[threshold]+=weight;
      for(const name of Object.keys(parts))parts[name]+=judgement[name]*weight;
      const values={feelsInSunAt14C:day.sun[4],feelsInShadeAt15C:day.shade[5],feelsInShadeAt21C:day.shade[11],dewPointC:day.dew,rainMmPerDay:day.rain,wetDayShare:day.rain>=1?1:0,windKmh:day.wind,sunnyShare:day.sunny/day.daylight,daylightHours:day.daylight};
      for(const name of Object.keys(typical))typical[name]+=values[name]*weight;
    }
  }
  const score=typicalScore(shares),niceDayShare=shares[3],perfectDayShare=shares[5],awfulDayShare=Math.max(0,1-shares[1]),count=share=>Math.round(share*totalDays*1e9)/1e9;
  return {meanScore:score,score,band:BANDS[Math.min(5,THRESHOLDS.findLastIndex(value=>score>=value))],niceDayShare,perfectDayShare,awfulDayShare,niceDayCount:count(niceDayShare),perfectDayCount:count(perfectDayShare),awfulDayCount:count(awfulDayShare),totalDays,sampleDays,parts,typical,pixel:packet.pixel,resolutionArcSeconds:30,status:packet.status};
}
function forecastNumbers(packet,taste) {
  const totalDays=packet.weights.reduce((sum,weight)=>sum+weight,0),shares=THRESHOLDS.map(()=>0);
  let singleDayScore=0;
  for(let index=0;index<packet.records.length;index++) {
    const record=packet.records[index],level=Math.max(0,Math.min(10,(packet.offsetsC[index]+15)/3)),lower=Math.min(Math.floor(level),9),upperWeight=level-lower;
    const scoreAtShift=shift=> {
      const sun=Array.from({length:6},(_,hour)=>[hour*2,record[hour]/3-30+shift]);
      const shade=Array.from({length:4},(_,hour)=>[hour*3+2,record[hour+6]/3-30+shift]);
      const day={sun:Array.from({length:12},(_,hour)=>curve(sun,hour)),shade:Array.from({length:12},(_,hour)=>curve(shade,hour)),dew:record[10]/3-30,rain:(record[11]/50)**2,wind:record[12]/2,sunny:record[13],daylight:Math.max(1,record[14])};
      const judgement=judgeDay(day,taste);
      return judgement.warmth*judgement.rainFactor*judgement.windFactor*judgement.humidityFactor*judgement.sunFactor;
    };
    const score=canonical((1-upperWeight)*scoreAtShift(-15+lower*3)+upperWeight*scoreAtShift(-15+(lower+1)*3));
    singleDayScore=score;
    for(let threshold=0;threshold<THRESHOLDS.length;threshold++)if(score>=THRESHOLDS[threshold])shares[threshold]+=packet.weights[index]/totalDays;
  }
  const score=totalDays===1?singleDayScore:typicalScore(shares),count=share=>Math.round(share*totalDays*1e9)/1e9;
  return {score,meanScore:score,band:BANDS[Math.min(5,THRESHOLDS.findLastIndex(value=>score>=value))],totalDays,sampleDays:packet.records.length,niceDayShare:shares[3],perfectDayShare:shares[5],awfulDayShare:Math.max(0,1-shares[1]),niceDayCount:count(shares[3]),perfectDayCount:count(shares[5]),awfulDayCount:count(1-shares[1])};
}
export function placeMonths(packet,taste=DEFAULT_FINE_TASTE) {
  if(packet.replay.physicalResponse)return compactPlaceMonths(packet,taste);
  return Array.from({length:12},(_,monthIndex)=> {
    const indices=packet.replay.dates.flatMap((date,index)=>Number(date.slice(5,7))===monthIndex+1?[index]:[]);
    if(!indices.length)throw new Error(`Fine replay has no full-period days for month ${monthIndex+1}`);
    const totals={highC:0,feelsAt21C:0,dewPointC:0,wetDaysPercent:0,sunnyPercent:0,windKmh:0};
    for(const index of indices) {
      const day=correctedDay(packet,index);
      totals.highC+=Math.max(...day.air.slice(0,11));totals.feelsAt21C+=day.shade[11];totals.dewPointC+=day.nativeDew;totals.wetDaysPercent+=(day.rain>=1?100:0);totals.sunnyPercent+=100*day.sunny/day.daylight;totals.windKmh+=day.wind;
    }
    for(const key of Object.keys(totals))totals[key]/=indices.length;
    const judgement=placeNumbers(packet,{kind:'month',month:monthIndex+1},taste);
    return {month:monthIndex+1,...totals,score:judgement.meanScore,niceDayShare:judgement.niceDayShare};
  });
}
function compactPlaceMonths(packet,taste) {
  const {dates,values,monthly}=packet.replay.physicalResponse;
  return Array.from({length:12},(_,monthIndex)=> {
    const [highOffset,lowOffset]=packet.corrections[monthIndex];
    let count=0,highC=0,feelsAt21C=0;
    for(let index=0;index<dates.length;index++)if(Number(dates[index].slice(5,7))===monthIndex+1) {
      const at=index*6,high=values[at],low=values[at+1],dayMax=values[at+2],air21=values[at+3],dew21=values[at+4],shade21=values[at+5];
      highC+=dayMax+orderedOffset(dayMax,high,low,highOffset,lowOffset);
      const eveningOffset=packet.eveningModel==='native'?0:packet.eveningModel==='uniform'?(highOffset+lowOffset)/2:orderedOffset(air21,high,low,highOffset,lowOffset);
      feelsAt21C+=shade21+eveningOffset+.33*(vapour(Math.min(dew21,air21+eveningOffset))-vapour(Math.min(dew21,air21)));count++;
    }
    if(!count)throw new Error(`Fine replay has no full-period days for month ${monthIndex+1}`);
    const judgement=placeNumbers(packet,{kind:'month',month:monthIndex+1},taste);
    return {month:monthIndex+1,highC:highC/count,feelsAt21C:feelsAt21C/count,dewPointC:monthly[monthIndex*4],wetDaysPercent:monthly[monthIndex*4+1],sunnyPercent:monthly[monthIndex*4+2],windKmh:monthly[monthIndex*4+3],score:judgement.meanScore,niceDayShare:judgement.niceDayShare};
  });
}
async function uncompressed(bytes) {
  if(bytes[0]!==31||bytes[1]!==139)return bytes;
  return new Uint8Array(await new Response(new Blob([bytes]).stream().pipeThrough(new DecompressionStream('gzip'))).arrayBuffer());
}
const parseJson=async bytes=>JSON.parse(new TextDecoder().decode(await uncompressed(bytes)));
/** readBytes(key) returns Uint8Array or null. An absent pointer preserves the current product. */
export function createFinePlaceLoader(readBytes) {
  const requests=new Map();
  const cached=(key,decode)=> {
    if(!requests.has(key))requests.set(key,readBytes(key).then(bytes=>bytes===null?null:decode(bytes)).catch(error=>{requests.delete(key);throw error;}));
    return requests.get(key);
  };
  const pointer=()=>cached('v6/fine/current.json',parseJson);
  const json=key=>cached(key,parseJson);
  const loader={
    enabled:async()=>Boolean(await pointer()),
    configuration:async()=>await pointer(),
    async load(latitude,longitude) {
      const current=await pointer();if(!current)return null;
      const pixel=finePixel(latitude,longitude),fineIndex=nativeFineIndex(90-(pixel.row+.5)/120,-180+(pixel.column+.5)/120),manifest=await json(`${current.nativeRoot}/manifest.json`);
      if(!manifest)throw new Error('Fine replay manifest is missing');
      const eveningModel=eveningModelFor(current,manifest);
      const compact=manifest.format==='nwfp-v1';
      if(compact&&(!current.eveningModel||!manifest.eveningModel))throw new Error('Fine compact replay requires a declared evening model');
      if(manifest.nativeFineIndices&&!manifest.nativeFineIndices.includes(fineIndex))return null;
      const expectedGeneration=manifest.generation??current.generation;
      if(!expectedGeneration)throw new Error('Fine replay generation is missing');
      let replay;
      if(compact) {
        const zoneBytes=await cached(`${current.nativeRoot}/zones/${String(Math.floor(pixel.row/256)).padStart(3,'0')}/${String(Math.floor(pixel.column/256)).padStart(3,'0')}.nwz.gz`,uncompressed);
        if(!zoneBytes)return null;
        const zoneIndex=targetFineZone(zoneBytes,pixel,expectedGeneration);
        if(zoneIndex===null)return null;
        if(!manifest.timezoneNames[zoneIndex])throw new Error('Fine target timezone is absent from the replay manifest');
        const bytes=await cached(`${current.nativeRoot}/native/${fineIndex}/${zoneIndex}.nwfp.gz`,uncompressed);
        if(!bytes)return null;
        replay=decodeFineReplay(bytes,manifest,fineIndex,zoneIndex);
      } else replay=await json(`${current.nativeRoot}/native/${fineIndex}.json.gz`);
      if(!replay)return null;
      if(replay.fineIndex!==fineIndex)throw new Error('Fine replay native-point address mismatch');
      for(const generation of [manifest.generation,replay.generation,current.generation])if(generation&&generation!==expectedGeneration)throw new Error('Fine replay generations differ');
      const tileRow=Math.floor(pixel.row/256),tileColumn=Math.floor(pixel.column/256),index=(pixel.row%256)*256+pixel.column%256;
      const corrections=await Promise.all(Array.from({length:12},async(_,monthIndex)=> {
        const key=`${current.tileRoot}/tiles/${String(monthIndex+1).padStart(2,'0')}/${String(tileRow).padStart(3,'0')}/${String(tileColumn).padStart(3,'0')}.nwf.gz`;
        const bytes=await cached(key,uncompressed);if(!bytes)return null;
        if(bytes.byteLength!==327712)throw new Error('Fine correction tile length mismatch');
        const view=new DataView(bytes.buffer,bytes.byteOffset,bytes.byteLength);
        if(view.getUint32(0,true)!==0x5446574e||view.getUint16(4,true)!==1||view.getUint8(7)!==0||view.getUint8(6)!==monthIndex+1||view.getUint16(8,true)!==tileRow||view.getUint16(10,true)!==tileColumn)throw new Error('Fine correction tile address mismatch');
        const generation=fineGeneration(bytes.subarray(16,32));
        if(generation!==expectedGeneration)throw new Error('Fine correction tile generation mismatch');
        const support=bytes[32+256*256*4+index];
        if(!(support&8))return null;
        const high=view.getInt16(32+index*4,true),low=view.getInt16(34+index*4,true);
        if(high===-32768||low===-32768)return null;
        const clock= replay.physicalResponse?.clockOffsets;
        return [high/10+(clock?.[monthIndex*2]??0),low/10+(clock?.[monthIndex*2+1]??0)];
      }));
      if(corrections.some(value=>value===null))return null;
      return {replay,corrections,pixel,status:current.status,eveningModel};
    }
  };
  return loader;
}
