// Written by AI (GPT-6.1 Sol), 2026-10-18.
// Default-taste named places: compact sampled scores and separately loaded facts.
import {typicalScore} from './fine-place.mjs';

const MONTH_DAYS=[31,28,31,30,31,30,31,31,30,31,30,31];
const THRESHOLDS=[0,25,40,55,70,85,100];
const BANDS=['Awful','Poor','Fair','Nice','Beautiful','Perfect'];
export const NAMED_PART_FIELDS=['warmth','evening','rainFactor','windFactor','humidityFactor','sunFactor'];
export const NAMED_TYPICAL_FIELDS=['feelsInSunAt14C','feelsInShadeAt15C','feelsInShadeAt21C','dewPointC','rainMmPerDay','wetDayShare','windKmh','sunnyShare','daylightHours'];
export const NAMED_PHYSICAL_FIELDS=['highC','feelsAt21C','dewPointC','wetDaysPercent','sunnyPercent','windKmh'];
const forecastPeriod=period=>period?.kind==='forecast'||period?.kind==='forecast-day';
const coordinate=value=>Math.round(value*1e5);
const band=score=>BANDS[Math.min(5,THRESHOLDS.findLastIndex(value=>score>=value))];
const zeroObject=fields=>Object.fromEntries(fields.map(field=>[field,0]));

export function namedPeriodMonths(period) {
  if(period.months)return period.months;
  if(period.kind==='month')return [period.month];
  if(period.kind==='season')return {'Dec-Feb':[12,1,2],'Mar-May':[3,4,5],'Jun-Aug':[6,7,8],'Sep-Nov':[9,10,11]}[period.season];
  if(period.kind==='months')return Array.from({length:(period.endMonth-period.startMonth+12)%12+1},(_,i)=>(period.startMonth-1+i)%12+1);
  if(period.kind==='year')return Array.from({length:12},(_,i)=>i+1);
  throw Error('Unknown named-place period');
}

export async function namedUncompressed(bytes) {
  if(bytes[0]!==31||bytes[1]!==139)return bytes;
  return new Uint8Array(await new Response(new Blob([bytes]).stream().pipeThrough(new DecompressionStream('gzip'))).arrayBuffer());
}
const jsonBytes=async bytes=>JSON.parse(new TextDecoder().decode(await namedUncompressed(bytes)));

export function decodeNamedTable(bytes) {
  const view=new DataView(bytes.buffer,bytes.byteOffset,bytes.byteLength);
  if(new TextDecoder().decode(bytes.subarray(0,4))!=='NWNP'||view.getUint16(4,true)!==1)throw Error('Invalid named-place table');
  const kind=view.getUint8(6),samples=view.getUint8(7),count=view.getUint32(8,true),rowStart=view.getUint32(12,true),fields=view.getUint16(16,true);
  const expected=kind===1?32+count*(8+96):kind===2?32+count*(8+15*15):kind===3?32+count*12*21*4:0;
  if(!expected||bytes.byteLength!==expected||kind===1&&samples!==96||kind===2&&samples!==15||kind===3&&fields!==21)throw Error('Named-place table size or fields differ');
  return {bytes,view,kind,samples,count,rowStart,fields};
}

function tableIndex(table,latitude,longitude) {
  if(!table||!Number.isFinite(latitude)||!Number.isFinite(longitude))return -1;
  const lat=coordinate(latitude),lon=coordinate(longitude);
  let lower=0,upper=table.count;
  while(lower<upper) {
    const middle=(lower+upper)>>>1,at=32+middle*8,a=table.view.getInt32(at,true),b=table.view.getInt32(at+4,true);
    if(a<lat||a===lat&&b<lon)lower=middle+1;else upper=middle;
  }
  return lower<table.count&&table.view.getInt32(32+lower*8,true)===lat&&table.view.getInt32(36+lower*8,true)===lon?lower:-1;
}

function rowFromTables(climate,forecast,latitude,longitude,forecastRun) {
  const climateIndex=tableIndex(climate,latitude,longitude),forecastIndex=tableIndex(forecast,latitude,longitude);
  if(climateIndex<0&&forecastIndex<0)return null;
  return {latitude,longitude,climate,climateIndex,forecast,forecastIndex,forecastRun};
}

function monthlyFacts(table,index) {
  if(table.kind!==3||index<table.rowStart||index>=table.rowStart+table.count)throw Error('Named-place facts address differs');
  return Array.from({length:12},(_,month)=>Array.from({length:21},(_,field)=>table.view.getFloat32(32+4*((month*21+field)*table.count+index-table.rowStart),true)));
}

