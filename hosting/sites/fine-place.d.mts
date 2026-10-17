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
export type FinePacket = Readonly<{replay:FineReplay;corrections:readonly (readonly [number,number])[];pixel:FinePixel;status:string;eveningModel?:FineEveningModel}>;
export type FineVerdict = PointVerdict & Readonly<{score:number;pixel:FinePixel;resolutionArcSeconds:30;status:string}>;
export type FineMonth = Readonly<{month:number;highC:number;feelsAt21C:number;dewPointC:number;wetDaysPercent:number;sunnyPercent:number;windKmh:number;score:number;niceDayShare:number}>;
export const DEFAULT_FINE_TASTE: Taste;
export type ForecastPlacePacket = Readonly<{source:'forecast';records:readonly (readonly number[]|Uint8Array)[];offsetsC:readonly number[];weights:readonly number[]}>;
export type ForecastPlaceVerdict = Readonly<{score:number;meanScore:number;band:string;totalDays:number;sampleDays:number;niceDayShare:number;perfectDayShare:number;awfulDayShare:number;niceDayCount:number;perfectDayCount:number;awfulDayCount:number}>;
export function finePixel(latitude:number,longitude:number):FinePixel;
export function nativeFineIndex(latitude:number,longitude:number):number;
export function placeNumbers(packet:FinePacket,period:Period,taste?:Taste):FineVerdict;
export function placeNumbers(packet:ForecastPlacePacket,period?:Period,taste?:Taste):ForecastPlaceVerdict;
export function placeMonths(packet:FinePacket,taste?:Taste):FineMonth[];
export function createFinePlaceLoader(readBytes:(key:string)=>Promise<Uint8Array|null>):{
 enabled():Promise<boolean>;
 configuration():Promise<Readonly<{nativeRoot:string;tileRoot:string;generation:string;status:string;eveningModel?:FineEveningModel;attribution?:string}>|null>;
 load(latitude:number,longitude:number):Promise<FinePacket|null>;
};
