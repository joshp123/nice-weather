// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Check captured real cards and map label payloads against the actual compact release.
import fs from 'node:fs/promises';
import path from 'node:path';
import {parseArgs} from 'node:util';
import {gunzipSync} from 'node:zlib';
import {createNamedPlaceLoader,decodeNamedTable,namedPlaceNumbers} from '../../hosting/sites/named-place.mjs';

const {values}=parseArgs({options:{release:{type:'string'},browser:{type:'string'},places:{type:'string'},output:{type:'string'}}});
for(const key of ['release','browser','places','output'])if(!values[key])throw Error(`Missing --${key}`);
const places=JSON.parse(await fs.readFile(values.places));
const climate=decodeNamedTable(gunzipSync(await fs.readFile(path.join(values.release,'climate.bin.gz'))));
const coordinates=(latitude,longitude)=>`${Math.round(latitude*1e5)},${Math.round(longitude*1e5)}`;
const climateRows=new Map(Array.from({length:climate.count},(_,index)=>[`${climate.view.getInt32(32+index*8,true)},${climate.view.getInt32(36+index*8,true)}`,index]));
const loader=createNamedPlaceLoader(async key=>{
  if(key==='v6/named/current.json')return Buffer.from(JSON.stringify({root:'release',generation:values.release,factsRows:16384}));
  if(!key.startsWith('release/'))return null;
  try{return await fs.readFile(path.join(values.release,key.slice(8)));}catch(error){if(error.code==='ENOENT')return null;throw error;}
});
const views=[];
for(const name of ['world-year-laptop','world-year-phone','nov-mar-laptop','nov-mar-phone','lima-year-phone','chiclayo-year-laptop','antofagasta-winter-phone']){
  const capture=JSON.parse(await fs.readFile(path.join(values.browser,name+'.json')));
  if(!capture.passes)throw Error(`Browser did not settle: ${name}`);
  if(capture.requests.some(request=>request.url.endsWith('/climate.bin.gz')))throw Error(`Browser downloaded the whole named climate table: ${name}`);
  const period=capture.pathname.endsWith('nov-mar')?{kind:'months',startMonth:11,endMonth:3}:{kind:'year'};
  const labels=capture.state.proof.labels.at(-1).value;
  const checked=[];let unavailable=0;
  for(const label of labels){
    // The payload also contains off-screen catalogue rows scored by the overview.
    // Visible named rows carry their count badge; selected/headline rows are explicit.
    if(!Number.isFinite(label.score)||!(label.countImage||label.chosen||label.headlineRank!==undefined))continue;
    const climateIndex=climateRows.get(coordinates(label.latitude,label.longitude));
    if(climateIndex===undefined){unavailable++;continue;}
    const expected=namedPlaceNumbers({climate,climateIndex,forecastIndex:-1},period);
    if(label.score!==Math.floor(expected.meanScore))throw Error(`Label score differs: ${name}/${label.name} ${label.score} versus ${expected.meanScore}`);
    if(label.displayedNumber!==undefined&&label.displayedNumber!==expected.niceDayCount)throw Error(`Label nice count differs: ${name}/${label.name}`);
    if(label.countImage&&!label.countImage.endsWith('-'+expected.niceDayCount))throw Error(`Label badge differs: ${name}/${label.name}`);
    checked.push({name:label.name,score:label.score,nice:expected.niceDayCount,headlineRank:label.headlineRank});
  }
  const headline=checked.filter(row=>row.headlineRank!==undefined).sort((left,right)=>left.headlineRank-right.headlineRank);
  const text=capture.state.proof.headlines.at(-1).value;
  let previous=-1;
  for(const place of headline){
    const index=text.indexOf(place.name);
    if(index<=previous)throw Error(`Headline differs from its scored map places: ${name}/${place.name}`);
    previous=index;
  }
  const cardPlace=places.find(place=>capture.pathname.includes('/'+place.slug+'/'));
  let card;
  if(cardPlace){
    const packet=await loader.load(cardPlace.latitude,cardPlace.longitude),expected=namedPlaceNumbers(packet,period),text=capture.state.card;
    const wanted={score:Math.floor(expected.meanScore),nice:expected.niceDayCount,perfect:expected.perfectDayCount,awful:expected.awfulDayCount,sunnyPercent:Math.round(expected.typical.sunnyShare*100),feelSunC:Math.round(expected.typical.feelsInSunAt14C)};
    if(!text.split('\n').includes(String(wanted.score))||!text.includes(`Nice on ${wanted.nice} of `)||!text.includes(`Perfect on ${wanted.perfect}; awful on ${wanted.awful}.`)||!text.includes(`${wanted.sunnyPercent}% of daylight`)||!text.includes(`${wanted.feelSunC} °C feels-like`))throw Error(`Card differs from shared release numbers: ${name}: ${JSON.stringify(wanted)}`);
    card={place:cardPlace.slug,...wanted};
  }
  views.push({name,checkedNamedLabels:checked.length,unavailableLabels:unavailable,headline:headline.map(row=>({name:row.name,score:row.score,nice:row.nice})),card});
}
const report={writtenBy:'AI, GPT-6.1 Sol, 2026-10-18',passes:true,release:values.release,releasePlaces:climate.count,browser:values.browser,noWholeClimateTableRequest:true,scope:'Settled real WebKit card text; map-label rows present in this release with named count badges, selected-place or headline markers; headline names and the same headline-label scores. Missing release rows are counted as unavailable, so a partial probe does not prove worldwide labels or ranking. Off-screen catalogue rows without badges use the overview and are excluded. Ranking selection is checked separately using a fixed ranking rule.',views};
await fs.writeFile(values.output,JSON.stringify(report,null,2)+'\n');
console.log(JSON.stringify(report,null,2));
