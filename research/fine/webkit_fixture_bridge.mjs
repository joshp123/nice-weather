// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Pipe actual local Worker responses to Playwright without opening a second server.
import readline from 'node:readline';
import {localWorkerFixture} from './local_worker_fixture.mjs';
const fixture=localWorkerFixture(process.argv[2]);
for await (const line of readline.createInterface({input:process.stdin})) {
  const request=JSON.parse(line);
  try {
    const response=await fixture(request.url,{method:request.method,headers:request.headers,...(request.body?{body:Buffer.from(request.body,'base64')}:{})});
    const bytes=Buffer.from(await response.arrayBuffer());
    process.stdout.write(JSON.stringify({id:request.id,status:response.status,headers:Object.fromEntries(response.headers),body:bytes.toString('base64')})+'\n');
  } catch(error) {process.stdout.write(JSON.stringify({id:request.id,error:error.stack})+'\n');}
}
