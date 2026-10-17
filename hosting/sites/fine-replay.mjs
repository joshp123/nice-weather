// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// NWFP native-weather replay and independent target-clock lookup.
export const fineGeneration = bytes => Array.from(bytes,value=>value.toString(16).padStart(2,'0')).join('');

export function targetFineZone(bytes,pixel,generation) {
  const view=new DataView(bytes.buffer,bytes.byteOffset,bytes.byteLength);
  const row=Math.floor(pixel.row/256),column=Math.floor(pixel.column/256);
  if(bytes.byteLength!==32+256*256*2||view.getUint32(0,true)!==0x545a574e||view.getUint16(4,true)!==1||view.getUint16(6,true)!==row||view.getUint16(8,true)!==column||view.getUint16(10,true)!==256||view.getUint32(28,true)!==256*256)throw new Error('Fine target-clock tile address mismatch');
  if(fineGeneration(bytes.subarray(12,28))!==generation)throw new Error('Fine target-clock generation mismatch');
  const zone=view.getUint16(32+((pixel.row%256)*256+pixel.column%256)*2,true);
  return zone===65535?null:zone;
}

export function decodeFineReplay(bytes,manifest,fineIndex,zoneIndex) {
  const view=new DataView(bytes.buffer,bytes.byteOffset,bytes.byteLength);
  if(bytes.byteLength<32||view.getUint32(0,true)!==0x5046574e||view.getUint8(4)!==1||view.getUint8(5)!==1||view.getUint32(8,true)!==fineIndex||view.getUint16(12,true)!==zoneIndex)throw new Error('Fine compact replay address mismatch');
  const selectedCount=view.getUint16(6,true),dayCount=view.getUint16(14,true),generation=fineGeneration(bytes.subarray(16,32));
  if(generation!==manifest.generation)throw new Error('Fine compact replay generation mismatch');
  if(dayCount!==manifest.dates.length||bytes.byteLength!==32+selectedCount*2+(selectedCount*54+dayCount*6+12*4+12*2)*4)throw new Error('Fine compact replay length mismatch');
  let offset=32;
  const selectedDateIndices=Array.from({length:selectedCount},()=>{const value=view.getUint16(offset,true);offset+=2;return value;});
  const readFloats=count=>Float32Array.from({length:count},()=>{const value=view.getFloat32(offset,true);offset+=4;return value;});
  const selected=readFloats(selectedCount*54);
  const field=(start,width)=>Float32Array.from({length:selectedCount*width},(_,index)=>selected[Math.floor(index/width)*54+start+index%width]);
  return {fineIndex,generation,targetTimezone:manifest.timezoneNames[zoneIndex],
    dates:selectedDateIndices.map(index=>manifest.dates[index]),selectedDateIndices,
    selectedIndices:Array.from({length:selectedCount},(_,index)=>index),
    airC:field(0,12),dewC:field(12,12),sunC:field(24,12),shadeC:field(36,12),
    highC:field(48,1),lowC:field(49,1),rainMm:field(50,1),windKmh:field(51,1),sunnyHours:field(52,1),daylightHours:field(53,1),
    physicalResponse:{dates:manifest.dates,values:readFloats(dayCount*6),monthly:readFloats(12*4),clockOffsets:readFloats(12*2)}};
}
