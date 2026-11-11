// SPDX-License-Identifier: AGPL-3.0-or-later
const {test}=require('node:test');const assert=require('node:assert/strict');const fs=require('node:fs');const vm=require('node:vm');const path=require('node:path');
function fixture(value,blocked=false){
 const events={};const storage={};let focus=0;
 const buttons=['comfortable','compact'].map(densityChoice=>({dataset:{densityChoice},attributes:{},setAttribute(k,v){this.attributes[k]=v;}}));
 const control={hidden:true};const menu={open:true,contains(){return false;},querySelector(){return{focus(){focus++;}}}};
 const root={dataset:{comfyApp:'tidings'}};
 const doc={documentElement:root,querySelectorAll(q){if(q==='[data-density-choice]')return buttons;if(q==='[data-density-controls]')return[control];if(q==='.comfy-menu[open]')return menu.open?[menu]:[];return[];},addEventListener(k,v){events[k]=v;}};
 const localStorage={getItem(){if(blocked)throw Error('blocked');return value;},setItem(k,v){if(blocked)throw Error('blocked');storage[k]=v;}};
 vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../ui/suite.js'),'utf8'),{document:doc,window:{addEventListener(k,v){events['window:'+k]=v;}},localStorage});
 return{root,buttons,control,menu,events,storage,focus:()=>focus};
}
test('reading preferences are bounded, accessible, persistent and work with blocked storage',()=>{
 for(const initial of ['comfortable','compact','invalid',null]){const f=fixture(initial);assert.equal(f.root.dataset.density,initial==='compact'?'compact':'comfortable');assert.equal(f.control.hidden,false);f.events.click({target:{closest(){return f.buttons[1];}}});assert.equal(f.root.dataset.density,'compact');assert.equal(f.buttons[1].attributes['aria-pressed'],'true');assert.equal(f.storage['comfyware:tidings:density'],'compact');}
 const f=fixture(null,true);f.events.click({target:{closest(){return f.buttons[1];}}});assert.equal(f.root.dataset.density,'compact');
});
test('menus close on outside clicks and Escape restores keyboard focus',()=>{
 const f=fixture('compact');f.events.keydown({key:'Escape'});assert.equal(f.menu.open,false);assert.equal(f.focus(),1);f.menu.open=true;f.events.click({target:{closest(){return null;}}});assert.equal(f.menu.open,false);
});
test('storage events respect the app key and htmx restores button states',()=>{
 const f=fixture('compact');f.events['window:storage']({key:'other',newValue:'comfortable'});assert.equal(f.root.dataset.density,'compact');f.events['window:storage']({key:'comfyware:tidings:density',newValue:'comfortable'});assert.equal(f.root.dataset.density,'comfortable');f.events['htmx:afterSwap']();assert.equal(f.buttons[0].attributes['aria-pressed'],'true');
});

test('navigation and theme choices dismiss an open menu',()=>{
 const f=fixture('comfortable');f.menu.contains=()=>true;
 f.events.click({target:{closest(selector){return selector==='a'?{}:null;}}});assert.equal(f.menu.open,false);
 f.menu.open=true;f.events.change({target:{closest(){return {};}}});assert.equal(f.menu.open,false);
});
