// Written by AI on 2026.10.19 by model GPT-6.1 Sol.
import {finePixel, typicalScore} from './fine-place.mjs';
import {namedPeriodMonths, namedUncompressed} from './named-place.mjs';
import {SCORE_THRESHOLDS,bandOf} from './score-bands.mjs';

export const FINE_MAP_TILE_SIZE=256;
export const FINE_MAP_THRESHOLDS=SCORE_THRESHOLDS.slice(1);
const MONTH_DAYS=[31,28,31,30,31,30,31,31,30,31,30,31];
const generationOf=bytes=>Array.from(bytes,value=>value.toString(16).padStart(2,'0')).join('');

export function decodeFineMapTile(bytes,configuration) {
  const view=new DataView(bytes.buffer,bytes.byteOffset,bytes.byteLength);
  const version=bytes.length>=32?view.getUint16(4,true):0;
  if(bytes.length<32||new TextDecoder().decode(bytes.subarray(0,4))!=='NWFC'||![1,2,3].includes(version))throw Error('Invalid fine map tile');
  const row=view.getUint16(6,true),column=view.getUint16(8,true),size=view.getUint16(10,true),generation=generationOf(bytes.subarray(12,28));
  if(size!==(version===3?260:FINE_MAP_TILE_SIZE)||view.getUint32(28,true)!==size*size||bytes.length!==32+size*size*36||generation!==configuration.generation||configuration.thresholds?.join(',')!==FINE_MAP_THRESHOLDS.join(','))throw Error('Fine map tile generation, thresholds or dimensions differ');
  const payload=bytes.subarray(32),pixels=size*size;
  let counts=payload;
  if(version>=2) {
    counts=new Uint8Array(payload.length);
    for(let pixel=0;pixel<pixels;pixel++)for(let month=0;month<12;month++) {
      const source=pixel*36+month*3,destination=(month*pixels+pixel)*3;
      counts[destination]=payload[source];counts[destination+1]=payload[source+1];counts[destination+2]=payload[source+2];
    }
  }
  return {counts,row,column,size,halo:version===3?2:0,generation,version};
}

export function fineMapTileAddress(latitude,longitude) {
  const pixel=finePixel(latitude,longitude);
  return {...pixel,tileRow:Math.floor(pixel.row/FINE_MAP_TILE_SIZE),tileColumn:Math.floor(pixel.column/FINE_MAP_TILE_SIZE),index:(pixel.row%FINE_MAP_TILE_SIZE)*FINE_MAP_TILE_SIZE+pixel.column%FINE_MAP_TILE_SIZE};
}

/** These sufficient statistics preserve the sampled-day thresholds for every stretch. */
export function fineMapNumbers(tile,pixelIndex,period) {
  const months=namedPeriodMonths(period),pixels=tile.size*tile.size;
  const totalDays=months.reduce((sum,month)=>sum+MONTH_DAYS[month-1],0),weightedCounts=[0,0,0,0,0,0];
  for(const month of months) {
    const at=((month-1)*pixels+pixelIndex)*3;
    if(tile.counts[at]===255)return null;
    for(let threshold=0;threshold<6;threshold++)weightedCounts[threshold]+=((tile.counts[at+(threshold>>1)]>>(4*(threshold%2)))&15)*MONTH_DAYS[month-1]/8;
  }
  const shares=[1,...weightedCounts.map(count=>count/totalDays)],score=typicalScore(shares);
  const band=bandOf(score);
  return {score,meanScore:score,band,niceDayCount:Math.round(weightedCounts[2]),perfectDayCount:Math.round(weightedCounts[4]),awfulDayCount:Math.round(totalDays-weightedCounts[0]),niceDayShare:shares[3],perfectDayShare:shares[5],awfulDayShare:1-shares[1],totalDays,countedDays:totalDays,sampleDays:months.length*8,countBasis:'sample',resolutionArcSeconds:30,status:'fine-map-default'};
}

const splineWeights=t=>{const u=1-t;return [u*u*u,(3*t*t*t-6*t*t+4),(-3*t*t*t+3*t*t+3*t+1),t*t*t].map(value=>value/6);};

/** The displayed field is a land-normalized cubic B-spline of pixel-centre scores and day shares. */
export function fineMapPointNumbers(tile,latitude,longitude,period) {
  const longitudeWrapped=((longitude+180)%360+360)%360;
  const x=longitudeWrapped*120-.5-tile.column*FINE_MAP_TILE_SIZE+(tile.halo??0);
  const y=(90-latitude)*120-.5-tile.row*FINE_MAP_TILE_SIZE+(tile.halo??0);
  const baseX=Math.floor(x),baseY=Math.floor(y),wx=splineWeights(x-baseX),wy=splineWeights(y-baseY);
  const sums={score:0,niceDayShare:0,perfectDayShare:0,awfulDayShare:0};
  let weight=0,reference;
  for(let row=0;row<4;row++)for(let column=0;column<4;column++) {
    const index=Math.max(0,Math.min(tile.size-1,baseY+row-1))*tile.size+Math.max(0,Math.min(tile.size-1,baseX+column-1));
    const value=fineMapNumbers(tile,index,period);if(!value)continue;
    const contribution=wx[column]*wy[row];weight+=contribution;reference=value;
    for(const key of Object.keys(sums))sums[key]+=value[key]*contribution;
  }
  if(!reference||weight<.00001)return null;
  for(const key of Object.keys(sums))sums[key]/=weight;
  sums.score=Math.round(sums.score*10000)/10000;
  const dayCount=share=>Math.round(Math.round(share*reference.totalDays*1e8)/1e8);
  return {...reference,...sums,meanScore:sums.score,band:bandOf(sums.score),niceDayCount:dayCount(sums.niceDayShare),perfectDayCount:dayCount(sums.perfectDayShare),awfulDayCount:dayCount(sums.awfulDayShare)};
}

/** One immutable release supplies Worker points and the browser's visible tiles. */
export function createFineMapLoader(readBytes) {
  let configurationPromise,expires=0;
  const tiles=new Map();
  const configuration=async()=> {
    if(!configurationPromise||Date.now()>=expires) {
      expires=Date.now()+60000;
      configurationPromise=readBytes('v6/fine-map/current.json').then(async bytes=> {
        if(!bytes)return null;
        const value=JSON.parse(new TextDecoder().decode(await namedUncompressed(bytes)));
        if(value===null)return null;
        if(value.fine&&value.fine.generation!==value.generation)throw Error('Fine map and replay generations differ');
        return value;
      });
    }
    return configurationPromise;
  };
  const tile=async(row,column)=> {
    const config=await configuration();if(!config)return null;
    const key=`${config.root}/counts/${row}-${column}.bin.gz`;
    if(!tiles.has(key)) {
      const promise=readBytes(key).then(async bytes=>bytes?decodeFineMapTile(await namedUncompressed(bytes),config):null).catch(error=>{tiles.delete(key);throw error;});
      tiles.set(key,promise);
      if(tiles.size>4)tiles.delete(tiles.keys().next().value);
    }
    return tiles.get(key);
  };
  return {configuration,tile,async numbers(latitude,longitude,period) {
    const address=fineMapTileAddress(latitude,longitude),value=await tile(address.tileRow,address.tileColumn);
    return value?fineMapPointNumbers(value,latitude,longitude,period):null;
  }};
}
