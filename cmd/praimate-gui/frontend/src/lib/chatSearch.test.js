import test from 'node:test'
import assert from 'node:assert/strict'
import { findMatches, findAllMatches, stepIndex, counterLabel, isFindShortcut, shouldOpenFind } from './chatSearch.js'

test('matching is case-insensitive and non-overlapping', () => {
  assert.deepEqual(findMatches('Hello hello HELLO', 'hello'), [[0, 5], [6, 11], [12, 17]])
  assert.deepEqual(findMatches('aaaa', 'aa'), [[0, 2], [2, 4]])
  assert.deepEqual(findMatches('abc', ''), [])
  assert.deepEqual(findMatches('abc', 'z'), [])
})

test('findAllMatches counts across chunks', () => {
  const m = findAllMatches(['foo bar', 'Foo', 'none'], 'foo')
  assert.equal(m.length, 2)
  assert.equal(m[1].node, 1)
})

test('stepIndex wraps and counter formats', () => {
  assert.equal(stepIndex(-1, 3, 1), 0)
  assert.equal(stepIndex(-1, 3, -1), 2)
  assert.equal(stepIndex(2, 3, 1), 0)
  assert.equal(stepIndex(0, 3, -1), 2)
  assert.equal(stepIndex(0, 0, 1), -1)
  assert.equal(counterLabel(1, 4), '2/4')
  assert.equal(counterLabel(-1, 0), '0 results')
})

test('shortcut opens bar except in foreign text fields', () => {
  const ctrl = { ctrlKey: true, key: 'f' }
  assert.ok(isFindShortcut(ctrl))
  assert.ok(isFindShortcut({ metaKey: true, key: 'F' }))
  assert.ok(!isFindShortcut({ ctrlKey: true, shiftKey: true, key: 'f' }))
  assert.ok(shouldOpenFind({ ...ctrl, target: { tagName: 'DIV' } }, null))
  const foreign = { tagName: 'INPUT' }
  assert.ok(!shouldOpenFind({ ...ctrl, target: foreign }, { contains: () => false }))
  assert.ok(shouldOpenFind({ ...ctrl, target: foreign }, { contains: () => true }))
})
