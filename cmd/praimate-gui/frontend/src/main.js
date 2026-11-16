import App from './App.svelte'
import './styles.css'
import { api } from './lib/api.js'
import { installExternalLinks, systemLinkOpener } from './lib/externalLinks.js'
import { showToast } from './lib/stores.js'

// Chat links open in the system browser; the WebView cannot open new windows.
installExternalLinks(
  document,
  (url) => api.openExternalURL(url),
  () => !!systemLinkOpener(window),
  (error, href) => {
    const link = href.length > 80 ? `${href.slice(0, 77)}…` : href
    showToast({ title: 'Could not open link', message: `${link} — ${String(error?.message || error)}`, tone: 'err', duration: 6000 })
  },
)

const app = new App({
  target: document.getElementById('app'),
})

export default app
