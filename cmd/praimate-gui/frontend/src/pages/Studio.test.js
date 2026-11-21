import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { compile } from 'svelte/compiler'
import { confirmModal } from '../lib/stores.js'

test('duplicate open requests create only one installation confirmation', async () => {
  const source = await readFile(new URL('./Studio.svelte', import.meta.url), 'utf8')
  const script = source.match(/<script>([\s\S]*?)<\/script>/)[1]
    .replace('const session =', 'export const session =')
    .replace('async function openProject(', 'export async function openProject(')
  // Run the real script with Svelte's reactive updates. An empty fragment
  // isolates the confirmation flow from the browser and native editor.
  const { js } = compile(`<script>${script}</script>`, { generate: 'dom', accessors: true })
  const code = js.code.replace(/(["'])(svelte(?:\/[^"']+)?|\.\.\/lib\/[^"']+)\1/g, (_, quote, name) =>
    JSON.stringify(name.startsWith('svelte') ? import.meta.resolve(name) : new URL(name, import.meta.url).href))
  const { default: Studio } = await import('data:text/javascript;base64,' + Buffer.from(code).toString('base64'))
  const previous = globalThis.window
  globalThis.window = { go: { main: { App: {
    StudioGetStatus: async () => ({ state: 'not_installed' }),
    StudioListRecentProjects: async () => [],
  } } } }
  const confirmations = []
  const unsubscribe = confirmModal.subscribe(value => { if (value) confirmations.push(value) })
  const component = new Studio({ target: {} })
  let requests = []
  try {
    await component.session.refresh()
    requests = [component.openProject('/tmp/one'), component.openProject('/tmp/two')]
    assert.equal(confirmations.length, 1)
  } finally {
    for (const confirmation of confirmations) confirmation.resolve(false)
    await Promise.all(requests)
    component.$destroy()
    unsubscribe()
    globalThis.window = previous
  }
})

async function preferencesComponent(host) {
  const source = await readFile(new URL('./Studio.svelte', import.meta.url), 'utf8')
  const script = source.match(/<script>([\s\S]*?)<\/script>/)[1]
    .replace('const session =', 'export const session =')
    .replace('let extensionsDirectory =', 'export let extensionsDirectory =')
    .replace('async function loadEditorPreferences(', 'export async function loadEditorPreferences(')
    .replace('async function saveEditorPreferences(', 'export async function saveEditorPreferences(')
  const { js } = compile(`<script>${script}</script>`, { generate: 'dom', accessors: true })
  const code = js.code.replace(/(["'])(svelte(?:\/[^"']+)?|\.\.\/lib\/[^"']+)\1/g, (_, quote, name) =>
    JSON.stringify(name.startsWith('svelte') ? import.meta.resolve(name) : new URL(name, import.meta.url).href))
  const { default: Studio } = await import('data:text/javascript;base64,' + Buffer.from(code).toString('base64'))
  globalThis.window = { go: { main: { App: host } } }
  return new Studio({ target: {} })
}

test('Studio extension preferences load, save once, report errors and restore the managed folder', async () => {
  const previous = globalThis.window
  const calls = []
  let finish
  const host = {
    StudioGetStatus: async () => ({ state: 'installed' }),
    StudioListRecentProjects: async () => [],
    StudioGetEditorPreferences: async () => ({ extensionsDirectory: '/personal', managedDirectory: '/managed' }),
    StudioSaveEditorPreferences: directory => { calls.push(directory); return new Promise(resolve => { finish = () => resolve({ extensionsDirectory: directory, managedDirectory: '/managed' }) }) },
  }
  const c = await preferencesComponent(host)
  try {
    await c.session.refresh()
    await c.loadEditorPreferences()
    assert.equal(c.extensionsDirectory, '/personal')
    c.extensionsDirectory = ' /another folder '
    const first = c.saveEditorPreferences(), second = c.saveEditorPreferences()
    assert.deepEqual(calls, ['/another folder'])
    finish(); await Promise.all([first, second])
    assert.equal(c.extensionsDirectory, '/another folder')
    host.StudioSaveEditorPreferences = async () => { throw new Error('Read-only directory') }
    await c.saveEditorPreferences()
    assert.match(c.extensionsError, /Read-only directory/)
    host.StudioSaveEditorPreferences = async directory => { calls.push(directory); return { extensionsDirectory: directory, managedDirectory: '/managed' } }
    c.extensionsDirectory = ''
    await c.saveEditorPreferences()
    assert.equal(calls.at(-1), '')
    assert.equal(c.extensionsError, '')
  } finally { c.$destroy(); globalThis.window = previous }
})

test('remote Studio never reads or changes native extensions folders', async () => {
  const previous = globalThis.window
  const c = await preferencesComponent({
    StudioGetStatus: async () => ({ state: 'installed', remote: true }),
    StudioListRecentProjects: async () => [],
    StudioGetEditorPreferences: () => { throw new Error('native preferences requested') },
    StudioSaveEditorPreferences: () => { throw new Error('native preferences changed') },
  })
  try {
    await c.session.refresh()
    await c.loadEditorPreferences()
    await c.saveEditorPreferences()
    assert.equal(c.extensionsError, '')
  } finally { c.$destroy(); globalThis.window = previous }
})

test('recent projects reopen with their saved configuration and confirm full access', async () => {
  const source = await readFile(new URL('./Studio.svelte', import.meta.url), 'utf8')
  const script = source.match(/<script>([\s\S]*?)<\/script>/)[1]
    .replace('const session =', 'export const session =')
    .replace('function openRecent(', 'export function openRecent(')
  const { js } = compile(`<script>${script}</script>`, { generate: 'dom', accessors: true })
  const code = js.code.replace(/(["'])(svelte(?:\/[^"']+)?|\.\.\/lib\/[^"']+)\1/g, (_, quote, name) =>
    JSON.stringify(name.startsWith('svelte') ? import.meta.resolve(name) : new URL(name, import.meta.url).href))
  const { default: Studio } = await import('data:text/javascript;base64,' + Buffer.from(code).toString('base64'))
  const previous = globalThis.window
  const launches = []
  globalThis.window = { go: { main: { App: {
    StudioGetStatus: async () => ({ state: 'installed' }),
    StudioListRecentProjects: async () => [],
    StudioOpenProject: async (...args) => { launches.push(args) },
  } } } }
  const confirmations = []
  const unsubscribe = confirmModal.subscribe(value => { if (value) confirmations.push(value) })
  const component = new Studio({ target: {} })
  try {
    await component.session.refresh()
    component.openRecent({ path: '/tmp/saved', cli: 'claude', model: 'sonnet', agentID: 'rev', tools: 'edits', configured: true })
    await new Promise(resolve => setTimeout(resolve, 10))
    assert.deepEqual(launches, [['/tmp/saved', 'claude', 'sonnet', 'rev', 'edits']])
    assert.equal(confirmations.length, 0)

    component.openRecent({ path: '/tmp/full', cli: 'codex', model: '', agentID: '', tools: 'full', configured: true })
    await new Promise(resolve => setTimeout(resolve, 10))
    assert.equal(confirmations.length, 1)
    assert.equal(launches.length, 1, 'full access must not launch before confirmation')
    confirmations[0].resolve(true)
    await new Promise(resolve => setTimeout(resolve, 10))
    assert.deepEqual(launches[1], ['/tmp/full', 'codex', '', '', 'full'])
  } finally {
    for (const confirmation of confirmations) confirmation.resolve(false)
    component.$destroy()
    unsubscribe()
    globalThis.window = previous
  }
})
