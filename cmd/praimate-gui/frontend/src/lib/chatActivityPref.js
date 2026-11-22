import { writable } from 'svelte/store'

// Whether intermediate agent activity (tools, thoughts, status) is folded away
// in chat views. Persisted in localStorage; default keeps activity visible.
export const HIDE_ACTIVITY_KEY = 'praimate:chat-hide-activity'

export function storedHideActivity() {
  try { return localStorage.getItem(HIDE_ACTIVITY_KEY) === '1' } catch { return false }
}

export const hideChatActivity = writable(storedHideActivity())

export function setHideChatActivity(value) {
  const next = !!value
  try { localStorage.setItem(HIDE_ACTIVITY_KEY, next ? '1' : '0') } catch {}
  hideChatActivity.set(next)
}
