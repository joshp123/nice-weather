// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import {previewFontMetadata as atlas, previewFontMask} from './fine-preview-font.mjs';

const WIDTH=1200, HEIGHT=630;
const INK=[32,57,52], PAPER=[246,246,238], MUTED=[86,107,96], GOLD=[171,113,29];
let maskPromise;
async function fontMask() {
  maskPromise ??= (async()=> {
    const packed=Uint8Array.from(atob(previewFontMask),character=>character.charCodeAt(0));
    return new Uint8Array(await new Response(new Blob([packed]).stream().pipeThrough(new DecompressionStream('gzip'))).arrayBuffer());
  })();
  return maskPromise;
}
function supportedText(text, fallback) {
  const supported=value=>[...value].every(character=>atlas.fonts.Regular[character]);
  if(supported(text))return text;
  const plain=(fallback||text).normalize('NFKD').replace(/\p{Mark}/gu,'');
  return supported(plain)?plain:[...plain].map(character=>atlas.fonts.Regular[character]?character:'?').join('');
}
function textWidth(text,size,weight='Regular') {
  return [...text].reduce((width,character)=>width+atlas.fonts[weight][character].Advance*size/atlas.size,0);
}
function drawText(pixels,mask,text,x,baseline,size,colour=INK,weight='Regular') {
  const scale=size/atlas.size;
  for(const character of text) {
    const glyph=atlas.fonts[weight][character];
    const left=x+glyph.Left*scale, top=baseline+glyph.Top*scale;
    for(let y=Math.max(0,Math.floor(top));y<Math.min(HEIGHT,Math.ceil(top+glyph.Height*scale));y++) {
      for(let px=Math.max(0,Math.floor(left));px<Math.min(WIDTH,Math.ceil(left+glyph.Width*scale));px++) {
        const sx=(px+.5-left)/scale-.5,sy=(y+.5-top)/scale-.5;
        const sample=(a,b)=>a<0||b<0||a>=glyph.Width||b>=glyph.Height?0:mask[(glyph.Y+b)*atlas.width+glyph.X+a];
        const ix=Math.floor(sx),iy=Math.floor(sy),fx=sx-ix,fy=sy-iy;
        const alpha=((sample(ix,iy)*(1-fx)+sample(ix+1,iy)*fx)*(1-fy)+(sample(ix,iy+1)*(1-fx)+sample(ix+1,iy+1)*fx)*fy)/255;
        const offset=(y*WIDTH+px)*3;
        for(let channel=0;channel<3;channel++)pixels[offset+channel]=Math.round(pixels[offset+channel]*(1-alpha)+colour[channel]*alpha);
      }
    }
    x+=glyph.Advance*scale;
  }
}
function rectangle(pixels,x,y,width,height,colour) {
  for(let row=y;row<y+height;row++)for(let col=x;col<x+width;col++)pixels.set(colour,(row*WIDTH+col)*3);
}
const crcTable=Uint32Array.from({length:256},(_,value)=> {
  for(let bit=0;bit<8;bit++)value=value&1?0xedb88320^(value>>>1):value>>>1;
  return value>>>0;
});
function pngChunk(name,bytes) {
  const output=new Uint8Array(bytes.length+12),view=new DataView(output.buffer);
  view.setUint32(0,bytes.length);output.set(new TextEncoder().encode(name),4);output.set(bytes,8);
  let crc=0xffffffff;
  for(const value of output.subarray(4,-4))crc=crcTable[(crc^value)&255]^(crc>>>8);
  view.setUint32(output.length-4,(crc^0xffffffff)>>>0);return output;
}
async function encodePng(pixels,metadata) {
  const rows=new Uint8Array(HEIGHT*(WIDTH*3+1));
  for(let y=0;y<HEIGHT;y++)rows.set(pixels.subarray(y*WIDTH*3,(y+1)*WIDTH*3),y*(WIDTH*3+1)+1);
  const compressed=new Uint8Array(await new Response(new Blob([rows]).stream().pipeThrough(new CompressionStream('deflate'))).arrayBuffer());
  const ihdr=new Uint8Array(13),view=new DataView(ihdr.buffer);view.setUint32(0,WIDTH);view.setUint32(4,HEIGHT);ihdr[8]=8;ihdr[9]=2;
  const chunks=[Uint8Array.of(137,80,78,71,13,10,26,10),pngChunk('IHDR',ihdr),pngChunk('tEXt',new TextEncoder().encode(`NiceWeather\0${JSON.stringify(metadata)}`)),pngChunk('IDAT',compressed),pngChunk('IEND',new Uint8Array())];
  const result=new Uint8Array(chunks.reduce((sum,chunk)=>sum+chunk.length,0));let offset=0;
  for(const chunk of chunks){result.set(chunk,offset);offset+=chunk.length;}return result;
}

export async function finePreviewPng(place,period,judgement) {
  const pixels=new Uint8Array(WIDTH*HEIGHT*3);for(let i=0;i<pixels.length;i+=3)pixels.set(PAPER,i);
  const mask=await fontMask();
  const name=supportedText(place.name,place.plainName),when=supportedText(period.when);
  const score=Math.floor(judgement.score),nice=Math.round(judgement.niceDayCount),perfect=Math.round(judgement.perfectDayCount),awful=Math.round(judgement.awfulDayCount);
  const band=[[85,'Perfect'],[70,'Beautiful'],[55,'Nice'],[40,'Fair'],[25,'Poor'],[0,'Awful']].find(([minimum])=>judgement.score>=minimum)[1];
  rectangle(pixels,56,48,40,6,GOLD);
  drawText(pixels,mask,'Nice Weather',110,65,28,INK,'Bold');
  const nameSize=Math.min(66,1088/textWidth(name,1,'Bold'));
  drawText(pixels,mask,name,56,158,nameSize,INK,'Bold');
  drawText(pixels,mask,when,58,207,32,MUTED);
  drawText(pixels,mask,band,56,332,88,INK,'Bold');
  const bandWidth=textWidth(band,88,'Bold');
  drawText(pixels,mask,'most days',56+bandWidth+24,329,34,MUTED);
  drawText(pixels,mask,`${score}`,56,411,61,GOLD,'Bold');
  drawText(pixels,mask,'/ 100 nice score',56+textWidth(`${score}`,61,'Bold')+15,407,29,MUTED);
  rectangle(pixels,56,447,1088,1,[208,216,204]);
  for(const [index,count,label] of [[0,nice,'nice or better'],[1,perfect,'perfect'],[2,awful,'awful']]) {
    const x=56+index*368;
    drawText(pixels,mask,String(count),x,515,47,INK,'Bold');
    drawText(pixels,mask,label,x+textWidth(String(count),47,'Bold')+13,511,26,MUTED);
  }
  drawText(pixels,mask,`Out of ${judgement.totalDays} days in a typical year`,56,571,24,MUTED);
  const site='nice.jjpcodes.com';drawText(pixels,mask,site,WIDTH-56-textWidth(site,24),571,24,MUTED);
  return encodePng(pixels,{place:place.name,when:period.when,score:judgement.score,niceDayCount:judgement.niceDayCount,perfectDayCount:judgement.perfectDayCount,awfulDayCount:judgement.awfulDayCount,totalDays:judgement.totalDays,labels:{score,nice,perfect,awful,band}});
}
