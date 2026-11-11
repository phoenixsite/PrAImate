import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { compile } from 'svelte/compiler'

// Exercise the component's real execution handler without a DOM dependency.
const source = await readFile(new URL('./WorkerGraph.svelte',import.meta.url),'utf8')
const script = source.match(/<script>([\s\S]*?)<\/script>/)[1].replace('let tasks =','export let tasks =')
const { js } = compile(`<script>${script}</script>`,{generate:'dom',accessors:true})
const code = js.code.replace(/(["'])(svelte(?:\/[^"']+)?|\.\/[^"']+)\1/g,(_,quote,name)=>JSON.stringify(name.startsWith('svelte')?import.meta.resolve(name):new URL(name,import.meta.url).href))
const { default: WorkerGraph } = await import('data:text/javascript;base64,'+Buffer.from(code).toString('base64'))

function fixture(t, save) {
  const previous = globalThis.window, calls=[]
  globalThis.window={go:{main:{App:{
    SaveWorkerDAG:async(...args)=>{calls.push(['save',...args]);await save?.()},
    ExecuteWorkerDAG:async(...args)=>calls.push(['execute',...args]),
  }}}}
  const graph=new WorkerGraph({target:{},props:{snapshot:{id:'run',status:'draft',profiles:[{tier:'middle',runtime:'cli',cli:'codex'}],dag:{tasks:[{id:'task-a',description:'Original',worker:{profile:'middle'}}],maxParallel:2}}}})
  t.after(()=>{graph.$destroy();globalThis.window=previous})
  return {graph,calls}
}

test('primary worker buttons use the shared btn primary style so hover keeps the accent colours', () => {
  // The generic button:hover rule sets a dark raised background; global button.btn.primary:hover keeps the accent background readable.
  assert.match(source, /<button class="btn primary" disabled=\{busy \|\| !tasks\.length\}[\s\S]*?>Run task graph<\/button>/)
  assert.match(source, /<button class="btn primary" disabled=\{busy\} on:click=\{\(\) => action\(async/)
  assert.match(source, /\.primary:hover:not\(:disabled\)\s*\{[^}]*background: var\(--accent\)/)
})
test('running a plan saves current edited assignments before executing once',async t=>{
  let complete
  const {graph,calls}=fixture(t,()=>new Promise(resolve=>{complete=resolve}))
  graph.tasks[0].description='Edited task'
  const pending=graph.execute()
  await graph.execute()
  assert.equal(calls.length,1)
  assert.equal(JSON.parse(calls[0][2])[0].description,'Edited task')
  complete();await pending
  assert.deepEqual(calls.map(call=>call[0]),['save','execute'])
  assert.equal(calls[1][1],'run')
})

test('a rejected plan save does not start workers',async t=>{
  const {graph,calls}=fixture(t,()=>{throw Error('invalid dependencies')})
  await graph.execute()
  assert.deepEqual(calls.map(call=>call[0]),['save'])
})
