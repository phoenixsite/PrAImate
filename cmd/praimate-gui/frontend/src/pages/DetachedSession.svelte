<script>
  import { onMount, onDestroy, tick } from 'svelte'
  import { Terminal } from '@xterm/xterm'
  import { FitAddon } from '@xterm/addon-fit'
  import '@xterm/xterm/css/xterm.css'
  import { api, onChatStream, onApproval } from '../lib/api.js'
  import { term, onTermData, onTermExit, decodeBase64Bytes } from '../lib/terminal.js'
  import { renderMarkdown } from '../lib/markdown.js'
  import ChatActivity from '../lib/ChatActivity.svelte'
  import ChatTools from '../lib/ChatTools.svelte'
  import ChatActivityToggle from '../lib/ChatActivityToggle.svelte'
  import { hideChatActivity } from '../lib/chatActivityPref.js'
  import { applyChatStreamEvent } from '../lib/chatActivity.js'
  import { normalizeToolsForCli } from '../lib/chatTools.js'

  export let mode

  let connected = true
  let error = ''
  let unsubs = []
  let disconnectTimer

  function markConnected() {
    connected = true
    clearTimeout(disconnectTimer)
    disconnectTimer = null
    if (mode.kind === 'terminal') replayTerminal()
    else if (mode.kind === 'chat') { loadChatConfig(); loadMessages() }
  }

  function markDisconnected(ev) {
    connected = false
    error = ev?.message || 'Main PrAImate window disconnected.'
    // Normal main-window closure is blocked. If it was killed or crashed,
    // avoid leaving orphan WebView processes retrying forever.
    if (!disconnectTimer) disconnectTimer = setTimeout(() => window.runtime?.Quit?.(), 30_000)
  }

  // Chat state. The main process executes and persists every operation; this
  // window owns only presentation state.
  let messages = []
  let draft = ''
  let sending = false
  let stream = null
  let approvals = []
  let attachments = []
  let threadEl
  let chat = null
  let toolsSaving = false

  async function loadChatConfig() {
    try { chat = await api.detachedChatConfig() } catch (e) { error = String(e) }
  }

  async function setTools(value) {
    if (!chat || toolsSaving || sending) return
    toolsSaving = true
    try {
      const tools = normalizeToolsForCli(chat.CLIAgent, value)
      await api.setChatTools(mode.sessionId, tools)
      chat = { ...chat, Settings: { ...chat.Settings, tools } }
      error = ''
    } catch (e) { error = String(e) }
    finally { toolsSaving = false }
  }

  function cleanMsg(value) {
    return String(value || '').replace(/\n*\[The user is looking at:[^\]]*\]\s*$/, '')
  }

  async function loadMessages() {
    try {
      messages = (await api.chatMessages(mode.sessionId)) || []
      if (!error) error = messages.at(-1)?.Meta?.error || ''
      await scrollToBottom()
    } catch (e) {
      error = String(e)
    }
  }

  function handleStream(ev) {
    if (ev?.chatId !== mode.sessionId) return
    sending = true
    stream = applyChatStreamEvent(stream, ev)
    scrollToBottom()
  }

  function handleApproval(req) {
    if (req?.chatId === mode.sessionId) approvals = [...approvals, req]
  }

  async function answerApproval(req, allow, always) {
    approvals = approvals.filter((item) => item.id !== req.id)
    try { await api.resolveApproval(req.id, allow, always) } catch (e) { error = String(e) }
  }

  async function pickAttachments() {
    try {
      const picked = (await api.pickChatAttachments(mode.sessionId)) || []
      attachments = [...attachments, ...picked]
    } catch (e) { error = String(e) }
  }

  async function send(override = null) {
    const compact = override === '/compact'
    const text = compact ? override : draft.trim()
    if (sending || (!text && attachments.length === 0)) return
    const staged = compact ? [] : attachments
    sending = true
    stream = null
    error = ''
    if (!compact) {
      draft = ''
      attachments = []
    }
    messages = [...messages, { Role: 'user', Content: text, _pending: true }]
    await scrollToBottom()
    try {
      if (text.startsWith('!')) await api.runChatCommand(mode.sessionId, text.slice(1))
      else await api.sendChatStream(mode.sessionId, text, staged.map((item) => item.path))
      await loadMessages()
    } catch (e) {
      error = String(e)
      if (!compact) attachments = staged
      await loadMessages()
    } finally {
      sending = false
      stream = null
      approvals = []
    }
  }

  function onComposerKey(e) {
    if (e.isComposing || e.keyCode === 229) return
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }

  async function stopChat() {
    sending = false
    stream = null
    try { await api.cancelChatTurn(mode.sessionId) } catch {}
  }

  async function scrollToBottom() {
    await tick()
    if (threadEl) threadEl.scrollTop = threadEl.scrollHeight
  }

  // Terminal state. Input and resize calls are coalesced to keep the
  // cross-process bridge cheap without changing terminal byte order.
  let terminalHost
  let xterm
  let fit
  let resizeObserver
  let cursor = 0
  let replaying = false
  let inputBuffer = ''
  let inputTimer
  let resizeTimer
  let exited = false

  async function replayTerminal() {
    if (!xterm || replaying) return
    replaying = true
    try {
      const snap = await term.snapshot(mode.sessionId)
      const end = Number(snap?.endOffset || 0)
      if (cursor === 0) {
        if (snap?.data) xterm.write(decodeBase64Bytes(snap.data))
      } else if (end > cursor && snap?.data) {
        const all = decodeBase64Bytes(snap.data)
        const start = Number(snap?.startOffset || 0)
        xterm.write(all.slice(Math.max(0, cursor - start)))
      }
      cursor = Math.max(cursor, end)
      error = ''
    } catch (e) {
      error = String(e)
    } finally {
      replaying = false
    }
  }

  function handleTerminalData(data, meta) {
    if (!xterm) return
    if (!meta) {
      xterm.write(data)
      return
    }
    const start = Number(meta.startOffset || 0)
    const end = Number(meta.endOffset || start + data.length)
    if (end <= cursor) return
    if (start > cursor) {
      replayTerminal()
      return
    }
    const skip = Math.max(0, cursor - start)
    xterm.write(skip ? data.slice(skip) : data)
    cursor = end
  }

  function flushInput() {
    inputTimer = null
    const value = inputBuffer
    inputBuffer = ''
    if (value) term.write(mode.sessionId, value).catch((e) => { error = String(e) })
  }

  function queueInput(value) {
    inputBuffer += value
    if (!inputTimer) inputTimer = setTimeout(flushInput, 4)
  }

  function syncTerminalSize() {
    clearTimeout(resizeTimer)
    resizeTimer = setTimeout(() => {
      try {
        fit.fit()
        term.resize(mode.sessionId, xterm.cols, xterm.rows)
      } catch {}
    }, 60)
  }

  async function mountTerminal() {
    await tick()
    xterm = new Terminal({
      fontFamily: 'JetBrains Mono, ui-monospace, monospace',
      fontSize: 13,
      cursorBlink: true,
      theme: { background: '#101218', foreground: '#e6e9f0' },
    })
    fit = new FitAddon()
    xterm.loadAddon(fit)
    xterm.open(terminalHost)
    xterm.onData(queueInput)
    unsubs.push(onTermData(mode.sessionId, handleTerminalData))
    unsubs.push(onTermExit(mode.sessionId, () => {
      exited = true
      xterm?.write('\r\n\x1b[2m[process exited]\x1b[0m\r\n')
    }))
    resizeObserver = new ResizeObserver(syncTerminalSize)
    resizeObserver.observe(terminalHost)
    syncTerminalSize()
    await replayTerminal()
    xterm.focus()
  }

  async function stopTerminal() {
    try { await term.close(mode.sessionId) } catch (e) { error = String(e) }
    finally { window.runtime?.Quit?.() }
  }

  onMount(async () => {
    if (window.runtime?.EventsOn) {
      window.runtime.EventsOn('praimate:detached-connected', markConnected)
      window.runtime.EventsOn('praimate:detached-disconnected', markDisconnected)
      window.runtime.EventsOn('praimate:detached-resync', () => { if (mode.kind === 'terminal') replayTerminal(); else { loadChatConfig(); loadMessages() } })
      window.runtime.EventsOn('praimate:chat-finished', async (event) => {
        if (event?.chatId !== mode.sessionId) return
        sending = false
        stream = null
        approvals = []
        error = event.error || ''
        await loadMessages()
      })
      unsubs.push(() => window.runtime.EventsOff('praimate:detached-connected'))
      unsubs.push(() => window.runtime.EventsOff('praimate:detached-disconnected'))
      unsubs.push(() => window.runtime.EventsOff('praimate:detached-resync'))
      unsubs.push(() => window.runtime.EventsOff('praimate:chat-finished'))
    }
    if (mode.kind === 'chat') {
      unsubs.push(onChatStream(handleStream), onApproval(handleApproval))
      try { sending = await api.detachedSessionActive() } catch { sending = false }
      await loadChatConfig()
      await loadMessages()
    } else {
      await mountTerminal()
    }
    try { await api.detachedRendererReady() } catch (e) { error = String(e) }
  })

  onDestroy(() => {
    clearTimeout(inputTimer)
    clearTimeout(resizeTimer)
    clearTimeout(disconnectTimer)
    flushInput()
    for (const unsubscribe of unsubs) unsubscribe()
    resizeObserver?.disconnect()
    xterm?.dispose()
  })
