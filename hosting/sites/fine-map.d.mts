// Written by AI on 2026.10.19 by model GPT-6.1 Sol.
import type {Period,PointVerdict} from '../../src/engine/index';
export const FINE_MAP_TILE_SIZE:number;
export const FINE_MAP_THRESHOLDS:readonly number[];
export type FineMapConfiguration=Readonly<{generation:string;root:string;thresholds:readonly number[];transportEncoding?:'br';tileSize?:number;resolutionArcSeconds?:number;attribution?:string;climateGeneration?:string;fine?:Readonly<{nativeRoot:string;tileRoot:string;generation:string;eveningModel:string;status:string;attribution?:string}>}>;
export type FineMapTile=Readonly<{counts:Uint8Array;row:number;column:number;size:number;halo:number;generation:string;version:number}>;
export function decodeFineMapTile(bytes:Uint8Array,configuration:FineMapConfiguration):FineMapTile;
export function fineMapTileAddress(latitude:number,longitude:number):{row:number;column:number;tileRow:number;tileColumn:number;index:number};
export function fineMapNumbers(tile:FineMapTile,pixelIndex:number,period:Period):Pick<PointVerdict,'meanScore'|'band'|'niceDayCount'|'perfectDayCount'|'awfulDayCount'|'niceDayShare'|'perfectDayShare'|'awfulDayShare'|'totalDays'|'countedDays'|'sampleDays'|'countBasis'|'resolutionArcSeconds'>&{score:number;status:string}|null;
export function fineMapPointNumbers(tile:FineMapTile,latitude:number,longitude:number,period:Period):ReturnType<typeof fineMapNumbers>;
export function createFineMapLoader(readBytes:(key:string)=>Promise<Uint8Array|null>):{configuration():Promise<FineMapConfiguration|null>;tile(row:number,column:number):Promise<FineMapTile|null>;numbers(latitude:number,longitude:number,period:Period):Promise<ReturnType<typeof fineMapNumbers>>};
