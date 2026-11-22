// Pure helpers for the in-chat find bar (Ctrl/Cmd+F). Matching is
// case-insensitive and non-overlapping; highlighting uses Range objects and the
// CSS Custom Highlight API so the rendered message DOM is never mutated.

export function findMatches(text, query) {
  const q = String(query ?? '')
  if (!q || !text) return []
  const hay = String(text).toLowerCase()
  const needle = q.toLowerCase()
  // toLowerCase can change string length for some characters; skip safely.
  if (hay.length !== String(text).length) return []
  const out = []
  let i = 0
  while ((i = hay.indexOf(needle, i)) !== -1) {
    out.push([i, i + needle.length])
    i += needle.length
  }
  return out
}

// Matches across several text chunks: [{ node: index, start, end }].
export function findAllMatches(texts, query) {
  const out = []
  texts.forEach((t, node) => {
    for (const [start, end] of findMatches(t, query)) out.push({ node, start, end })
  })
  return out
}

export function stepIndex(current, total, delta) {
  if (total <= 0) return -1
  if (current < 0) return delta < 0 ? total - 1 : 0
  return (((current + delta) % total) + total) % total
}

export function counterLabel(index, total) {
  return total > 0 ? `${index + 1}/${total}` : '0 results'
}

// Ctrl+F (Cmd+F on macOS) opens the bar. Other text fields keep native behavior
// unless they live inside `scope` (the chat view, which includes its composer).
export function isFindShortcut(e) {
  return (e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && String(e.key).toLowerCase() === 'f'
}

export function shouldOpenFind(e, scope) {
  if (!isFindShortcut(e)) return false
  const t = e.target
  const editable = t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT' || t.isContentEditable)
  if (!editable) return true
  return Boolean(scope && scope.contains && scope.contains(t))
}

// DOM helpers (browser only).
export function collectTextNodes(root) {
  const nodes = []
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode: (n) => (n.nodeValue && n.nodeValue.trim() ? (n.parentElement?.closest('script,style') ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT) : NodeFilter.FILTER_REJECT)
  })
  while (walker.nextNode()) nodes.push(walker.currentNode)
  return nodes
}

export function buildRanges(root, query) {
  const nodes = collectTextNodes(root)
  return findAllMatches(nodes.map((n) => n.nodeValue), query).map((m) => {
    const r = document.createRange()
    r.setStart(nodes[m.node], m.start)
    r.setEnd(nodes[m.node], m.end)
    return r
  })
}

export function paintHighlights(ranges, current) {
  if (typeof CSS === 'undefined' || !CSS.highlights || typeof Highlight === 'undefined') return
  CSS.highlights.set('chat-find', new Highlight(...ranges))
  if (current >= 0 && ranges[current]) CSS.highlights.set('chat-find-current', new Highlight(ranges[current]))
  else CSS.highlights.delete('chat-find-current')
}

export function clearHighlights() {
  if (typeof CSS === 'undefined' || !CSS.highlights) return
  CSS.highlights.delete('chat-find')
  CSS.highlights.delete('chat-find-current')
}
