import assert from 'node:assert/strict'
import fs from 'node:fs/promises'

// Real Svelte and Studio DOM; synthetic data never opens an account or CLI.
const socket = new WebSocket(process.env.BIDI_URL || 'ws://127.0.0.1:9235/session')
await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject })
let sequence = 0
const pending = new Map()
socket.onmessage = event => {
  const message = JSON.parse(event.data)
  if (!message.id) return
  const request = pending.get(message.id)
  pending.delete(message.id)
  message.type === 'error' ? request.reject(Error(JSON.stringify(message))) : request.resolve(message.result)
}
const call = (method, params = {}) => new Promise((resolve, reject) => {
  const id = ++sequence; pending.set(id, { resolve, reject }); socket.send(JSON.stringify({ id, method, params }))
})
await call('session.new', { capabilities: { alwaysMatch: {} } })
const { context } = await call('browsingContext.create', { type: 'tab' })
const evaluate = async expression => {
  const response = await call('script.evaluate', { expression: `(async()=>JSON.stringify(await (${expression})))()`, target: { context }, awaitPromise: true })
  if (response.type === 'exception') throw Error(response.exceptionDetails.text)
  return response.result.type === 'undefined' ? undefined : JSON.parse(response.result.value)
}
const execute = source => evaluate(`(()=>{${source}})()`)
const until = async expression => {
  for (let attempt = 0; attempt < 100; attempt++) {
    if (await evaluate(expression)) return
    await new Promise(resolve => setTimeout(resolve, 100))
  }
  throw Error('Missing state: ' + expression)
}
const base = process.env.REVIEW_TEST_URL || 'http://127.0.0.1:5188/tests/fixtures/issue-review.html'
const output = process.env.REVIEW_TEST_OUTPUT || '/tmp/praimate-worker-review-browser'
await fs.mkdir(output, { recursive: true })
const screenshot = async name => {
  const result = await call('browsingContext.captureScreenshot', { context, origin: 'viewport' })
  await fs.writeFile(`${output}/${name}.png`, Buffer.from(result.data, 'base64'))
}
const task = (id, review = 'pending') => ({ id, description: id, status: 'completed', dependencies: [], worker: { profile: 'middle' }, review, result: { baseCommit: 'aaaaaaa', resultCommit: 'bbbbbbb', changedFiles: [id + '.txt'], diff: 'Saved diff of ' + id } })
try {
  await call('browsingContext.setViewport', { context, viewport: { width: 1200, height: 700 }, devicePixelRatio: 1 })
  await call('browsingContext.navigate', { context, url: base, wait: 'complete' })
  await until('!!window.issueReview')
  for (const theme of ['dark', 'light']) {
    const snapshot = { id: 'browser-run', status: 'draft', task: 'Review browser results', profiles: [], events: [], dag: { targetBranch: 'main', tasks: [task('one'), task('two', 'rejected')], maxParallel: 2 } }
    await execute(`document.documentElement.classList.toggle('light',${theme === 'light'});window.issueReview.show('monitor',${JSON.stringify(snapshot)});`)
    await until("!!document.querySelector('.review-bar')")
    assert.equal(await evaluate("document.querySelector('.review-bar button').disabled"), true)
    await execute("[...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='Review plan').click();")
    await until("document.activeElement?.classList.contains('plan-anchor') && document.activeElement.getBoundingClientRect().top < innerHeight")
    assert.equal(await evaluate("document.activeElement.getAttribute('aria-label')"), 'Plan, routes and integration')
    await screenshot(`${theme}-plan-reveal`)
    snapshot.status = 'review'
    snapshot.dag.tasks = [task('one', 'rejected'), task('two', 'rejected')]
    await execute(`window.issueReview.show('monitor',${JSON.stringify(snapshot)});`)
    await until("!!document.querySelector('.review-bar')")
    assert.match(await evaluate("document.querySelector('.review-bar').textContent"), /0 accepted · 2 rejected/)
    assert.equal(await evaluate("document.querySelector('.review-bar button').disabled"), true)
  }

  const studio = await fs.readFile(new URL('../../../../internal/studio/extension/resources/chat.html', import.meta.url), 'utf8')
  const prelude = `<script>window.sent=[];window.acquireVsCodeApi=()=>({postMessage:m=>sent.push(m),getState:()=>({}),setState:s=>{window.savedState=s}});window.receive=m=>window.dispatchEvent(new MessageEvent('message',{data:m}));</script>`
  const tokens = `<style>:root{--vscode-font-family:sans-serif;--vscode-foreground:#eee;--vscode-sideBar-background:#17191c;--vscode-descriptionForeground:#aaa;--vscode-widget-border:#555;--vscode-button-background:#0e639c;--vscode-button-foreground:#fff;--vscode-button-secondaryBackground:#292c31;--vscode-input-background:#25282d;--vscode-input-foreground:#eee;--vscode-focusBorder:#58a6ff;--vscode-errorForeground:#f14c4c;--vscode-editorWidget-background:#25282d}</style>`
  const html = studio.replace('__RESOURCE_SOURCE__', "'none'").replace('<script>', prelude + '<script>').replace('</head>', tokens + '</head>')
  await call('browsingContext.setViewport', { context, viewport: { width: 420, height: 900 }, devicePixelRatio: 1 })
  await call('browsingContext.navigate', { context, url: 'data:text/html,' + encodeURIComponent(html), wait: 'complete' })
  await until('window.sent?.some(m=>m.type==="ready")')
  const status = { connected: true, activeCLI: 'claude', activeTools: 'safe', models: ['stored-model'], availableCLIs: [{ id: 'claude', label: 'Claude', available: true }, { id: 'codex', label: 'Codex', available: true }] }
  await execute(`receive({type:'status',status:${JSON.stringify(status)}});navigate('settings');document.getElementById('cli-select').value='codex';document.getElementById('cli-select').dispatchEvent(new Event('change',{bubbles:true}));`)
  const request = await evaluate("sent.filter(m=>m.type==='sessionModels').at(-1)")
  await execute(`receive({type:'sessionModels',cli:'codex',requestId:${request.requestId},models:['correct-model']});`)
  assert.deepEqual(await evaluate("[...document.getElementById('model-list').children].map(o=>o.value)"), ['correct-model'])

  const profiles = ['primary', 'middle', 'fast'].map(tier => ({ tier, runtime: 'cli', cli: 'codex', model: 'fixture' }))
  const snapshot = n => ({ id: 'run', updatedAt: String(n), status: 'running', task: 'Streaming tasks', profiles, events: [{ workerID: 'att', sequence: n, kind: 'stream', text: 'chunk-' + n, timestamp: new Date().toISOString() }], attempts: [{ id: 'att', taskID: 'task', tier: 'middle', cli: 'codex', model: 'fixture', status: 'running' }], dag: { tasks: [{ id: 'task', description: 'task', status: 'running', worker: { profile: 'middle' }, dependencies: [] }], maxParallel: 2 } })
  const send = async snap => execute(`receive({type:'workerSnapshot',selectedId:'run',runs:[{id:'run',status:${JSON.stringify(snap.status)}}],snapshot:${JSON.stringify(snap)}});`)
  await execute("navigate('workers');")
  await send(snapshot(1))
  await until("!!document.querySelector('.worker-board')")
  await evaluate("(()=>{const s=document.getElementById('worker-board-search');s.value='task';s.dispatchEvent(new Event('input'));window.keptBoard=document.querySelector('.worker-board');window.keptSearch=document.getElementById('worker-board-search');keptSearch.focus();keptSearch.setSelectionRange(2,2);keptBoard.scrollLeft=150;})()")
  for (let n = 2; n <= 5; n++) await send(snapshot(n))
  assert.deepEqual(await evaluate("({same:keptBoard===document.querySelector('.worker-board'),left:keptBoard.scrollLeft,focused:document.activeElement===keptSearch,caret:keptSearch.selectionStart,updated:document.querySelector('.worker-console').textContent.includes('chunk-5')})"), { same: true, left: 150, focused: true, caret: 2, updated: true })
  await screenshot('studio-stream-retains-board')

  const completed = { ...snapshot(6), status: 'review', dag: { tasks: [task('task'), task('task-two', 'accepted')], maxParallel: 2 }, attempts: [] }
  await send(completed)
  assert.equal(await evaluate("document.querySelector('.worker-board').scrollLeft"), 150)
  await evaluate("(()=>{const c=[...document.querySelectorAll('.worker-console button')].find(b=>b.textContent==='Changes');c.click();[...document.querySelectorAll('.worker-task-card')].find(b=>b.textContent.includes('task-two')).click()})()")
  assert.equal(await evaluate("[...document.querySelectorAll('.worker-console button')].find(b=>b.textContent==='Changes').getAttribute('aria-pressed')"), 'true')
  assert.equal(await evaluate("document.querySelector('.worker-review-bar button').disabled"), false)
  assert.match(await evaluate("document.querySelector('.worker-console').textContent"), /Saved diff of task-two/)
  await screenshot('studio-review-actions')
  completed.updatedAt = '7'
  completed.dag.tasks = [task('task', 'rejected'), task('task-two', 'rejected')]
  await send(completed)
  assert.match(await evaluate("document.querySelector('.worker-review-bar').textContent"), /0 accepted · 2 rejected/)
  assert.equal(await evaluate("document.querySelector('.worker-review-bar button').disabled"), true)
  console.log('PASS: real Desktop plan focus in both themes; Studio selected-CLI models; stable board, search focus and horizontal scroll while streaming; completed-task tabs; visible review/merge actions.')
} catch (error) {
  await screenshot('failure')
  throw error
} finally {
  try { await call('browsingContext.close', { context }) } finally { await call('session.end'); socket.close() }
}
