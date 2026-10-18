// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import {previewFontMetadata as atlas, previewFontMask} from './fine-preview-font.mjs';
import {shareLogo, shareLand, shareFlags} from './share-assets.mjs';

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
const textTokens=text=>text.match(/[\u{1F1E6}-\u{1F1FF}]{2}|[\s\S]/gu)??[];
const flagCode=token=>[...token].map(character=>String.fromCharCode(character.codePointAt(0)-127397)).join('');
const isFlag=token=>/^([\u{1F1E6}-\u{1F1FF}]{2})$/u.test(token);
function supportedText(text, fallback) {
  return textTokens(text).map(token=>isFlag(token)||atlas.fonts.Regular[token]?token:token.normalize('NFKD').replace(/\p{Mark}/gu,'').split('').map(character=>atlas.fonts.Regular[character]?character:'?').join('')).join('');
}
function textWidth(text,size,weight='Regular') {
  return textTokens(text).reduce((width,token)=>width+(isFlag(token)?size*1.2:token===' '?size*.32:atlas.fonts[weight][token].Advance*size/atlas.size),0);
}
let flagPixels;
function drawText(pixels,mask,text,x,baseline,size,colour=INK,weight='Regular') {
  const scale=size/atlas.size;
  for(const character of textTokens(text)) {
    if(isFlag(character)) {
      const flag=shareFlags.codes.indexOf(flagCode(character)),width=Math.ceil(size*1.1),height=Math.ceil(size),top=baseline-size*.82;
      if(flag>=0&&flagPixels)for(let y=0;y<height;y++)for(let px=0;px<width;px++) {
        const source=((flag*shareFlags.size+Math.floor(y/height*shareFlags.size))*shareFlags.size+Math.floor(px/width*shareFlags.size))*4,alpha=flagPixels[source+3]/255;
        const target=((Math.floor(top+y))*WIDTH+Math.floor(x+px))*3;
        if(target>=0&&target<pixels.length)for(let channel=0;channel<3;channel++)pixels[target+channel]=Math.round(pixels[target+channel]*(1-alpha)+flagPixels[source+channel]*alpha);
      }
      x+=size*1.2;continue;
    }
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
    x+=character===' '?size*.32:glyph.Advance*scale;
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

export const SHARE_WIDTH=WIDTH, SHARE_HEIGHT=HEIGHT;
export const SHARE_MAP={x:40,y:174,width:1120,height:338};
const BANDS=[['Awful',0,[74,18,34]],['Poor',25,[156,64,64]],['Fair',40,[111,123,138]],['Nice',55,[59,158,219]],['Beautiful',70,[85,194,122]],['Perfect',85,[245,197,66]]];
const bandFor=score=>BANDS.findLast(band=>score>=band[1])??BANDS[0];
const unpack=async value=>new Uint8Array(await new Response(new Blob([Uint8Array.from(atob(value),character=>character.charCodeAt(0))]).stream().pipeThrough(new DecompressionStream('gzip'))).arrayBuffer());
let logoPromise,landPromise;
const clean=value=>supportedText(String(value??''));
function fitLines(value,maxWidth,size,weight='Bold',maxLines=2) {
  const words=clean(value).split(/\s+/),lines=[];let line='';
  for(const word of words) {
    if(line&&textWidth(`${line} ${word}`,size,weight)>maxWidth){lines.push(line);line=word;}else line=line?`${line} ${word}`:word;
  }
  if(line)lines.push(line);
  if(lines.length>maxLines)return fitLines(value,maxWidth,size-1,weight,maxLines);
  return {lines,size};
}
function drawLogo(pixels,mask,colour) {
  const width=216,height=216*shareLogo.height/shareLogo.width,x=40,y=47;
  for(let row=0;row<Math.ceil(height);row++)for(let col=0;col<width;col++) {
    const alpha=mask[Math.min(shareLogo.height-1,Math.floor(row/height*shareLogo.height))*shareLogo.width+Math.floor(col/width*shareLogo.width)]/255;
    const index=((Math.floor(y+row))*WIDTH+x+col)*3;
    for(let channel=0;channel<3;channel++)pixels[index+channel]=Math.round(pixels[index+channel]*(1-alpha)+colour[channel]*alpha);
  }
}
function copyMap(pixels,map) {
  const frame=SHARE_MAP,scale=Math.max(frame.width/map.width,frame.height/map.height);
  const sourceWidth=frame.width/scale,sourceHeight=frame.height/scale;
  const left=Math.max(0,Math.min(map.width-sourceWidth,(map.focusX??map.width/2)-sourceWidth/2)),top=Math.max(0,Math.min(map.height-sourceHeight,(map.focusY??map.height/2)-sourceHeight/2));
  for(let y=0;y<frame.height;y++)for(let x=0;x<frame.width;x++) {
    const source=(Math.min(map.height-1,Math.floor(top+y/scale))*map.width+Math.min(map.width-1,Math.floor(left+x/scale)))*3;
    pixels.set(map.pixels.subarray(source,source+3),((frame.y+y)*WIDTH+frame.x+x)*3);
  }
}

/** The only image composition, used by browser exports and server link previews. */
export async function shareImagePng(content) {
  const dark=content.dark??false,paper=dark?[14,15,17]:[238,234,225],ink=dark?[243,241,236]:[20,20,19],muted=dark?[170,166,158]:[95,91,83];
  const pixels=new Uint8Array(WIDTH*HEIGHT*3);for(let i=0;i<pixels.length;i+=3)pixels.set(paper,i);
  const [mask,logo,flags]=await Promise.all([fontMask(),logoPromise??=unpack(shareLogo.mask),unpack(shareFlags.rgba)]);
  flagPixels=flags;
  drawLogo(pixels,logo,ink);
  const heading=fitLines(content.title||'Where is the weather nice?',856,35,'Bold',2);
  heading.lines.forEach((line,index)=>drawText(pixels,mask,line,294,65+index*(heading.size+4),heading.size,ink,'Bold'));
  const dates=fitLines(content.dates||'',1120,23,'Regular',1);
  dates.lines.forEach(line=>drawText(pixels,mask,line,40,148,dates.size,muted));
  if(content.map)copyMap(pixels,content.map);
  const numbers=content.numbers;
  const detail=numbers?`${Math.floor(numbers.score)} / 100, ${bandFor(numbers.score)[0]}${numbers.totalDays>1?' most days':''}.${(numbers.countedDays??numbers.totalDays)>1?` Nice on ${numbers.niceDayCount} of ${numbers.countedDays??numbers.totalDays} ${numbers.countBasis==='sample'?'sampled ':''}days; perfect on ${numbers.perfectDayCount}.`:''}`:(content.detail||'A world of weather, by your taste.');
  const figures=fitLines(detail,1120,26,'Bold',1);
  figures.lines.forEach(line=>drawText(pixels,mask,line,40,554,figures.size,ink,'Bold'));
  BANDS.forEach(([name,minimum,colour],index)=>{const x=40+index*141;rectangle(pixels,x,590,16,10,colour);drawText(pixels,mask,`${name} ${minimum}+`,x+23,602,16,muted);});
  const site='nice.jjpcodes.com';drawText(pixels,mask,site,1160-textWidth(site,20),602,20,ink,'Bold');
  return encodePng(pixels,{template:'nice-share-v1',title:content.title,dates:content.dates,numbers:content.numbers??null});
}

/** Decode an existing 8-bit score PNG. The stored images are greyscale, not map screenshots. */
export async function decodeScorePng(bytes) {
  const view=new DataView(bytes.buffer,bytes.byteOffset,bytes.byteLength);let width=0,height=0,channels=1,offset=8;const pieces=[];
  while(offset<bytes.length){const length=view.getUint32(offset),kind=String.fromCharCode(...bytes.subarray(offset+4,offset+8));if(kind==='IHDR'){width=view.getUint32(offset+8);height=view.getUint32(offset+12);if(bytes[offset+16]!==8||bytes[offset+17]!==0)throw new Error('Expected an 8-bit greyscale weather image');}if(kind==='IDAT')pieces.push(bytes.slice(offset+8,offset+8+length));offset+=length+12;}
  const rows=new Uint8Array(await new Response(new Blob(pieces).stream().pipeThrough(new DecompressionStream('deflate'))).arrayBuffer()),pixels=new Uint8Array(width*height);
  for(let y=0;y<height;y++){const filter=rows[y*(width+1)];for(let x=0;x<width;x++){const a=x?pixels[y*width+x-1]:0,b=y?pixels[(y-1)*width+x]:0,c=x&&y?pixels[(y-1)*width+x-1]:0,p=a+b-c,pa=Math.abs(p-a),pb=Math.abs(p-b),pc=Math.abs(p-c);const predictor=filter===0?0:filter===1?a:filter===2?b:filter===3?Math.floor((a+b)/2):pa<=pb&&pa<=pc?a:pb<=pc?b:c;pixels[y*width+x]=(rows[y*(width+1)+x+1]+predictor)&255;}}
  return {width,height,pixels};
}

const splineWeights=t=>[(1-t)**3/6,(3*t**3-6*t*t+4)/6,(-3*t**3+3*t*t+3*t+1)/6,t**3/6];
function smoothScore(scores,x,y) {
  const column=Math.floor(x),row=Math.floor(y),horizontal=splineWeights(x-column),vertical=splineWeights(y-row);let sum=0,weight=0;
  for(let dy=0;dy<4;dy++)for(let dx=0;dx<4;dx++){
    const sourceY=Math.max(0,Math.min(scores.height-1,row+dy-1)),sourceX=((column+dx-1)%scores.width+scores.width)%scores.width;
    const score=scores.pixels[sourceY*scores.width+sourceX],part=horizontal[dx]*vertical[dy];if(score!==255){sum+=score*part;weight+=part;}
  }
  return weight?sum/weight:NaN;
}

/** A world map from the existing score image, with quiet land borders and an optional place marker. */
export async function previewMap(scores,place) {
  const land=await(landPromise??=unpack(shareLand.mask));const {width,height}=shareLand,pixels=new Uint8Array(width*height*3),sea=[251,249,244];
  for(let y=0;y<height;y++)for(let x=0;x<width;x++){
    const offset=(y*width+x)*3,type=land[y*width+x];let colour=sea;
    if(type){const longitude=x/width*360-180,latitude=84-y/height*144,sx=(longitude+180)/360*scores.width,sy=(90-latitude)/180*(scores.height-1);const score=smoothScore(scores,sx,sy);colour=Number.isFinite(score)?bandFor(score)[2]:[213,217,223];if(type===2)colour=colour.map(value=>Math.round(value*.7+20*.3));}
    pixels.set(colour,offset);
  }
  if(place){const cx=(place.longitude+180)/360*width,cy=(84-place.latitude)/144*height;for(let y=Math.max(0,Math.floor(cy-7));y<Math.min(height,cy+8);y++)for(let x=Math.max(0,Math.floor(cx-7));x<Math.min(width,cx+8);x++){const radius=Math.hypot(x-cx,y-cy);if(radius<=7)pixels.set(radius>4?[255,255,255]:[20,20,19],(y*width+x)*3);}}
  return {width,height,pixels};
}
const englishCountries=new Intl.DisplayNames(['en'],{type:'region'});
const countryCodesByName=new Map(shareFlags.codes.map(code=>[englishCountries.of(code).toLowerCase(),code]));
for(const [name,code] of Object.entries({'palestine':'PS','the netherlands':'NL','united states':'US','united kingdom':'GB','russia':'RU','turkey':'TR','iran':'IR','syria':'SY'}))countryCodesByName.set(name,code);
export function sharePlaceTitle(place) {
  const name=place.region?.split(',').at(-1)?.trim().toLowerCase(),slug=place.slug?.split('/')[0]?.replaceAll('-',' ');
  const code=name&&/^[a-z]{2}$/.test(name)?name.toUpperCase():countryCodesByName.get(name)??countryCodesByName.get(slug);
  const flag=code?[...code].map(letter=>String.fromCodePoint(127397+letter.charCodeAt(0))).join(''):'';
  return `${flag} ${place.name}`.trim();
}
/** Name hit boxes use the same line wrapping as the picture; they are not part of the PNG. */
export function shareHeadingLinks(title,labels) {
  const heading=fitLines(title,856,35,'Bold',2),links=[];
  for(const [index,line] of heading.lines.entries())for(const label of labels) {
    const text=clean(label),start=line.indexOf(text);
    if(start>=0)links.push({label,x:294+textWidth(line.slice(0,start),heading.size,'Bold'),y:65+index*(heading.size+4)-heading.size,width:textWidth(text,heading.size,'Bold'),height:heading.size+8});
  }
  return links;
}
