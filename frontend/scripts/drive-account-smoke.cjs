// Run against the local Vite server with a synthetic desktop bridge only.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const assert = require("node:assert/strict");
(async () => {
 const browser = await chromium.launch({headless:true, channel:process.env.PLAYWRIGHT_CHANNEL || "msedge"});
 try {
  const page = await browser.newPage({viewport:{width:1280,height:900}});
  const errors=[];page.on("pageerror",e=>errors.push(e.message));
  await page.goto(process.env.DRIVE_UI_URL || "http://127.0.0.1:5187/");
  // Render the real account import component in a harness without touching an
  // installed desktop profile, license state, or any actual Drive links.
  await page.evaluate(async () => {
   const React = await import('/node_modules/.vite/deps/react.js');
   const ReactDOM = await import('/node_modules/.vite/deps/react-dom_client.js');
   const {DriveAccountsImport} = await import('/src/DriveAccountsImport.tsx');
   let batch={id:"",running:false,items:[]};
   window.__driveCalls=[];
   window.go={wails:{Bindings:{
    GetDriveAccountImportStatus:async()=>batch,
    StartDriveAccountImport:async raw=>{window.__driveCalls.push(raw);batch={id:"synthetic",running:true,items:[{ordinal:1,phase:"downloading",added:0,skipped:0,error:""}]};return batch;},
    CancelDriveAccountImport:async()=>{batch={...batch,running:false,items:[{ordinal:1,phase:"cancelled",added:0,skipped:0,error:""}]};}
   }}};
   const host=document.createElement("div");document.body.replaceChildren(host);
   (ReactDOM.default || ReactDOM).createRoot(host).render((React.default || React).createElement(DriveAccountsImport,{locale:"ru"}));
  });
  const button=page.getByRole('button',{name:'Добавить TData аккаунты',exact:true});await button.click();
  const dialog=page.getByRole('dialog');await dialog.waitFor();
  const start=page.getByRole('button',{name:'Скачать и добавить',exact:true});
  assert(await start.isDisabled());
  await page.getByRole('textbox').fill('https://evil.example/file');assert(await start.isDisabled());
  const links=Array.from({length:100},(_,i)=>`https://drive.google.com/uc?id=synthetic_${i}&export=download`).join('\n');
  await page.getByRole('textbox').fill(links);assert(await start.isEnabled());
  await page.getByRole('textbox').fill(links+'\nhttps://drive.google.com/uc?id=overflow');assert(await start.isDisabled());
  await page.getByRole('textbox').fill('https://drive.google.com/uc?id=synthetic&export=download');
  await page.screenshot({path:process.env.DRIVE_UI_SCREENSHOT || 'drive-account-modal.png'});
  await start.click();await page.getByRole('button',{name:'Отменить импорт',exact:true}).waitFor();
  assert.equal(await page.getByRole('textbox').count(),0);
  assert.equal(await page.evaluate(()=>window.__driveCalls.length),1);
  await page.getByRole('button',{name:'Отменить импорт',exact:true}).click();
  await page.getByText('Импорт завершён.',{exact:false}).waitFor();
  await page.keyboard.press('Escape');assert.equal(await dialog.count(),0);
  assert.equal(await button.evaluate(n=>document.activeElement===n),true);
  assert.deepEqual(errors,[]);
  console.log('PASS: exact label, modal, invalid URL, 100/101 limit, submission, progress, cancellation, Escape, focus restoration');
 } finally {await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
