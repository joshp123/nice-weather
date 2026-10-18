// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import type { Period, PointVerdict } from '../../src/engine/index';
import type { Taste } from '../../src/engine/taste';
export type FinePixel = Readonly<{row:number;column:number}>;
type NumericArray = readonly number[]|Float32Array;
export type FineReplay = Readonly<{
  fineIndex:number; latitude:number; longitude:number; dates:readonly string[];
  highC:NumericArray;lowC:NumericArray;airC:NumericArray;dewC:NumericArray;
  sunC:NumericArray;shadeC:NumericArray;rainMm:NumericArray;windKmh:NumericArray;
  sunnyHours:NumericArray;daylightHours:NumericArray;dewMeanC?:NumericArray;selectedIndices:readonly number[];
  targetTimezone?:string;selectedDateIndices?:readonly number[];
  physicalResponse?:Readonly<{dates:readonly string[];values:NumericArray;monthly:NumericArray;clockOffsets:NumericArray}>;
}>;
export type FineEveningModel = 'ordered'|'uniform'|'native';
export type FineSunshineMonth = Readonly<{sunnyShareOffset:number;dniScale:readonly number[];sunnyShare:number}>;
export type FinePacket = Readonly<{replay:FineReplay;corrections:readonly (readonly [number,number])[];pixel:FinePixel;status:string;eveningModel?:FineEveningModel;sunshine?:readonly (FineSunshineMonth|null)[]}>;
export type FineVerdict = PointVerdict & Readonly<{score:number;pixel:FinePixel;resolutionArcSeconds:30;status:string}>;
export type FineMonth = Readonly<{month:number;highC:number;feelsAt21C:number;dewPointC:number;wetDaysPercent:number;sunnyPercent:number;windKmh:number;score:number;niceDayShare:number}>;
export const DEFAULT_FINE_TASTE: Taste;
export type ForecastPlacePacket = Readonly<{source:'forecast';records:readonly (readonly number[]|Uint8Array)[];offsetsC:readonly number[];weights:readonly number[]}>;
export type ForecastPlaceVerdict = PointVerdict & Readonly<{score:number}>;
export function finePixel(latitude:number,longitude:number):FinePixel;
export function nativeFineIndex(latitude:number,longitude:number):number;
export function placeNumbers(packet:FinePacket,period:Period,taste?:Taste):FineVerdict;
export function placeNumbers(packet:ForecastPlacePacket,period?:Period,taste?:Taste):ForecastPlaceVerdict;
export function typicalScore(shares:readonly number[]):number;
export function placeSampleScores(packet:FinePacket,taste?:Taste):number[][];
export function placeMonths(packet:FinePacket,taste?:Taste):FineMonth[];
export function createFinePlaceLoader(readBytes:(key:string)=>Promise<Uint8Array|null>):{
 enabled():Promise<boolean>;
 configuration():Promise<Readonly<{nativeRoot:string;tileRoot:string;labelsRoot?:string;generation:string;status:string;eveningModel?:FineEveningModel;attribution?:string}>|null>;
 cacheUsage():Readonly<{correctionBytes:number;timezoneBytes:number;activeTileReads:number;maximumActiveTileReads:number;waitingTileReads:number}>;
 load(latitude:number,longitude:number):Promise<FinePacket|null>;
};

export function encodeFinePlacePacket(packet:FinePacket):Uint8Array;
export function decodeFinePlacePacket(bytes:Uint8Array):FinePacket;
export function createFinePlaceClient(endpoint:string,fetchResponse?:typeof fetch):{
 enabled():Promise<boolean>;
 load(latitude:number,longitude:number):Promise<FinePacket|null>;
};

export function mapPointWeights(latitude:number,longitude:number,valid:(index:number)=>boolean):{index:number;weight:number}[];
export function blendPlaceNumbers(points:readonly Readonly<{numbers:PointVerdict;weight:number}>[]):(PointVerdict & Readonly<{score:number}>)|null;
