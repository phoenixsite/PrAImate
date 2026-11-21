import test from 'node:test'
import assert from 'node:assert/strict'
import { createStudioSession, studioProjectName } from './studioSession.js'

function deferred() {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const ready = { state: 'installed', version: 'test' }
function fixture(overrides = {}) {
  return {
    studioGetStatus: async () => ready,
    studioListRecentProjects: async () => ['/tmp/project'],
    listCLIs: async () => [{ id: 'praimate-code', available: true }],
    listAgents: async () => [], listCLIModels: async () => [], refreshCLIModels: async () => [],
    studioInstall: async () => ready, studioUpdate: async () => ready, studioRepair: async () => ready,
    studioOpenProject: async () => {}, ...overrides,
  }
}
const config = { workspacePath: '/tmp/project', cli: 'codex', model: 'test-model', agentID: 'test-agent', tools: 'full' }

test('opening a project refreshes extension status after launch repairs it', async () => {
  let installed = false
  const session = createStudioSession(fixture({
    studioGetStatus: async () => ({state: installed ? 'installed' : 'update_available'}),
    studioOpenProject: async () => { installed = true },
  }))
  await session.refresh()
  assert.equal(await session.launch(config), true)
  assert.equal(session.state.status.state, 'installed')
})

test('overview never probes CLIs and shows projects while status is still pending', async () => {
  const status = deferred()
  let probes = 0
  const session = createStudioSession(fixture({ studioGetStatus: () => status.promise, listCLIs: () => { probes++; return new Promise(() => {}) } }))
  const load = session.refresh()
  await Promise.resolve()
  assert.deepEqual(session.state.projects, [{ path: '/tmp/project', cli: '', model: '', agentID: '', tools: '', configured: false }])
  assert.equal(session.state.loadingProjects, false)
  assert.equal(session.state.loadingStatus, true)
  assert.equal(probes, 0)
  status.resolve(ready)
  await load
})

test('an old status check cannot undo the state refreshed after launch', async () => {
  const oldStatus = deferred()
  let calls = 0
  const session = createStudioSession(fixture({
    studioGetStatus: () => ++calls === 2 ? oldStatus.promise : Promise.resolve(ready),
  }))
  await session.refresh()
  const checking = session.refresh()
  assert.equal(await session.launch(config), true)
  oldStatus.resolve({state: 'update_available'})
  await checking
  assert.equal(session.state.status.state, 'installed')
})

test('a status refresh failure after opening is separate from launch failure', async () => {
  let calls = 0
  const session = createStudioSession(fixture({
    studioGetStatus: async () => { if (calls++) throw Error('status unavailable'); return ready },
  }))
  await session.refresh()
  assert.equal(await session.launch(config), true)
  assert.equal(session.state.error, '')
  assert.match(session.state.statusError, /status unavailable/)
  assert.equal(session.state.openingPath, '')
})

test('refresh and lazy catalogue requests coalesce and only cache successful results', async () => {
  const clis = deferred()
  let statusCalls = 0, cliCalls = 0
  const session = createStudioSession(fixture({ studioGetStatus: async () => { statusCalls++; return ready }, listCLIs: () => { cliCalls++; return clis.promise } }))
  await Promise.all([session.refresh(), session.refresh()])
  assert.equal(statusCalls, 1)
  const first = session.loadChoices()
  const second = session.loadChoices()
  assert.equal(first, second)
  clis.resolve([])
  await first
  await session.loadChoices()
  assert.equal(cliCalls, 1)
  await session.loadChoices(true)
  assert.equal(cliCalls, 2)
})

test('resource errors stay visible and preserve independently loaded content', async () => {
  const session = createStudioSession(fixture({ studioGetStatus: async () => { throw Error('status unavailable') } }))
  await session.refresh()
  assert.match(session.state.statusError, /status unavailable/)
  assert.equal(session.state.status, null)
  assert.deepEqual(session.state.projects, [{ path: '/tmp/project', cli: '', model: '', agentID: '', tools: '', configured: false }])
  assert.equal(session.state.loadingStatus, false)
})

test('a failing agent catalogue preserves usable CLIs and can be retried', async () => {
  let attempts = 0
  const session = createStudioSession(fixture({ listAgents: async () => { if (!attempts++) throw Error('agents unavailable'); return [{ id: 'agent' }] } }))
  await session.loadChoices()
  assert.equal(session.state.clis[0].id, 'praimate-code')
  assert.match(session.state.choicesError, /agents unavailable/)
  await session.loadChoices()
  assert.equal(session.state.agents[0].id, 'agent')
  assert.equal(session.state.choicesError, '')
})

test('failed installation and missing runtime cannot launch an editor', async () => {
  let launches = 0
  const session = createStudioSession(fixture({ studioGetStatus: async () => ({ state: 'not_installed' }), studioInstall: async () => { throw Error('download failed') }, studioOpenProject: async () => { launches++ } }))
  await session.refresh()
  assert.equal(await session.maintain('install'), false)
  assert.match(session.state.error, /download failed/)
  assert.equal(await session.launch(config), false)
  assert.equal(launches, 0)
})

test('maintenance does not claim success while the integration still needs refresh', async () => {
  const session = createStudioSession(fixture({studioRepair: async () => ({state: 'update_available'})}))
  assert.equal(await session.maintain('repair'), false)
  assert.match(session.state.error, /Studio is not ready/)
})

test('maintenance invalidates a stale environment check', async () => {
  const oldStatus = deferred()
  const session = createStudioSession(fixture({ studioGetStatus: () => oldStatus.promise }))
  const loading = session.refresh()
  assert.equal(await session.maintain('install'), true)
  oldStatus.resolve({ state: 'not_installed' })
  await loading
  assert.equal(session.state.status.state, 'installed')
  assert.equal(session.state.loadingStatus, false)
})

test('launch preserves configuration, rejects duplicate clicks and separates history failures', async () => {
  const opening = deferred()
  let calls = [], historyCalls = 0
  const session = createStudioSession(fixture({
    studioOpenProject: (...args) => { calls.push(args); return opening.promise },
    studioListRecentProjects: async () => { if (historyCalls++) throw Error('history unavailable'); return ['/tmp/project'] },
  }))
  await session.refresh()
  const result = session.launch(config)
  assert.equal(await session.launch(config), false)
  opening.resolve()
  assert.equal(await result, true)
  assert.deepEqual(calls, [[config.workspacePath, config.cli, config.model, config.agentID, config.tools]])
  assert.equal(session.state.error, '')
  assert.match(session.state.projectsError, /history unavailable/)
  assert.deepEqual(session.state.projects, [{ path: '/tmp/project', cli: '', model: '', agentID: '', tools: '', configured: false }])
  assert.equal(session.state.openingPath, '')
})

test('model changes discard previous CLI results, including late errors', async () => {
  const first = deferred(), second = deferred()
  const session = createStudioSession(fixture({ listCLIModels: cli => cli === 'codex' ? first.promise : second.promise }))
  const old = session.loadModels('codex')
  const current = session.loadModels('claude')
  second.resolve(['sonnet'])
  await current
  first.reject(Error('old CLI error'))
  await old
  assert.deepEqual(session.state.models, ['sonnet'])
  assert.equal(session.state.modelError, '')
  assert.equal(session.state.loadingModels, false)
})

test('disposed views neither publish late results nor start new operations', async () => {
  const status = deferred()
  const updates = []
  const session = createStudioSession(fixture({ studioGetStatus: () => status.promise }), next => updates.push(next))
  const load = session.refresh()
  session.dispose()
  const count = updates.length
  status.resolve(ready)
  await load
  assert.equal(updates.length, count)
  assert.equal(await session.launch(config), false)
  assert.equal(await session.maintain('install'), false)
})

test('project names handle Linux, Windows and root folders', () => {
  assert.equal(studioProjectName('/home/user/project/'), 'project')
  assert.equal(studioProjectName('C:\\Projects\\Example\\'), 'Example')
  assert.equal(studioProjectName('/'), '/')
})

test('recent project entries keep their saved launch configuration', async () => {
  const { normalizeRecentProjects } = await import('./studioSession.js')
  assert.deepEqual(normalizeRecentProjects([
    { path: '/tmp/a', cli: 'claude', model: 'sonnet', agentID: 'rev', tools: '', configured: true },
    '/tmp/legacy', { path: '' }, null,
  ]), [
    { path: '/tmp/a', cli: 'claude', model: 'sonnet', agentID: 'rev', tools: '', configured: true },
    { path: '/tmp/legacy', cli: '', model: '', agentID: '', tools: '', configured: false },
  ])
  assert.deepEqual(normalizeRecentProjects(null), [])
})

