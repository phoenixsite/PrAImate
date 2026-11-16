// Smoothly brings an element into view and moves keyboard focus to it, so a
// button that opens something far below the fold visibly takes the user there.
// Users who ask the OS for reduced motion get an instant jump instead.

export function prefersReducedMotion(win = globalThis.window) {
  return Boolean(win?.matchMedia?.('(prefers-reduced-motion: reduce)')?.matches)
}

export function revealElement(element, { win = globalThis.window, focus = true } = {}) {
  if (!element) return false
  if (focus) {
    if (!element.hasAttribute('tabindex')) element.setAttribute('tabindex', '-1')
    // preventScroll: the animated scroll below is the only movement.
    element.focus({ preventScroll: true })
  }
  element.scrollIntoView({ behavior: prefersReducedMotion(win) ? 'auto' : 'smooth', block: 'start' })
  return true
}
