// Links in chat messages open in the system browser through the Go backend.
// The Wails WebView cannot open target="_blank" links itself, and the page
// must never navigate away from the app. Only web and mail links are handled;
// in-page (#) and relative links keep their normal behaviour.

const OPENABLE = /^(https?:|mailto:)/i

// The managed Server desktop retains its real local bindings. Remote browser
// and Android bridges synthesize every method name, so their presence alone
// cannot establish that this device has a native browser opener.
export function systemLinkOpener(host = globalThis.window) {
  const native = host?.praimateManagedHost?.native || (!host?.praimateRemote && host?.go?.main?.App)
  return typeof native?.OpenExternalURL === 'function' ? native.OpenExternalURL.bind(native) : null
}

export function linkToOpen(target) {
  const anchor = target?.closest?.('a[href]')
  if (!anchor) return ''
  const href = String(anchor.getAttribute('href') || '').trim()
  return OPENABLE.test(href) ? href : ''
}

// `open` receives the URL; `available` says whether a backend can open it. When
// it cannot (a plain browser, or the remote Server UI) the browser's own link
// handling is left alone. `onError(error, href)` is called when the backend
// rejects or fails to open the link.
export function installExternalLinks(root, open, available = () => true, onError = () => {}) {
  const onClick = (event) => {
    if (event.defaultPrevented || event.button !== 0 || !available()) return
    const href = linkToOpen(event.target)
    if (!href) return
    event.preventDefault()
    Promise.resolve().then(() => open(href)).catch((error) => onError(error, href))
  }
  root.addEventListener('click', onClick)
  return () => root.removeEventListener('click', onClick)
}
