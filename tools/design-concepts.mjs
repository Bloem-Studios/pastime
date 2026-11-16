// Original vector compositions, not image-model output or screenshots of completed features.
// Run with the pinned browser dependency. No reference-product artwork or source is used.
import fs from 'node:fs/promises';
import { chromium } from '@playwright/test';
import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { House, Books, Stack, ClockCounterClockwise, GearSix, Users, EnvelopeSimple, Plugs, ListChecks, ShieldCheck, FloppyDisk, Plus, MagnifyingGlass, ArrowLeft, CaretRight } from '@phosphor-icons/react';
const root = new URL('../', import.meta.url);
const output = new URL('docs/design/concepts/', root);
await fs.mkdir(output, { recursive: true });
const font = (await fs.readFile(new URL('node_modules/@fontsource-variable/geist/files/geist-latin-wght-normal.woff2', root))).toString('base64');
const esc = s => String(s).replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('"','&quot;');
const palettes = { dark: {bg:'#141715',side:'#101310',panel:'#1c211d',text:'#eff2ed',muted:'#adb6aa',line:'#30382f',accent:'#c2e4a8',ink:'#172412'}, light: {bg:'#f5f7f3',side:'#ecefe8',panel:'#ffffff',text:'#20271e',muted:'#58634f',line:'#d7dfd0',accent:'#365e24',ink:'#f7fff2'} };
const titles=['Orbit Nine','Atlas of Quiet Places','The Last Light','A Season Elsewhere','Northbound','The Long Way Home'];
const colors=['#38454e','#405443','#66483a','#50475d','#395054','#655732'];
function composition(screen,mobile,mode){
 const c=palettes[mode],w=mobile?390:1366,h=mobile?844:900,side=mobile?0:220,x=mobile?22:260,full=w-x-(mobile?22:40); let content='',fixedBottom='';
 const box=(x,y,w,h,fill=c.panel,r=8,stroke='none')=>content+=`<rect x="${x}" y="${y}" width="${w}" height="${h}" rx="${r}" fill="${fill}" stroke="${stroke}"/>`;
 const text=(x,y,value,size=14,color=c.text,weight=400)=>content+=`<text x="${x}" y="${y}" fill="${color}" font-size="${size}" font-weight="${weight}" font-family="Geist">${esc(value)}</text>`;
 const icon=(Type,x,y,color=c.muted,size=19)=>content+=`<g transform="translate(${x} ${y})">${renderToStaticMarkup(React.createElement(Type,{size,weight:'light',color})).replace('<svg ','<svg overflow="visible" ')}</g>`;
 const line=(y)=>box(x,y,full,1,c.line,0);
 const button=(x,y,label,primary=false,width=126)=>{box(x,y,width,42,primary?c.accent:c.panel,7,primary?'none':c.line);text(x+16,y+26,label,14,primary?c.ink:c.text,550);};
 const field=(x,y,label,value,width=full)=>{text(x,y,label,13,c.text,500);box(x,y+12,width,44,c.panel,6,c.line);text(x+13,y+40,value,14,c.muted);};
 const cover=(x,y,width,height,title,index)=>{box(x,y,width,height,colors[index%colors.length],5);box(x+14,y+17,26,2,'#d3dece',0);text(x+14,y+39,'PERSONAL EDITION',8,'#e1e8db',500);const size=width<150?11:20,wrap=Math.floor((width-28)/(size*.6));let words=title.toUpperCase().split(' '),row='',rowY=y+height*.48;for(const word of words){if((row+' '+word).length>wrap&&row){text(x+14,rowY,row,size,'#f4f2e9',600);rowY+=size*1.22;row='';}row+=(row?' ':'')+word;}if(row)text(x+14,rowY,row,size,'#f4f2e9',600);box(x+14,y+height-24,width-28,1,'#98a08f',0);};
 box(0,0,w,h,c.bg,0);
 const admin=['users','account','integrations','jobs'].includes(screen);
 if(!mobile && screen!=='claim'){box(0,0,side,h,c.side,0);icon(FloppyDisk,24,26,c.accent,25);text(60,47,'Pastime',18,c.text,600);text(24,98,admin?'ADMINISTRATION':'YOUR WORKSPACE',10,c.muted,500);
  const nav=admin?[[Users,'Users'],[EnvelopeSimple,'Invitations'],[ShieldCheck,'Audit'],[Plugs,'Integrations'],[ListChecks,'Jobs & sync']]:[[House,'Home'],[Books,'Library'],[Stack,'Lists'],[ClockCounterClockwise,'Activity']];
  nav.forEach(([I,label],i)=>{let yy=122+i*48;let selected=label.toLowerCase().startsWith(screen==='account'?'users':screen);if(selected)box(12,yy,196,42,c.panel,7);icon(I,26,yy+11,selected?c.accent:c.muted);text(57,yy+27,label,14,selected?c.text:c.muted,selected?600:400);});
  icon(admin?ArrowLeft:GearSix,26,784);text(57,799,admin?'My library':'Account',14,c.muted);box(22,831,32,32,c.panel,16);text(31,853,'J',14,c.text,500);text(65,851,'Jamie',13,c.text,500);text(65,867,admin?'Owner':'Personal library',10,c.muted);
  box(side,0,w-side,65,c.bg,0);icon(admin?ShieldCheck:MagnifyingGlass,x,24);text(x+30,40,admin?'Account and access':'Search your library',14,c.muted);button(w-178,13,admin?'My library':'Add media',!admin,138);box(side,65,w-side,1,c.line,0);
 } else if(mobile) {icon(FloppyDisk,22,19,c.accent,25);text(58,40,admin?'Administration':'Pastime',17,c.text,600);icon(MagnifyingGlass,318,22);box(352,19,22,22,c.panel,11);text(359,35,'J',11);box(0,64,w,1,c.line,0);const start=content.length;box(0,h-67,w,67,c.side,0);[[House,'Home'],[Books,'Library'],[Stack,'Lists'],[ClockCounterClockwise,'Activity']].forEach(([I,label],i)=>{icon(I,36+i*94,h-53);text(29+i*94,h-18,label,10,c.muted);});fixedBottom=screen==='claim'?'':content.slice(start);content=content.slice(0,start); }
 const top=mobile?108:123;
 if(screen==='home'){
  text(x,top,'Keep the story going.',mobile?28:36,c.text,550);text(x,top+31,'Your next chapter is right here.',14,c.muted);text(x,top+81,'Continue tracking',20,c.text,550);
  const cy=top+106;const count=mobile?2:3;const cw=mobile?full:(full-48)/3;
  for(let i=0;i<count;i++){const xx=mobile?x:x+i*(cw+24),yy=mobile?cy+i*186:cy;cover(xx,yy,96,145,titles[i],i);text(xx+112,yy+27,titles[i].length>21?'Atlas of Quiet Places':titles[i],mobile?15:17,c.text,550);text(xx+112,yy+53,i===0?'Series · In progress':'Book · In progress',12,c.muted);text(xx+112,yy+91,i===0?'8 of 12 episodes':'142 of 284 pages',13,c.muted);button(xx+112,yy+105,'Update',false,95);}
  const yy=mobile?cy+393:cy+203;text(x,yy,'On your list',20,c.text,550);for(let i=0;i<(mobile?2:5);i++){const cw=mobile?164:(full-80)/5,xx=x+i*(cw+20);cover(xx,yy+27,cw,mobile?205:232,titles[i+1],i+1);text(xx,yy+(mobile?258:285),titles[i+1],12,c.text,500);}
 } else if(screen==='library'){
  text(x,top,'Library',mobile?30:36,c.text,550);text(x,top+31,'Everything you are watching, reading and playing.',mobile?12:14,c.muted);
  if(!mobile)button(w-183,top-30,'Add media',true,143);
  box(x,top+56,full,mobile?42:46,c.panel,7,c.line);icon(MagnifyingGlass,x+14,top+69);text(x+44,top+85,'Search titles',14,c.muted);
  text(x,top+134,'All media',13,c.text,600);text(x+96,top+134,'In progress',13,c.muted);text(x+205,top+134,'Planned',13,c.muted);line(top+149);
  const cw=mobile?164:(full-80)/5,hh=mobile?225:250;for(let i=0;i<(mobile?4:5);i++){let col=mobile?i%2:i,row=mobile?Math.floor(i/2):0,xx=x+col*(cw+18),yy=top+171+row*(hh+79);cover(xx,yy,cw,hh,titles[i],i);text(xx,yy+hh+24,titles[i],12,c.text,550);text(xx,yy+hh+45,i%2?'Book · Planned':'Series · In progress',11,c.muted);}
 } else if(screen==='detail'){
  icon(ArrowLeft,x,top-13);text(x+28,top,'Back to library',13,c.muted);
  const yy=top+28,cw=mobile?106:174,ch=mobile?160:252;cover(x,yy,cw,ch,'Atlas of Quiet Places',1);const tx=x+cw+(mobile?19:30);text(tx,yy+27,'Atlas of',mobile?22:34,c.text,550);text(tx,yy+57,'Quiet Places',mobile?22:34,c.text,550);text(tx,yy+86,'Book · 2026',13,c.muted);text(tx,yy+117,'In progress',13,c.accent,550);
  if(!mobile){text(tx,yy+167,'142 of 284 pages',16,c.muted);button(tx,yy+190,'Update progress',true,164);button(tx+180,yy+190,'Add to list',false,132);}
  const formY=mobile?yy+203:yy+311;text(x,formY,'Your progress',22,c.text,550);field(x,formY+38,'Pages read','142',mobile?full:240);if(mobile)button(x,formY+109,'Update progress',true,full);
  const notesY=formY+(mobile?201:141);text(x,notesY,'Private notes',19,c.text,550);box(x,notesY+22,full,120,c.panel,7,c.line);text(x+16,notesY+52,'A place to keep your thoughts.',14,c.muted);text(x,notesY+166,'Only you can see these notes.',12,c.muted);
 } else if(screen==='users'){
  text(x,top,'Users',mobile?30:36,c.text,550);text(x,top+31,'Manage access. Personal libraries stay private.',mobile?12:14,c.muted);
  button(x,top+54,'Invite',true,105);button(x+117,top+54,'Create account',false,153);box(x,top+122,full,44,c.panel,7,c.line);icon(MagnifyingGlass,x+14,top+135);text(x+44,top+151,'Search usernames',14,c.muted);
  const names=[['Jamie Brooks','jamie','Owner','Active'],['Alex Rowan','alex','Member','Active'],['Morgan Reed','morgan','Admin','Active'],['Noa Ellis','noa','Member','Awaiting setup']];
  if(!mobile){text(x,top+210,'ACCOUNT',10,c.muted,500);text(x+430,top+210,'ROLE',10,c.muted,500);text(x+630,top+210,'STATUS',10,c.muted,500);text(x+820,top+210,'LAST SIGN-IN',10,c.muted,500);}
  names.forEach((a,i)=>{const yy=top+(mobile?200:230)+i*(mobile?94:80);line(yy-12);box(x,yy,36,36,c.panel,18);text(x+12,yy+24,a[0][0],14,c.text,500);text(x+51,yy+14,a[0],15,c.text,550);text(x+51,yy+36,'@'+a[1],12,c.muted);if(mobile){text(x+51,yy+62,a[2]+' · '+a[3],12,c.muted);icon(CaretRight,w-38,yy+9);}else{text(x+430,yy+22,a[2],13);text(x+630,yy+22,a[3],13,c.muted);text(x+820,yy+22,i===3?'Not yet':'Today, 09:42',13,c.muted);}});
 } else if(screen==='claim'){
  const fx=mobile?x:x+220,fw=mobile?full:440,yy=mobile?top:top+47;
  text(fx,yy,'A place for your stories.',mobile?27:34,c.text,550);text(fx,yy+33,'You have been invited to join this library server.',mobile?12:14,c.muted);field(fx,yy+89,'Username','Choose a username',fw);field(fx,yy+179,'Display name','How should we call you?',fw);field(fx,yy+269,'Password','At least 12 characters',fw);button(fx,yy+355,'Create account',true,fw);text(fx,yy+427,'Your library and notes are private by default.',12,c.muted);
 } else if(screen==='account'){
  text(x,top,'Alex Rowan',mobile?30:36,c.text,550);text(x,top+31,'@alex · Member',14,c.muted);text(x,top+89,'Account',15,c.text,550);text(x+101,top+89,'Access',15,c.muted);line(top+107);
  const fw=mobile?full:500;field(x,top+148,'Display name','Alex Rowan',fw);field(x,top+241,'Contact email','alex@example.test',fw);field(x,top+334,'Role','Member',fw);button(x,top+414,'Save changes',true,148);line(top+499);text(x,top+536,'Account access',19,c.text,550);text(x,top+566,'Reset credentials without exposing private media.',mobile?12:14,c.muted);button(x,top+589,'Reset link',false,126);button(x+138,top+589,'Disable account',false,157);
 } else if(screen==='integrations'){
  text(x,top,'Integrations',mobile?30:36,c.text,550);text(x,top+31,'Connect only the services you choose.',14,c.muted);text(x,top+95,'CONCEPT · NOT IMPLEMENTED',11,c.muted,550);
  [['Metadata providers','Add artwork and discover new titles.'],['Meilisearch','Optional, rebuildable catalog search.'],['Mail delivery','Send invitations instead of copying links.']].forEach((a,i)=>{const yy=top+132+i*153;line(yy-16);text(x,yy+18,a[0],19,c.text,550);text(x,yy+49,a[1],mobile?12:14,c.muted);text(x,yy+87,'Not configured',13,c.muted);if(!mobile)button(w-194,yy+13,'Configure',false,134);});
 } else if(screen==='jobs'){
  text(x,top,'Jobs & sync',mobile?30:36,c.text,550);text(x,top+31,'Know what ran, what failed and what needs you.',mobile?12:14,c.muted);text(x,top+95,'CONCEPT · NOT IMPLEMENTED',11,c.muted,550);line(top+129);
  const cx=mobile?x:x+270;icon(ListChecks,cx,top+209,c.muted,32);text(cx,top+274,'No jobs to review',25,c.text,550);text(cx,top+309,'Background work will appear here.',14,c.muted);text(cx,top+337,'Uncertain writes need reconciliation, not blind retries.',mobile?11:13,c.muted);
 }
 return `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}">${content}${fixedBottom}</svg>`;
}
const browser=await chromium.launch();
for(const screen of ['home','library','detail','users','claim','account','integrations','jobs']){
 for(const mobile of [false,true]){
  const mode=['users','account','claim','jobs'].includes(screen)?'light':'dark';const name=`${screen}-${mobile?'390':'1366'}-${mode}`;const svg=composition(screen,mobile,mode);
  await fs.writeFile(new URL(name+'.svg',output),svg);
  const page=await browser.newPage({viewport:{width:mobile?390:1366,height:mobile?844:900},deviceScaleFactor:1});
  await page.setContent(`<style>@font-face{font-family:Geist;src:url(data:font/woff2;base64,${font});font-weight:100 900}body{margin:0}</style>${svg}`);await page.evaluate(()=>document.fonts.ready);
  await page.screenshot({path:new URL(name+'.png',output).pathname});await page.close();
 }
}
await browser.close();console.log('Rendered 16 original standalone desktop/mobile concepts. Integrations and jobs are explicitly future concepts.');
