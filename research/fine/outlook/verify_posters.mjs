// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Compare actual rendered poster manifests with independent shared-JS results.
import fs from 'node:fs';
import path from 'node:path';
const [posterRoot,expectedPath,outputPath]=process.argv.slice(2);
const expected=new Map(fs.readFileSync(expectedPath,'utf8').trim().split('\n').map(JSON.parse).map(value=>[value.id,value.numbers[0]]));
const checks=[];
for(const region of ['world','europe','usa']){
 const poster=JSON.parse(fs.readFileSync(path.join(posterRoot,`${region}.json`)));
 for(const place of poster.places){
  const numbers=expected.get(`${place.slug}/tomorrow`) ?? expected.get(`${place.slug}/tomorrow/default`),score=numbers?.score;
  if(!Number.isFinite(score)||Math.floor(score)!==place.score)throw new Error(`${region}/${place.name}: poster ${place.score}, shared JS ${score}`);
  for(const [posterField,numberField]of [['floatScore','score'],['niceDayCount','niceDayCount'],['perfectDayCount','perfectDayCount']]) {
   if(place[posterField]!==undefined&&Math.abs(place[posterField]-numbers[numberField])>1e-7)throw new Error(`${region}/${place.name}/${posterField}: poster ${place[posterField]}, shared JS ${numbers[numberField]}`);
  }
  checks.push({region,name:place.name,posterScore:place.score,sharedJSScore:score,niceDayCount:place.niceDayCount,perfectDayCount:place.perfectDayCount});
 }
}
const report={written_by:'AI, GPT-6.1 Sol, 2026.10.18',passes:true,checks};
fs.writeFileSync(outputPath,JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify(report));