function climateNumbers(row,period) {
  if(!row?.climate||row.climateIndex<0)return null;
  const months=namedPeriodMonths(period),totalDays=months.reduce((sum,month)=>sum+MONTH_DAYS[month-1],0),shares=THRESHOLDS.map(()=>0);
  const {climate:table,climateIndex:index}=row,parts=zeroObject(NAMED_PART_FIELDS),typical=zeroObject(NAMED_TYPICAL_FIELDS);
  for(const month of months) {
    const weight=MONTH_DAYS[month-1]/totalDays/8;
    for(let sample=0;sample<8;sample++) {
      const score=table.bytes[32+8*table.count+((month-1)*8+sample)*table.count+index];
      if(score===255)return null;
      for(let threshold=0;threshold<THRESHOLDS.length;threshold++)if(score>=THRESHOLDS[threshold])shares[threshold]+=weight;
    }
    const facts=row.months?.[month-1];
    if(facts) {
      const monthWeight=MONTH_DAYS[month-1]/totalDays;
      for(let field=0;field<6;field++)parts[NAMED_PART_FIELDS[field]]+=facts[field]*monthWeight;
      for(let field=0;field<9;field++)typical[NAMED_TYPICAL_FIELDS[field]]+=facts[6+field]*monthWeight;
    }
  }
  const score=typicalScore(shares),count=share=>Math.round(share*totalDays*1e9)/1e9;
  return {score,meanScore:score,band:band(score),niceDayShare:shares[3],perfectDayShare:shares[5],awfulDayShare:Math.max(0,1-shares[1]),niceDayCount:count(shares[3]),perfectDayCount:count(shares[5]),awfulDayCount:count(Math.max(0,1-shares[1])),totalDays,sampleDays:months.length*8,parts,typical,resolutionArcSeconds:30,status:'named-default',factsAvailable:!!row.months};
}

function forecastNumbers(row,period) {
  if(!row?.forecast||row.forecastIndex<0)return null;
  const table=row.forecast,index=row.forecastIndex,slot=period.kind==='forecast-day'?period.day:period.day??14;
  if(!Number.isInteger(slot)||slot<0||slot>14)return null;
  const base=32+8*table.count+slot*15*table.count,at=base+index*3;
  const packed=table.bytes[at]|table.bytes[at+1]<<8|table.bytes[at+2]<<16;
  if(packed===0xffffff)return null;
  const score=packed/10000,shares=Array.from({length:3},(_,field)=>table.view.getFloat32(base+3*table.count+(field*table.count+index)*4,true));
  const totalDays=slot===14?14:1;
  return {score,meanScore:score,band:band(score),niceDayShare:shares[0],perfectDayShare:shares[1],awfulDayShare:shares[2],niceDayCount:shares[0]*totalDays,perfectDayCount:shares[1]*totalDays,awfulDayCount:shares[2]*totalDays,totalDays,sampleDays:totalDays,parts:zeroObject(NAMED_PART_FIELDS),typical:zeroObject(NAMED_TYPICAL_FIELDS),status:'named-default',factsAvailable:false};
}

/** One default named-place aggregate for app, Worker and offline validation. */
export function namedPlaceNumbers(row,period) {
  return forecastPeriod(period)?forecastNumbers(row,period):climateNumbers(row,period);
}

export function namedPlaceMonths(row) {
  if(!row?.months||row.climateIndex<0)return null;
  if(!climateNumbers(row,{kind:'year'}))return null;
  return row.months.map((values,index)=>{
    const numbers=climateNumbers(row,{kind:'month',month:index+1});
    return {month:index+1,...Object.fromEntries(NAMED_PHYSICAL_FIELDS.map((field,at)=>[field,values[15+at]])),score:numbers.score,niceDayShare:numbers.niceDayShare};
  });
}

