// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import type { Period, PointVerdict } from '../../src/engine/index';
import type { Taste } from '../../src/engine/taste';
export type FinePixel = Readonly<{row:number;column:number}>;
export type FineReplay = Readonly<{
  fineIndex:number; latitude:number; longitude:number; dates:readonly string[];
  highC:readonly number[];lowC:readonly number[];airC:readonly number[];dewC:readonly number[];
  sunC:readonly number[];shadeC:readonly number[];rainMm:readonly number[];windKmh:readonly number[];
  sunnyHours:readonly number[];daylightHours:readonly number[];dewMeanC:readonly number[];selectedIndices:readonly number[];
}>;
export type FinePacket = Readonly<{replay:FineReplay;corrections:readonly (readonly [number,number])[];pixel:FinePixel;status:string}>;
export type FineVerdict = PointVerdict & Readonly<{score:number;pixel:FinePixel;resolutionArcSeconds:30;status:string}>;
export type FineMonth = Readonly<{month:number;highC:number;feelsAt21C:number;dewPointC:number;wetDaysPercent:number;sunnyPercent:number;windKmh:number;score:number;niceDayShare:number}>;
export const DEFAULT_FINE_TASTE: Taste;
export function finePixel(latitude:number,longitude:number):FinePixel;
export function nativeFineIndex(latitude:number,longitude:number):number;
export function placeNumbers(packet:FinePacket,period:Period,taste?:Taste):FineVerdict;
export function placeMonths(packet:FinePacket,taste?:Taste):FineMonth[];
export function createFinePlaceLoader(readBytes:(key:string)=>Promise<Uint8Array|null>):{
 enabled():Promise<boolean>;
 load(latitude:number,longitude:number):Promise<FinePacket|null>;
};
