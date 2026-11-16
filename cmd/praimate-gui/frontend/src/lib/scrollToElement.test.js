import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import vm from 'node:vm'
import { prefersReducedMotion, revealElement } from './scrollToElement.js'

function fakeElement(attributes = {}) {
  const calls = []
  return {
    calls,
    hasAttribute: (name) => name in attributes,
    setAttribute: (name, value) => { attributes[name] = value; calls.push(['attr', name, value]) },
    focus: (options) => calls.push(['focus', options]),
    scrollIntoView: (options) => calls.push(['scroll', options]),
  }
}
const win = (reduce) => ({ matchMedia: (query) => ({ matches: reduce && query === '(prefers-reduced-motion: reduce)' }) })

test('reveals with a smooth scroll and moves focus without a second jump', () => {
  const element = fakeElement()
  assert.equal(revealElement(element, { win: win(false) }), true)
  assert.deepEqual(element.calls, [
    ['attr', 'tabindex', '-1'],
    ['focus', { preventScroll: true }],
    ['scroll', { behavior: 'smooth', block: 'start' }],
  ])
})

test('reduced motion jumps instantly and an existing tabindex is kept', () => {
  const element = fakeElement({ tabindex: '0' })
  revealElement(element, { win: win(true) })
  assert.deepEqual(element.calls.map((call) => call[0]), ['focus', 'scroll'])
  assert.equal(element.calls.at(-1)[1].behavior, 'auto')
})

test('a missing element and a missing matchMedia are handled', () => {
  assert.equal(revealElement(null), false)
  assert.equal(prefersReducedMotion({}), false)
  assert.equal(prefersReducedMotion(undefined), false)
})

test('Review plan and Route options reveal the plan, while automatic opening does not scroll', async () => {
  const monitor = await readFile(new URL('./WorkerMonitor.svelte', import.meta.url), 'utf8')
  assert.match(monitor, /<button on:click=\{revealPlan\}>Review plan<\/button>/)
  assert.match(monitor, /<button on:click=\{revealPlan\}>Route options<\/button>/)
  const source = monitor.match(/async function revealPlan\(\) \{[\s\S]*?\n  \}/)[0]
  const calls = []
  const context = { snapshot: { id: 'original' }, showGraph: false, planAnchor: {}, tick: async () => {}, revealElement: el => calls.push(el) }
  vm.createContext(context)
  vm.runInContext(source, context)
  await context.revealPlan()
  assert.equal(context.showGraph, true)
  assert.deepEqual(calls, [context.planAnchor])
  context.tick = async () => { context.snapshot = { id: 'another-run' } }
  await context.revealPlan()
  assert.equal(calls.length, 1, 'a delayed reveal cannot move focus into another run')
  const runPlan = monitor.match(/async function runPlan\(\) \{[\s\S]*?\n  \}/)[0]
  assert.doesNotMatch(runPlan, /revealElement|revealPlan/)
})
