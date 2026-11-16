import {test,expect} from '@playwright/test';

// Parent owns a task-only synthetic household, fresh schema and loopback listener.
// No production account, environment file or external provider is used.
test.beforeEach(async({page})=>{
 const response=await page.request.post('/api/v1/login',{headers:{Origin:'http://localhost:18164'},data:{username:'profile_preview',password:'synthetic-profile-preview-2026'}});
 expect(response.status()).toBe(200);
 await page.goto('/library');
 await expect(page.getByRole('combobox',{name:'Profile',exact:true})).toBeVisible();
});

test('household can select both profiles and preserve scope across navigation and reload',async({page})=>{
 const chooser=page.getByRole('combobox',{name:'Profile',exact:true});
 await expect(chooser.locator('option')).toHaveCount(2);
 await expect(page.getByRole('link',{name:'Main private book',exact:true})).toBeVisible();
 await chooser.selectOption({label:'Child'});
 await expect(page.getByRole('link',{name:'Child private book',exact:true})).toBeVisible();
 await expect(page.getByRole('link',{name:'Main private book',exact:true})).toHaveCount(0);
 await page.reload();
 await expect(chooser).toHaveValue('2');
 await expect(page.getByRole('link',{name:'Child private book',exact:true})).toBeVisible();
 await chooser.selectOption({label:'Household Main'});
 await expect(page.getByRole('link',{name:'Main private book',exact:true})).toBeVisible();
});

test('delayed old-profile response cannot repaint new selection',async({page})=>{
 let release!:()=>void;const gate=new Promise<void>(resolve=>release=resolve);
 let held!:()=>void;const ready=new Promise<void>(resolve=>held=resolve);
 await page.route('**/api/v1/library?**',async route=>{
  if(route.request().headers()['x-pastime-profile-id']==='2') {
   const response=await route.fetch();held();await gate;
   await route.fulfill({response});
  } else await route.continue();
 });
 await page.evaluate(()=>{
  (window as any).__profileLeaked=false;
  new MutationObserver(()=>{
   const chooser=document.querySelector<HTMLSelectElement>('select[aria-label="Profile"]');
   if(chooser?.value==='1'&&document.querySelector('main')?.innerText.includes('Child private book'))(window as any).__profileLeaked=true;
  }).observe(document.body,{childList:true,subtree:true});
 });
 const chooser=page.getByRole('combobox',{name:'Profile',exact:true});
 await chooser.selectOption({label:'Child'});await ready;
 await chooser.selectOption({label:'Household Main'});release();
 await expect(page.getByRole('link',{name:'Main private book',exact:true})).toBeVisible();
 await expect(page.getByRole('link',{name:'Child private book',exact:true})).toHaveCount(0);
 expect(await page.evaluate(()=>(window as any).__profileLeaked)).toBe(false);
 await page.unrouteAll({behavior:'wait'});
});

test('profile name remains readable and selectable on a narrow phone',async({page})=>{
 await page.setViewportSize({width:320,height:720});
 const chooser=page.getByRole('combobox',{name:'Profile',exact:true});
 await expect.poll(async()=>await chooser.evaluate(e=>e.getBoundingClientRect().width)).toBeGreaterThanOrEqual(88);
 await expect.poll(()=>page.evaluate(()=>document.documentElement.scrollWidth)).toBe(320);
 await chooser.selectOption({label:'Child'});
 await expect(page.getByRole('link',{name:'Child private book',exact:true})).toBeVisible();
});
