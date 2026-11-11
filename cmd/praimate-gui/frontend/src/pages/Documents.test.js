import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { compile } from 'svelte/compiler'

const source=await readFile(new URL('./Documents.svelte',import.meta.url),'utf8')
let script=source.match(/<script>([\s\S]*?)<\/script>/)[1].replace(/^\s*import .*?\.svelte['"]\s*$/gm,'')
for(const name of ['form','mcpServers'])script=script.replace(`let ${name} =`,`export let ${name} =`)
for(const name of ['load','openNew','agentChanged','launch'])script=script.replace(`async function ${name}(`,`export async function ${name}(`)
const {js}=compile(`<script>${script}</script>`,{generate:'dom',accessors:true})
const code=js.code.replace(/(["'])(svelte(?:\/[^"']+)?|\.\.\/lib\/[^"']+)\1/g,(_,quote,name)=>JSON.stringify(name.startsWith('svelte')?import.meta.resolve(name):new URL(name,import.meta.url).href))
const {default:Documents}=await import('data:text/javascript;base64,'+Buffer.from(code).toString('base64'))

function fixture(t,failSelection=false){
  const previous=globalThis.window,calls=[]
  const servers=[{id:'documentation-id',name:'Human readable documentation',enabled:true},{id:'disabled-id',name:'Disabled server',enabled:false}]
  globalThis.window={go:{main:{App:{
    ListChats:async()=>[],ListAgents:async()=>[{id:'persona',mcp_servers:['documentation-id']}],
    ListCLIs:async()=>[{id:'codex',available:true}],LocalLLMModels:async()=>({configured:false}),ListCLIModels:async()=>[],
    MCPServers:async()=>servers,PreflightExecution:async()=>({ok:true,issues:[]}),
    PrepareStudioChat:async()=>{calls.push(['prepare']);return 'created-chat'},
    SetChatMCPServers:async(id,ids)=>{calls.push(['mcp',id,ids]);if(failSelection)throw Error('MCP no longer available')},
    OpenEditorWindow:async()=>calls.push(['open']),
  }}}}
  const component=new Documents({target:{}})
  t.after(()=>{component.$destroy();globalThis.window=previous})
  return {component,calls}
}

test('Documents exposes enabled MCPs at creation and applies their IDs before opening the editor',async t=>{
  const {component,calls}=fixture(t)
  await component.load();await component.openNew()
  assert.deepEqual(component.mcpServers.map(server=>server.id),['documentation-id'])
  component.form.folder='/project';component.form.mcps=['documentation-id']
  await component.launch()
  assert.deepEqual(calls,[['prepare'],['mcp','created-chat',['documentation-id']],['open']])
})

test('clearing persona MCP defaults creates an explicitly empty session selection',async t=>{
  const {component,calls}=fixture(t)
  await component.load();await component.openNew();component.form.agentID='persona';await component.agentChanged()
  assert.deepEqual(component.form.mcps,['documentation-id'])
  component.form.folder='/project';component.form.mcps=[]
  await component.launch()
  assert.deepEqual(calls.find(call=>call[0]==='mcp'),['mcp','created-chat',[]])
})

test('Documents does not launch an editor if its MCP selection could not be saved',async t=>{
  const {component,calls}=fixture(t,true)
  await component.load();await component.openNew();component.form.folder='/project';component.form.mcps=['documentation-id']
  await component.launch()
  assert.equal(calls.some(call=>call[0]==='open'),false)
  assert.equal(component.form.busy,false)
})
