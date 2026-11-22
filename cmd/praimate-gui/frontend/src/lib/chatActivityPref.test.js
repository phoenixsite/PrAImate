import assert from 'node:assert/strict'
import test from 'node:test'
import { readFile } from 'node:fs/promises'
import { compile } from 'svelte/compiler'

function memoryStorage(initial = {}) {
  const data = { ...initial }
  return { data, getItem: k => (k in data ? data[k] : null), setItem: (k, v) => { data[k] = String(v) } }
}

async function loadPref(storage) {
  globalThis.localStorage = storage
  return import('./chatActivityPref.js?' + Math.random())
}

test('activity is visible by default and the preference persists across reloads', async () => {
  const first = await loadPref(memoryStorage())
  let value
  first.hideChatActivity.subscribe(v => { value = v })()
  assert.equal(value, false)
  const storage = memoryStorage()
  const pref = await loadPref(storage)
  pref.setHideChatActivity(true)
  assert.equal(storage.data[pref.HIDE_ACTIVITY_KEY], '1')
  const reloaded = await loadPref(storage)
  assert.equal(reloaded.storedHideActivity(), true)
  reloaded.setHideChatActivity(false)
  assert.equal((await loadPref(storage)).storedHideActivity(), false)
})

test('unavailable storage falls back to visible activity', async () => {
  const pref = await loadPref({ getItem() { throw new Error('x') }, setItem() { throw new Error('x') } })
  assert.equal(pref.storedHideActivity(), false)
  pref.setHideChatActivity(true)
})

test('hidden activity collapses into a counted line without losing events', async () => {
  const source = await readFile(new URL('./ChatActivity.svelte', import.meta.url), 'utf8')
  const { js } = compile(source, { generate: 'ssr' })
  const code = js.code.replaceAll('"svelte/internal"', JSON.stringify(import.meta.resolve('svelte/internal')))
    .replace(/^(import.* from )['"]\.\/chatActivity\.js['"]/m, (_, p) => p + JSON.stringify(new URL('./chatActivity.js', import.meta.url).href))
  const { default: Activity } = await import('data:text/javascript;base64,' + Buffer.from(code).toString('base64'))
  const events = [{ type: 'tool', tool: 'bash', detail: 'ls', done: true, ok: true }, { type: 'status', detail: 'working' }]
  for (const collapsible of [true, false]) {
    const { html } = Activity.render({ events, collapsible, hidden: true })
    assert.match(html, /<summary[^>]*>2 actions hidden<\/summary>/)
    assert.doesNotMatch(html, /<details[^>]*\sopen/)
    assert.match(html, /bash/)
  }
  assert.match(Activity.render({ events, collapsible: true, hidden: false }).html, /Activity · 2 events/)
})

test('finished messages follow the toggle: expanded when shown, folded when hidden', async () => {
  const source = await readFile(new URL('./ChatActivity.svelte', import.meta.url), 'utf8')
  const { js } = compile(source, { generate: 'ssr' })
  const code = js.code.replaceAll('"svelte/internal"', JSON.stringify(import.meta.resolve('svelte/internal')))
    .replace(/^(import.* from )['"]\.\/chatActivity\.js['"]/m, (_, p) => p + JSON.stringify(new URL('./chatActivity.js', import.meta.url).href))
  const { default: Activity } = await import('data:text/javascript;base64,' + Buffer.from(code).toString('base64'))
  const events = [{ type: 'tool', tool: 'bash', detail: 'ls', done: true, ok: true }]
  assert.match(Activity.render({ events, collapsible: true, hidden: false }).html, /<details[^>]*\sopen/)
  assert.doesNotMatch(Activity.render({ events, collapsible: true, hidden: true }).html, /<details[^>]*\sopen/)
  assert.doesNotMatch(Activity.render({ events, collapsible: false, hidden: false }).html, /\sopen/)
})
