import test from 'node:test'
import assert from 'node:assert/strict'
import { installExternalLinks, linkToOpen } from './externalLinks.js'

const anchor = (href) => ({ getAttribute: () => href })
const target = (href) => ({ closest: (selector) => (selector === 'a[href]' && href !== null ? anchor(href) : null) })

function root() {
  const handlers = []
  return {
    addEventListener: (_type, fn) => handlers.push(fn),
    removeEventListener: (_type, fn) => handlers.splice(handlers.indexOf(fn), 1),
    click(href, extra = {}) {
      const event = { button: 0, defaultPrevented: false, target: target(href), preventDefault() { this.defaultPrevented = true }, ...extra }
      handlers.forEach((fn) => fn(event))
      return event
    },
    handlers,
  }
}

test('only web and mail links are opened', () => {
  assert.equal(linkToOpen(target('https://example.com/a')), 'https://example.com/a')
  assert.equal(linkToOpen(target(' HTTP://example.com ')), 'HTTP://example.com')
  assert.equal(linkToOpen(target('mailto:a@b.co')), 'mailto:a@b.co')
  for (const href of ['#section', '/local', 'javascript:alert(1)', 'file:///etc/passwd', '']) {
    assert.equal(linkToOpen(target(href)), '', href)
  }
  assert.equal(linkToOpen(target(null)), '')
})

test('a click opens the link through the backend and cancels navigation', async () => {
  const opened = []
  const r = root()
  installExternalLinks(r, (url) => { opened.push(url) })
  const event = r.click('https://example.com')
  assert.equal(event.defaultPrevented, true)
  await new Promise((resolve) => setImmediate(resolve))
  assert.deepEqual(opened, ['https://example.com'])
  // Ctrl/Meta/Shift clicks open it the same way.
  r.click('https://example.com/ctrl', { ctrlKey: true })
  await new Promise((resolve) => setImmediate(resolve))
  assert.equal(opened.at(-1), 'https://example.com/ctrl')
})

test('other clicks and unavailable backends keep the default behaviour', () => {
  const opened = []
  const r = root()
  let available = false
  installExternalLinks(r, (url) => { opened.push(url) }, () => available)
  assert.equal(r.click('https://example.com').defaultPrevented, false)
  available = true
  assert.equal(r.click('#anchor').defaultPrevented, false)
  assert.equal(r.click('https://example.com', { button: 1 }).defaultPrevented, false)
  assert.equal(r.click('https://example.com', { defaultPrevented: true }).defaultPrevented, true)
  assert.deepEqual(opened, [])
})

test('a rejected link reports the error and the page still does not navigate', async () => {
  const r = root()
  const errors = []
  installExternalLinks(r, async () => { throw new Error('invalid link') }, () => true, (error, href) => errors.push([error.message, href]))
  const event = r.click('https://')
  assert.equal(event.defaultPrevented, true)
  await new Promise((resolve) => setImmediate(resolve))
  assert.deepEqual(errors, [['invalid link', 'https://']])
})

test('a synchronous failure is also reported and the listener can be removed', async () => {
  const r = root()
  const errors = []
  const remove = installExternalLinks(r, () => { throw new Error('boom') }, () => true, (error) => errors.push(error.message))
  r.click('https://example.com')
  await new Promise((resolve) => setImmediate(resolve))
  assert.deepEqual(errors, ['boom'])
  remove()
  assert.equal(r.handlers.length, 0)
})
