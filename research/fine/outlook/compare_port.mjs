// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
import fs from 'node:fs';
import crypto from 'node:crypto';
const [expectedPath,actualPath,outputPath]=process.argv.slice(2);
const read=path=>fs.readFileSync(path,'utf8').trim().split('\n').filter(line=>line.startsWith('{')).map(line=>JSON.parse(line));
const expected=read(expectedPath),actual=new Map(read(actualPath).map(value=>[value.id,value]));
let values=0,maximumDifference=0;const failures=[];
function compare(expected,actual,path){
 if(typeof expected==='number'){
  const difference=Math.abs(expected-actual);values++;maximumDifference=Math.max(maximumDifference,difference);
  if(!Number.isFinite(actual)||difference>1e-7)failures.push({path,expected,actual,difference});
 }else if(Array.isArray(expected)){
  if(!Array.isArray(actual)||actual.length!==expected.length){failures.push({path,expectedLength:expected.length,actualLength:actual?.length});return;}
  expected.forEach((value,index)=>compare(value,actual[index],`${path}/${index}`));
 }else if(expected!==null&&typeof expected==='object'){
  for(const [key,value]of Object.entries(expected)){if(['pixel','resolutionArcSeconds','status'].includes(key))continue;compare(value,actual?.[key],`${path}/${key}`);}
 }else if(expected!==actual)failures.push({path,expected,actual});
}
for(const value of expected)compare(value,actual.get(value.id),value.id);
const sha=path=>crypto.createHash('sha256').update(fs.readFileSync(path)).digest('hex');
const result={written_by:'AI, GPT-6.1 Sol, 2026.10.18',passes:failures.length===0,queries:expected.length,numericValues:values,maximumAbsoluteDifference:maximumDifference,expectedSha256:sha(expectedPath),actualSha256:sha(actualPath),failures:failures.slice(0,30)};
fs.writeFileSync(outputPath,JSON.stringify(result,null,2)+'\n');console.log(JSON.stringify(result));
if(failures.length)process.exitCode=1;
