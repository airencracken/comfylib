// SPDX-License-Identifier: AGPL-3.0-or-later
// Exercise the same browser setup contract against each real application binary.
import assert from 'node:assert/strict';
import { spawn,spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdtemp,mkdir,rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { createServer } from 'node:net';
import { pathToFileURL } from 'node:url';
import { accountLayout,accountWidths } from '../ui/uitest/layout.mjs';

const {chromium}=await import(process.env.COMFYWARE_PLAYWRIGHT_PATH?pathToFileURL(process.env.COMFYWARE_PLAYWRIGHT_PATH).href:'playwright');
const directory=process.argv[2];assert.ok(directory,'usage: node tools/check_setup.mjs BINARY_DIRECTORY [APP...]');
const apps=process.argv.slice(3);if(!apps.length)apps.push('songstead','witmoot','imvault','folkweave','tidings');
const browser=await chromium.launch({headless:true});
const secret='a quiet private first password';
async function freePort(){const socket=createServer();await new Promise(resolve=>socket.listen(0,'127.0.0.1',resolve));const {port}=socket.address();await new Promise(resolve=>socket.close(resolve));return port;}
try{
 for(const app of apps){
  const temporary=await mkdtemp(path.join(tmpdir(),'comfyware-setup-'));const data=path.join(temporary,'data');await mkdir(data,{mode:0o700});const port=await freePort();const origin=`http://127.0.0.1:${port}`;const prefix=app.toUpperCase();
  const env={...process.env,[prefix+'_DATA_DIR']:data,[prefix+'_ADDR']:`127.0.0.1:${port}`,[prefix+'_LISTEN']:`127.0.0.1:${port}`,[prefix+'_BASE_URL']:'',[prefix+'_SECURE_COOKIES']:'false',[prefix+'_BACKUP_INTERVAL']:'0',[prefix+'_DB']:path.join(data,app+'.db')};
  const binary=path.resolve(directory,app);let server,context;let output='';
  try{
   const enabled=spawnSync(binary,['setup'],{env,encoding:'utf8'});assert.equal(enabled.status,0,app+': '+enabled.stderr);const key=enabled.stdout.match(/Setup key: ([a-f0-9]{64})/)?.[1];assert.ok(key,app+': missing host setup key');assert.ok(!enabled.stdout.includes('key='+key),'key leaked into address');
   server=spawn(binary,['serve'],{env,stdio:['ignore','pipe','pipe']});server.stdout.on('data',chunk=>output+=chunk);server.stderr.on('data',chunk=>output+=chunk);
   let ready=false;for(let attempt=0;attempt<150;attempt++){try{const response=await fetch(origin+'/healthz');if(response.ok){ready=true;break;}}catch{}if(server.exitCode!==null)break;await new Promise(resolve=>setTimeout(resolve,100));}assert.ok(ready,app+': server did not start: '+output);
   assert.ok(!output.includes(key),app+': server logged setup key');
   context=await browser.newContext({javaScriptEnabled:false});const page=await context.newPage();await page.goto(origin+'/');assert.equal(new URL(page.url()).pathname,'/setup');
   for(const colorScheme of ['light','dark'])for(const width of accountWidths){await page.emulateMedia({colorScheme});await page.setViewportSize({width,height:1000});assert.ok(await page.evaluate(accountLayout),`${app}: setup layout ${width} ${colorScheme}`);}
   await page.getByLabel('Setup key',{exact:true}).fill('wrong');await page.getByLabel('Username',{exact:true}).fill('alex');await page.getByLabel('Password',{exact:true}).fill(secret);await page.getByLabel('Confirm password',{exact:true}).fill(secret);await page.getByRole('button',{name:'Create owner account',exact:true}).click();assert.equal(new URL(page.url()).pathname,'/setup');assert.ok(await page.getByRole('alert').isVisible());
   await page.getByLabel('Setup key',{exact:true}).fill(key);await page.getByLabel('Username',{exact:true}).fill('alex');await page.getByLabel('Password',{exact:true}).fill(secret);await page.getByLabel('Confirm password',{exact:true}).fill(secret);await page.getByRole('button',{name:'Create owner account',exact:true}).click();assert.equal(new URL(page.url()).pathname,'/login',app+': setup did not reach sign-in');
   await page.getByLabel('Username',{exact:true}).fill('alex');await page.getByLabel('Password',{exact:true}).fill(secret);await page.getByRole('button',{name:'Sign in',exact:true}).click();await page.waitForURL(url=>url.pathname!='/login');assert.ok(!['/login','/setup'].includes(new URL(page.url()).pathname),app+': first owner cannot sign in');
   const replay=await context.request.get(origin+'/setup',{maxRedirects:0});assert.equal(replay.status(),303,app+': setup stayed open');const reenabled=spawnSync(binary,['setup'],{env,encoding:'utf8'});assert.notEqual(reenabled.status,0,app+': initialized setup key could be reissued');assert.ok(!reenabled.stdout.includes(key));
   console.log(app+': private host authorization, first owner, sign-in, closed setup, no-JavaScript forms, and responsive themes passed');
  }finally{if(context)await context.close();if(server&&server.exitCode===null){const stopped=once(server,'exit');server.kill('SIGTERM');await Promise.race([stopped,new Promise(resolve=>setTimeout(resolve,15000))]);if(server.exitCode===null){server.kill('SIGKILL');await stopped;}}await rm(temporary,{recursive:true,force:true});}
 }
}finally{await browser.close();}