/** readBytes returns a Uint8Array or null, including for absent activation. */
export function createNamedPlaceLoader(readBytes) {
  let configurationPromise,expires=0,climatePromise,forecastPromise,forecastRun;
  const factsCache=new Map();
  const configuration=async()=> {
    if(!configurationPromise||Date.now()>=expires) {
      expires=Date.now()+60000;
      configurationPromise=(async()=> {
        const bytes=await readBytes('v6/named/current.json');
        return bytes?jsonBytes(bytes):null;
      })();
    }
    return configurationPromise;
  };
  const climate=async()=> {
    const config=await configuration();if(!config)return null;
    if(!climatePromise||climatePromise.generation!==config.generation) {
      climatePromise={generation:config.generation,promise:(async()=> {
        const bytes=await readBytes(`${config.root}/${config.climate??'climate.bin.gz'}`);
        return bytes?decodeNamedTable(await namedUncompressed(bytes)):null;
      })()};
      factsCache.clear();
    }
    return climatePromise.promise;
  };
  const forecast=async()=> {
    const current=await readBytes('v6/forecast/current.json');if(!current)return null;
    const metadata=await jsonBytes(current),run=metadata.run??metadata.id;
    if(!run)return null;
    if(!forecastPromise||forecastRun!==run) {
      forecastRun=run;forecastPromise=(async()=> {
        const bytes=await readBytes(`v6/forecast/${run}/named.bin.gz`);
        return bytes?decodeNamedTable(await namedUncompressed(bytes)):null;
      })();
    }
    return forecastPromise;
  };
  const load=async(latitude,longitude,period)=> {
    const config=await configuration();if(!config)return null;
    const isForecast=forecastPeriod(period),table=isForecast?await forecast():await climate();
    const row=rowFromTables(isForecast?null:table,isForecast?table:null,latitude,longitude,forecastRun);
    if(!row)return null;
    if(!isForecast) {
      if(!climateNumbers(row,{kind:'year'}))return null;
      const shard=Math.floor(row.climateIndex/(config.factsRows??16384));
      const key=`${config.root}/facts-${String(shard).padStart(3,'0')}.bin.gz`;
      if(!factsCache.has(key)) {
        factsCache.clear();
        factsCache.set(key,(async()=> {
          const bytes=await readBytes(key);return bytes?decodeNamedTable(await namedUncompressed(bytes)):null;
        })());
      }
      const facts=await factsCache.get(key);if(!facts)return null;
      row.months=monthlyFacts(facts,row.climateIndex);
    }
    return row;
  };
  return {configuration,enabled:async()=>!!await configuration(),load};
}

export function createNamedPlaceClient(endpoint='/api/named') {
  let configuration,climate,forecast,forecastRun;
  const factsCache=new Map();
  const read=async key=> {
    const response=await fetch(`/data/${key}`);
    if(response.status===404)return null;
    if(!response.ok)throw Error(`Named-place request failed: ${response.status}`);
    return new Uint8Array(await response.arrayBuffer());
  };
  const readyPromise=(async()=> {
    const response=await fetch(endpoint);if(!response.ok)return;
    const status=await response.json();
    configuration=status.enabled?(status.configuration??status):null;
    if(!configuration)return;
    const bytes=await read(`${configuration.root}/${configuration.climate??'climate.bin.gz'}`);
    if(bytes)climate=decodeNamedTable(await namedUncompressed(bytes));
    const current=await read('v6/forecast/current.json');
    if(current) {
      const meta=await jsonBytes(current);forecastRun=meta.run??meta.id;
      if(forecastRun) {
        const bytes=await read(`v6/forecast/${forecastRun}/named.bin.gz`);
        if(bytes)forecast=decodeNamedTable(await namedUncompressed(bytes));
      }
    }
  })();
  const client={
    ready:()=>readyPromise,
    get enabled(){return !!configuration;},
    find:(latitude,longitude,period)=> {
      if(!configuration)return null;
      const row=rowFromTables(forecastPeriod(period)?null:climate,forecastPeriod(period)?forecast:null,latitude,longitude,forecastRun);
      if(!row)return null;
      const cached=factsCache.get(`${coordinate(latitude)},${coordinate(longitude)}`);if(cached)row.months=cached;
      return row;
    },
    load:async(latitude,longitude,period)=> {
      await readyPromise;
      const row=client.find(latitude,longitude,period);if(!row)return null;
      if(forecastPeriod(period))return row;
      if(!climateNumbers(row,period??{kind:'year'}))return null;
      if(!row.months) {
        const response=await fetch(`${endpoint}/place?lat=${latitude}&lon=${longitude}&kind=climate`);
        if(!response.ok)return null;
        const facts=await response.json();if(!facts?.months)return null;
        row.months=facts.months;
        if(factsCache.size>=512)factsCache.delete(factsCache.keys().next().value);
        factsCache.set(`${coordinate(latitude)},${coordinate(longitude)}`,row.months);
      }
      return row;
    },
  };
  return client;
}
