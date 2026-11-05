import {createRequire} from 'node:module';
import {existsSync,readdirSync,writeFileSync} from 'node:fs';
import {execFileSync} from 'node:child_process';
import {homedir} from 'node:os';
import {join} from 'node:path';
const {chromium}=createRequire(new URL('../apps/web/package.json',import.meta.url))('@playwright/test');
const url=new URL(process.argv[2]);
if(!['localhost','127.0.0.1'].includes(url.hostname)||Number(url.port)<24100||Number(url.port)>24299)throw new Error('Expected local worker live view');
url.pathname='/vnc.html';url.search='autoconnect=1&resize=scale';
const cache=join(homedir(),'Library/Caches/ms-playwright');
const candidates=[chromium.executablePath(),...(existsSync(cache)?readdirSync(cache):[]).filter(n=>n.startsWith('chromium_headless_shell-')).sort().reverse().map(n=>join(cache,n,'chrome-headless-shell-mac-arm64/chrome-headless-shell'))];
const executablePath=candidates.find(p=>existsSync(p));
if(!executablePath){
  const cid=execFileSync('docker',['ps','-q','--filter','label=smm.worker=true','--filter',`publish=${url.port}`],{encoding:'utf8'}).trim();
  if(!cid||cid.includes('\n'))throw new Error('Expected one running worker for capture');
  const script=`const {createRequire}=require('node:module');const {chromium}=createRequire('/repo/apps/worker/package.json')('playwright');
    (async()=>{const b=await chromium.launch({headless:true,args:['--no-sandbox']});try{const p=await b.newPage({viewport:{width:1280,height:900}});
    await p.goto('http://127.0.0.1:6080/vnc.html?autoconnect=1&resize=scale',{waitUntil:'domcontentloaded'});
    await p.waitForFunction(()=>document.documentElement.classList.contains('noVNC_connected'),{timeout:15000});
    await p.waitForTimeout(5000);process.stdout.write((await p.screenshot()).toString('base64'));}finally{await b.close()}})().catch(()=>process.exit(1));`;
  const data=execFileSync('docker',['exec',cid,'node','-e',script],{encoding:'utf8',timeout:60000,maxBuffer:8*1024*1024});
  writeFileSync(process.argv[3],Buffer.from(data,'base64'));
  process.exit(0);
}
const browser=await chromium.launch({headless:true,executablePath});
try{
  const page=await browser.newPage({viewport:{width:1280,height:900}});
  await page.goto(url.href,{waitUntil:'domcontentloaded'});
  await page.waitForFunction(()=>document.documentElement.classList.contains('noVNC_connected'),undefined,{timeout:15000});
  await page.waitForTimeout(5000);
  await page.screenshot({path:process.argv[3]});
}finally{await browser.close();}
