import assert from 'node:assert/strict'
import fs from 'node:fs/promises'

// Run Vite and a fresh Firefox headless profile with remote-debugging-port=9235.
const socket=new WebSocket(process.env.BIDI_URL || 'ws://127.0.0.1:9235/session')
await new Promise((resolve,reject)=>{socket.onopen=resolve;socket.onerror=reject})
let sequence=0
const pending=new Map()
socket.onmessage=event=>{const message=JSON.parse(event.data);if(!message.id){if(message.method==='log.entryAdded'&&message.params.level==='error')console.error(message.params.text);return;}const request=pending.get(message.id);pending.delete(message.id);message.type==='error'?request.reject(Error(JSON.stringify(message))):request.resolve(message.result)}
const call=(method,params={})=>new Promise((resolve,reject)=>{const id=++sequence;pending.set(id,{resolve,reject});socket.send(JSON.stringify({id,method,params}))})
await call('session.new',{capabilities:{alwaysMatch:{}}})
await call('session.subscribe',{events:['log.entryAdded']})
const {context}=await call('browsingContext.create',{type:'tab'})
const evaluate=async expression=>{const response=await call('script.evaluate',{expression:`(async()=>JSON.stringify(await (${expression})))()`,target:{context},awaitPromise:true});if(response.type==='exception')throw Error(response.exceptionDetails.text);return response.result.type==='undefined'?undefined:JSON.parse(response.result.value)}
const until=async expression=>{for(let attempt=0;attempt<100;attempt++){if(await evaluate(expression))return;await new Promise(resolve=>setTimeout(resolve,100))}throw Error('Missing state: '+expression)}
const base=process.env.REVIEW_TEST_URL || 'http://127.0.0.1:5188/tests/fixtures/issue-review.html'
const output=process.env.REVIEW_TEST_OUTPUT || '/tmp/praimate-issue-review-browser'
await fs.mkdir(output,{recursive:true})
const screenshot=async name=>{const result=await call('browsingContext.captureScreenshot',{context,origin:'viewport'});await fs.writeFile(`${output}/${name}.png`,Buffer.from(result.data,'base64'))}
async function hover(selector){
 const point=await evaluate(`(()=>{const r=document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect();return {x:Math.round(r.left+r.width/2),y:Math.round(r.top+r.height/2)}})()`)
 await call('input.performActions',{context,actions:[{type:'pointer',id:'mouse',parameters:{pointerType:'mouse'},actions:[{type:'pointerMove',x:point.x,y:point.y,duration:0}]}]})
 // Existing component transitions should reach their final color.
 await new Promise(resolve=>setTimeout(resolve,180))
}
const colors=selector=>evaluate(`(()=>{const s=getComputedStyle(document.querySelector(${JSON.stringify(selector)}));return {background:s.backgroundColor,color:s.color}})()`)
try{
 await call('browsingContext.setViewport',{context,viewport:{width:1280,height:900},devicePixelRatio:1})
 await call('browsingContext.navigate',{context,url:base,wait:'complete'})
 await until('!!window.issueReview')
 for(const theme of ['dark','light']){
  await evaluate(`document.documentElement.classList.toggle('light',${theme==='light'})`)
  for(const mode of ['graph','monitor']){
   await evaluate(`window.issueReview.show(${JSON.stringify(mode)})`)
   const selector=mode==='graph'?'.graph .btn.primary':'.plan-ready .btn.primary'
   // WorkerGraph's root class is intentionally not part of its public props.
   const button=mode==='graph'?'button.btn.primary':selector
   await until(`!!document.querySelector(${JSON.stringify(button)})`)
   await hover(button)
   const rendered=await colors(button)
   const expected=await evaluate(`(()=>{const e=document.createElement('span');e.style.background='var(--accent)';e.style.color='var(--accent-fg)';document.body.appendChild(e);const s=getComputedStyle(e),result={background:s.backgroundColor,color:s.color};e.remove();return result})()`)
   assert.deepEqual(rendered,expected,`${theme} ${mode}: hover lost the primary color pair`)
   await screenshot(`${theme}-${mode}`)
  }
 }
 await evaluate("window.issueReview.show('documents')")
 await until("[...document.querySelectorAll('button')].some(button=>button.textContent.includes('New Document Session'))")
 await evaluate("[...document.querySelectorAll('button')].find(button=>button.textContent.includes('New Document Session')).click()")
 await until("!!document.querySelector('input[type=checkbox][value=\"docs-id\"]')")
 assert.equal(await evaluate("!!document.querySelector('input[value=disabled]')"),false)
 await evaluate("(()=>{const c=document.querySelector('input[value=\"docs-id\"]');c.checked=true;c.dispatchEvent(new Event('change',{bubbles:true}));[...document.querySelectorAll('button')].find(button=>button.textContent.includes('Browse')).click()})()")
 await until("!!document.querySelector('input[value=\"/project\"]') || [...document.querySelectorAll('input')].some(input=>input.value==='/project')")
 await screenshot('documents-mcp')
 await evaluate("[...document.querySelectorAll('button')].find(button=>button.textContent.trim()==='Open Document Session').click()")
 await until("window.issueReview.calls.some(call=>call.method==='OpenEditorWindow')")
 assert.deepEqual(await evaluate("window.issueReview.calls.filter(call=>['PrepareStudioChat','SetChatMCPServers','OpenEditorWindow'].includes(call.method)).map(call=>call.method)"),['PrepareStudioChat','SetChatMCPServers','OpenEditorWindow'])
 assert.deepEqual(await evaluate("window.issueReview.calls.find(call=>call.method==='SetChatMCPServers').args"),['created-chat',['docs-id']])
 // Render Studio's actual stylesheet in an isolated document with theme tokens.
 const studio=await fs.readFile(new URL('../../../../internal/studio/extension/resources/chat.html',import.meta.url),'utf8')
 const style=studio.match(/<style>([\s\S]*?)<\/style>/)[1]
 for(const theme of ['dark','light']){
  const html=`<!doctype html><style>:root{--vscode-button-background:${theme==='dark'?'#0e639c':'#007acc'};--vscode-button-hoverBackground:#005a9e;--vscode-button-foreground:#fff;--vscode-button-secondaryBackground:#333;--vscode-button-secondaryHoverBackground:#444;--vscode-button-secondaryForeground:#ccc;--vscode-widget-border:#555}</style><style>${style}</style><button class="primary">Run task graph</button>`
  await call('browsingContext.navigate',{context,url:'data:text/html,'+encodeURIComponent(html),wait:'complete'})
  await hover('button.primary')
  assert.deepEqual(await colors('button.primary'),{background:'rgb(0, 90, 158)',color:'rgb(255, 255, 255)'})

 }
 console.log('PASS: Desktop worker hover in both themes, Studio hover in both themes, and Documents MCP selection before editor launch.')
}catch(error){await screenshot('failure');throw error}
finally{try{await call('browsingContext.close',{context})}finally{await call('session.end');socket.close()}}
