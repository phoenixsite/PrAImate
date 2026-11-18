import assert from 'node:assert/strict'
import fs from 'node:fs/promises'

// Real Studio Svelte DOM with a synthetic native bridge; no editor or accounts.
const socket=new WebSocket(process.env.BIDI_URL || 'ws://127.0.0.1:9240/session')
await new Promise((resolve,reject)=>{socket.onopen=resolve;socket.onerror=reject})
let sequence=0
const pending=new Map()
socket.onmessage=event=>{const m=JSON.parse(event.data);if(!m.id)return;const p=pending.get(m.id);pending.delete(m.id);m.type==='error'?p.reject(Error(JSON.stringify(m))):p.resolve(m.result)}
const call=(method,params={})=>new Promise((resolve,reject)=>{const id=++sequence;pending.set(id,{resolve,reject});socket.send(JSON.stringify({id,method,params}))})
await call('session.new',{capabilities:{alwaysMatch:{}}})
const {context}=await call('browsingContext.create',{type:'tab'})
const evaluate=async expression=>{
  const r=await call('script.evaluate',{expression:`(async()=>JSON.stringify(await (${expression})))()`,target:{context},awaitPromise:true})
  if(r.type==='exception')throw Error(r.exceptionDetails.text)
  return r.result.type==='undefined'?undefined:JSON.parse(r.result.value)
}
const until=async expression=>{for(let n=0;n<100;n++){if(await evaluate(expression))return;await new Promise(r=>setTimeout(r,100))}throw Error('Missing state: '+expression)}
const output=process.env.REVIEW_TEST_OUTPUT || '/tmp/praimate-studio-extensions-browser'
await fs.mkdir(output,{recursive:true})
const screenshot=async name=>{const r=await call('browsingContext.captureScreenshot',{context,origin:'viewport'});await fs.writeFile(`${output}/${name}.png`,Buffer.from(r.data,'base64'))}
try {
  await call('browsingContext.navigate',{context,url:process.env.REVIEW_TEST_URL || 'http://127.0.0.1:5190/tests/fixtures/issue-review.html',wait:'complete'})
  await until('!!window.issueReview')
  for(const theme of ['light','dark'])for(const width of [430,1280]){
    await call('browsingContext.setViewport',{context,viewport:{width,height:900},devicePixelRatio:1})
    await evaluate(`(()=>{document.documentElement.classList.toggle('light',${theme==='light'});window.issueReview.show('studio');})()`)
    await until("!!document.querySelector('#studio-extensions') && !document.querySelector('#studio-extensions').matches(':disabled')")
    await evaluate("(()=>{document.querySelector('.environment').open=true;document.querySelector('#studio-extensions').scrollIntoView({block:'center'});})()")
    assert.equal(await evaluate('document.documentElement.scrollWidth<=innerWidth'),true,'Studio preferences overflow the viewport')
    await evaluate("(()=>{const input=document.querySelector('#studio-extensions');input.value='/personal/extensions';input.dispatchEvent(new Event('input',{bubbles:true}));[...document.querySelectorAll('.extensions-settings button')].find(b=>b.textContent==='Save and refresh').click()})()")
    await until("window.issueReview.calls.some(c=>c.method==='StudioSaveEditorPreferences' && c.args[0]==='/personal/extensions') && !document.querySelector('#studio-extensions').matches(':disabled')")
    assert.equal(await evaluate("document.querySelector('#studio-extensions').value"),'/personal/extensions')
    await screenshot(`${theme}-${width}`)
    await evaluate("[...document.querySelectorAll('.extensions-settings button')].find(b=>b.textContent==='Use managed folder').click()")
    await until("document.querySelector('#studio-extensions').value==='' && !document.querySelector('#studio-extensions').matches(':disabled')")
  }
  console.log('PASS: Studio extension preferences render without horizontal overflow in both themes at mobile/desktop widths; native-bridge save and managed reset work.')
}catch(error){await screenshot('failure');throw error}
finally{try{await call('browsingContext.close',{context})}finally{await call('session.end');socket.close()}}
