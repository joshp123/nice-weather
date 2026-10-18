export type ShareNumbers = Readonly<{score:number;niceDayCount:number;perfectDayCount:number;totalDays:number;countedDays?:number;countBasis?:string}>;
export type ShareMap = Readonly<{pixels:Uint8Array;width:number;height:number;focusX?:number;focusY?:number}>;
export const SHARE_WIDTH:number,SHARE_HEIGHT:number;
export function shareImagePng(content:Readonly<{title:string;dates:string;detail?:string;numbers?:ShareNumbers;map:ShareMap;dark?:boolean}>):Promise<Uint8Array>;
export function shareHeadingLinks(title:string,labels:readonly string[]):ReadonlyArray<{label:string;x:number;y:number;width:number;height:number}>;