</script>

<div class="detached-shell">
  <header class="detached-head">
    <div class="grow">
      <strong>{mode.title}</strong>
      <span class="pill">{mode.kind}</span>
      <span class="connection" class:offline={!connected}>{connected ? 'Connected to PrAImate' : 'Disconnected'}</span>
    </div>
    {#if mode.kind === 'chat' && chat}<ChatTools cli={chat.CLIAgent} value={normalizeToolsForCli(chat.CLIAgent, chat.Settings?.tools)} disabled={!connected || sending || toolsSaving} on:change={event => setTools(event.detail)} />{/if}
    {#if mode.kind === 'chat'}<ChatActivityToggle />{/if}
    {#if mode.kind === 'chat' && sending}<button class="btn danger" on:click={stopChat}>■ Stop</button>{/if}
    {#if mode.kind === 'terminal' && !exited}<button class="btn danger" on:click={stopTerminal}>■ Stop</button>{/if}
  </header>

  {#if error}<div class="banner">{error}</div>{/if}

  {#if mode.kind === 'terminal'}
    <div class="terminal-host" bind:this={terminalHost}></div>
  {:else}
    <div class="thread" bind:this={threadEl}>
      {#if messages.length === 0}<div class="empty">No messages yet.</div>{/if}
      {#each messages as message}
        <div class="msg {message.Role === 'user' ? 'user' : message.Role === 'command' ? 'command' : 'assistant'}" class:pending={message._pending}>
          <div class="who">{message.Role}{message.Meta?.interrupted ? ' · interrupted' : ''}</div>
          {#if message.Role === 'assistant'}
            <ChatActivity events={message.Meta?.activity || []} hidden={$hideChatActivity} />
            <div class="markdown">{@html renderMarkdown(cleanMsg(message.Content))}</div>
          {:else if message.Role === 'command'}
            <pre class="command-output">{message.Content}</pre>
          {:else}
            {cleanMsg(message.Content)}
          {/if}
        </div>
      {/each}
      {#if sending}
        <div class="msg assistant">
          <div class="who">assistant</div>
          <ChatActivity events={stream?.activity || []} collapsible={false} hidden={$hideChatActivity} />
          {#if stream?.text}<div class="markdown">{@html renderMarkdown(stream.text)}</div>{:else}<span class="typing">…thinking</span>{/if}
        </div>
      {/if}
      {#each approvals as approval (approval.id)}
        <div class="approval-card">
          <strong>The agent asks to use {approval.tool}</strong>
          {#if approval.detail}<div class="mono">{approval.detail}</div>{/if}
          <div class="row">
            <button class="btn primary" on:click={() => answerApproval(approval, true, false)}>Allow once</button>
            <button class="btn" on:click={() => answerApproval(approval, true, true)}>Always allow</button>
            <button class="btn danger" on:click={() => answerApproval(approval, false, false)}>Deny</button>
          </div>
        </div>
      {/each}
    </div>
    {#if attachments.length}
      <div class="attachments">{#each attachments as item}<span class="pill">{item.name}<button on:click={() => (attachments = attachments.filter((x) => x.path !== item.path))}>×</button></span>{/each}</div>
    {/if}
    <div class="composer">
      <button class="btn" on:click={pickAttachments} disabled={sending} title="Add files" aria-label="Add files">📎</button>
      <textarea class="field" rows="2" bind:value={draft} on:keydown={onComposerKey} disabled={sending} placeholder="Message the agent…"></textarea>
      <button class="btn" on:click={() => send('/compact')} disabled={sending || approvals.length > 0} title="Compact saved conversation context">◫ Compact</button>
      <button class="btn primary" on:click={send} disabled={sending || (!draft.trim() && attachments.length === 0)}>Send</button>
    </div>
  {/if}
</div>

<style>
  .detached-shell { height: 100vh; display: flex; flex-direction: column; padding: 14px; gap: 10px; overflow: hidden; }
  .detached-head { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; min-height: 34px; }
  .connection { margin-left: 8px; color: var(--ok); font-size: 12px; }
  .connection.offline { color: var(--err); }
  .terminal-host { flex: 1; min-height: 0; border: 1px solid var(--border); border-radius: var(--radius); background: #101218; padding: 8px; overflow: hidden; }
  .thread { flex: 1; min-height: 0; overflow-y: auto; display: flex; flex-direction: column; gap: 8px; }
  .msg { border: 1px solid var(--border); border-radius: var(--radius); padding: 10px 12px; background: var(--bg-panel); overflow-wrap: anywhere; }
  .msg.user { background: var(--bg-raised); }
  .who { color: var(--text-dim); text-transform: uppercase; font-size: 10px; font-weight: 700; margin-bottom: 5px; }
  .composer { display: flex; align-items: flex-end; gap: 8px; }
  .composer textarea { flex: 1; resize: vertical; }
  .approval-card { border: 1px solid var(--warn); border-radius: var(--radius); padding: 10px; background: var(--bg-panel); }
  .approval-card .row { margin-top: 8px; }
  .command-output { white-space: pre-wrap; margin: 0; }
  .attachments { display: flex; gap: 6px; flex-wrap: wrap; }
  .attachments button { border: 0; color: inherit; background: transparent; cursor: pointer; }
  .tool-feed { color: var(--text-dim); margin-bottom: 6px; }
  .typing { color: var(--text-dim); font-style: italic; }
</style>
