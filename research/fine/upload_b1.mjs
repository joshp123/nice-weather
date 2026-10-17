// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Upload immutable B1 files through the existing authenticated Sites publisher route.
import fs from 'node:fs/promises';
import path from 'node:path';
import {gzipSync} from 'node:zlib';
import {parseArgs} from 'node:util';

const {values}=parseArgs({options:{root:{type:'string'},release:{type:'string'},site:{type:'string'},
  journal:{type:'string'},workers:{type:'string',default:'24'},'dry-run':{type:'boolean',default:false}}});
for(const key of ['root','release','site','journal'])if(!values[key])throw Error(`Missing --${key}`);
const root=path.resolve(values.root),folder=path.join(root,values.release),workers=Number(values.workers);
if(!Number.isInteger(workers)||workers<1||workers>64)throw Error('--workers must be 1 to 64');
if(path.dirname(folder)!==root)throw Error('--release must name one directory directly under --root');
const release=JSON.parse(await fs.readFile(path.join(folder,'RELEASE.json'),'utf8'));
if(release.status!=='ready')throw Error('The worldwide B1 release is not ready');
const entries=await fs.readdir(folder,{recursive:true,withFileTypes:true});
const files=entries.filter(entry=>entry.isFile()).map(entry=>path.join(entry.parentPath,entry.name)).sort();
let journal='';
try {journal=await fs.readFile(values.journal,'utf8');}catch(error){if(error.code!=='ENOENT')throw error;}
const previous=journal.trim()?journal.trim().split('\n').map(line=>JSON.parse(line)):[];
const identity={site:new URL(values.site).origin,release:values.release,generation:release.generation};
if(previous.length&&JSON.stringify(previous[0])!==JSON.stringify(identity))throw Error('Resume journal belongs to another destination or generation');
const completed=new Set(previous.slice(1).map(row=>row.key));
const pending=files.filter(file=>!completed.has(path.relative(root,file).split(path.sep).join('/')));
let bytes=0;
for(const file of pending)bytes+=(await fs.stat(file)).size;
console.log(JSON.stringify({files:files.length,alreadyUploaded:files.length-pending.length,pending:pending.length,bytes,workers,dryRun:values['dry-run']}));
if(!values['dry-run']) {
  const token=process.env.NICE_WEATHER_PUBLISH_TOKEN;
  if(!token)throw Error('NICE_WEATHER_PUBLISH_TOKEN must be supplied by the existing secret workflow');
  await fs.mkdir(path.dirname(values.journal),{recursive:true});
  if(!previous.length)await fs.writeFile(values.journal,JSON.stringify(identity)+'\n');
  let next=0,uploaded=0;
  const started=performance.now();
  await Promise.all(Array.from({length:workers},async()=> {
    while(next<pending.length) {
      const file=pending[next++],key=path.relative(root,file).split(path.sep).join('/');
      const bytes=await fs.readFile(file),compress=/\.(json|txt|md|tsv|sha256)$/.test(file);
      const body=compress?gzipSync(bytes):bytes;
      const headers={Authorization:`Bearer ${token}`,...(compress?{'Content-Encoding':'gzip'}:{})};
      for(let attempt=1;;attempt++) {
        const response=await fetch(new URL(`/__publish/${key}`,values.site),{method:'PUT',headers,body}).catch(error=>error);
        if(response instanceof Response&&response.status===201)break;
        if(attempt===3)throw Error(`Upload failed for ${key}: ${response instanceof Response?response.status:response.message}`);
        await new Promise(resolve=>setTimeout(resolve,attempt*1000));
      }
      await fs.appendFile(values.journal,JSON.stringify({key,bytes:bytes.length})+'\n');
      uploaded++;
      if(uploaded%1000===0)console.log(JSON.stringify({uploaded,pending:pending.length-uploaded,seconds:(performance.now()-started)/1000}));
    }
  }));
  console.log(JSON.stringify({uploaded,seconds:(performance.now()-started)/1000}));
}
