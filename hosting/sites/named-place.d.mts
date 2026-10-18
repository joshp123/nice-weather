// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import type {Period, PointVerdict} from '../../src/engine/index';
export type NamedPlaceRow = {latitude:number;longitude:number;climateIndex:number;forecastIndex:number;months?:number[][]};
export function namedPlaceNumbers(row:NamedPlaceRow,period:Period):PointVerdict|null;
export function createNamedPlaceClient(endpoint?:string):{
  ready():Promise<void>;
  readonly enabled:boolean;
  find(latitude:number,longitude:number,period:Period):NamedPlaceRow|null;
  load(latitude:number,longitude:number,period?:Period):Promise<NamedPlaceRow|null>;
};
