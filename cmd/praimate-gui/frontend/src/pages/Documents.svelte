<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import SkillBindingsEditor from '../lib/SkillBindingsEditor.svelte'
  import SkillChoiceDraft from '../lib/SkillChoiceDraft.svelte'
  import { activePage, openChatId, pageRevision, showToast, showConfirm } from '../lib/stores.js'
  import { localRoutingUnavailableMessage, supportsLocalRouting } from '../lib/localRouting.js'

  let chats = []
  let agents = []
  let clis = []
  let loading = true
  let error = ''
  let form = null
  let localOpt = null
  let preflightWarnings = null

  let cfg = null
  let cfgSaving = false
  let mcpServers = []

  function normalizeToolsForCli(cli, tools) {
    if (cli === 'claude' || cli === 'openclaude') return tools || ''
    if (cli === 'codex' || cli === 'gemini') return ['edits', 'full'].includes(tools) ? tools : ''
    if (cli === 'opencode' || cli === 'praimate-code') return ['plan', 'full'].includes(tools) ? tools : ''
    return ''
  }

  function toolLevelsForCli(c) {
    if (c === 'claude' || c === 'openclaude') return [{id:'',label:'Safe',hint:''}, {id:'ask',label:'Ask',hint:''}, {id:'edits',label:'Edits',hint:''}, {id:'full',label:'Full',hint:''}]
    if (c === 'opencode' || c === 'praimate-code') return [{id:'plan',label:'Plan',hint:''}, {id:'',label:'Build',hint:''}, {id:'full',label:'Full',hint:''}]
    return [{id:'',label:'Safe',hint:''}, {id:'ask',label:'Ask',hint:''}, {id:'edits',label:'Edits',hint:''}, {id:'full',label:'Full',hint:''}]
  }

  function openConfig(chat) {
    error = ''
    cfg = {
      chat, name: chat.Title || '', workspacePath: chat.WorkspacePath || '', cli: chat.CLIAgent, model: chat.Settings?.model || '',
      tools: normalizeToolsForCli(chat.CLIAgent, chat.Settings?.tools),
      localEndpoint: chat.Settings?.local?.endpoint || '',
      localApiKey: chat.Settings?.local?.api_key || '',
      localModel: chat.Settings?.local?.model || '',
      suggestions: [], modelLoading: true,
      mcps: (chat.Settings?.mcp_servers || []).slice(),
    }
    if (clis.length === 0) api.listCLIs().then((r) => { clis = r || [] }).catch(() => {})
    if (localOpt === null) api.localLLMModels().then((r) => { localOpt = r }).catch(() => { localOpt = { configured: false } })
    loadMCPServers()
    cfgCliChanged()
  }

  async function cfgCliChanged() {
    if (!cfg) return
    if (cfg.localEndpoint && !supportsLocalRouting(cfg.cli)) { cfg.localEndpoint = ''; cfg.localModel = '' }
    cfg.modelLoading = true
    cfg.suggestions = (await api.listCLIModels(cfg.cli).catch(() => [])) || []
    cfg.modelLoading = false
  }

  async function saveConfig() {
    if (!cfg) return
    cfgSaving = true; error = ''
    try {
      if (!cfg.workspacePath?.trim()) throw new Error('Choose an existing document folder.')
      if (cfg.workspacePath.trim() !== (cfg.chat.WorkspacePath || '')) {
        await api.updateChatWorkspace(cfg.chat.ID, cfg.workspacePath.trim())
      }
      await api.updateChatConfig(cfg.chat.ID, cfg.cli, cfg.model.trim(), normalizeToolsForCli(cfg.cli, cfg.tools), cfg.localEndpoint.trim(), cfg.localApiKey, cfg.localModel.trim())
      if (cfg.name.trim() && cfg.name.trim() !== cfg.chat.Title) {
        await api.renameChat(cfg.chat.ID, cfg.name.trim())
      }
      await api.setChatMCPServers(cfg.chat.ID, cfg.mcps || [])
      cfg = null
      await load()
    } catch (e) { error = String(e) } finally { cfgSaving = false }
  }

  function continueLaunch() {
    if (!form) return
    form.busy = false
    preflightWarnings = null
    launch()
  }

  $: sessions = chats.filter((chat) => chat.Settings?.surface === 'studio')
  $: editorAgents = agents.filter((agent) => !agent.surfaces?.length || agent.surfaces.includes('editor'))
  $: selectedAgent = form?.agentID ? agents.find((agent) => agent.id === form.agentID) : null
  $: compatibleCLIs = !form
    ? []
    : selectedAgent?.supports?.length
      ? clis.filter((cli) => selectedAgent.supports.includes(cli.id))
      : clis
  $: localRoutable = !!form && supportsLocalRouting(form.cli)

  function agentName(chat) {
    if (!chat.AgentID) return 'No agent persona'
    return agents.find((agent) => agent.id === chat.AgentID)?.name || chat.AgentID
  }

  function fmtDate(value) {
    try { return new Date(value).toLocaleString() } catch { return value }
  }

  async function load() {
    loading = true
    try {
      const [loadedChats, loadedAgents, loadedClis] = await Promise.all([
        api.listChats(),
        api.listAgents().catch(() => []),
        api.listCLIs().catch(() => [])
      ])
      chats = loadedChats || []
      agents = loadedAgents || []
      clis = loadedClis || []
      error = ''
    } catch (e) {
      error = String(e)
    } finally {
      loading = false
    }
  }

  async function loadMCPServers() {
    try { mcpServers = ((await api.mcpServers()) || []).filter((s) => s.enabled) }
    catch { mcpServers = [] }
  }

  async function openNew() {
    error = ''
    try {
      if (!clis.length) clis = (await api.listCLIs()) || []
      if (localOpt === null) localOpt = await api.localLLMModels().catch(() => ({ configured: false }))
      const first = clis.find((cli) => cli.available) || clis[0]
      form = {
        agentID: '', cli: first?.id || '', model: '', folder: '', useLocal: false,
        name: '', mcps: [],
        localModel: '', suggestions: [], busy: false, preflight: null, preflightChecked: false, skillChoices: null,
      }
      await loadMCPServers()
      await cliChanged()
    } catch (e) {
      error = String(e)
    }
  }

  function invalidatePreflight() {
    if (!form) return
    error = ''
    form = { ...form, preflight: null, preflightChecked: false }
  }

  async function agentChanged() {
    if (!form) return
    const nextAgent = agents.find((agent) => agent.id === form.agentID)
    const enabledMCPs = new Set(mcpServers.map(server => server.id))
    form.mcps = (nextAgent?.mcp_servers || []).filter(id => enabledMCPs.has(id))
    const allowed = nextAgent?.supports?.length
      ? clis.filter((cli) => nextAgent.supports.includes(cli.id))
      : clis
    if (!allowed.some((cli) => cli.id === form.cli && cli.available)) {
      form.cli = allowed.find((cli) => cli.available)?.id || allowed[0]?.id || ''
    }
    invalidatePreflight()
    await cliChanged()
  }

  async function cliChanged() {
    if (!form) return
    if (form.useLocal && !supportsLocalRouting(form.cli)) form.useLocal = false
    form = { ...form, suggestions: [], modelLoading: true, preflight: null, preflightChecked: false }
    const cli = form.cli
    const suggestions = cli ? await api.listCLIModels(cli).catch(() => []) : []
    if (form?.cli === cli) form = { ...form, suggestions: suggestions || [], modelLoading: false }
  }

  async function pickFolder() {
    try {
      const folder = await api.pickFolder()
      if (folder && form) {
        form.folder = folder
        error = ''
        invalidatePreflight()
      }
    } catch (e) {
      error = String(e)
    }
  }

  async function launch() {
    if (!form || form.busy) return
    if (!form.folder) {
      error = 'Pick a project folder first.'
      return
    }
    if (!form.cli) {
      error = 'Install and select a CLI first.'
      return
    }
    const local = form.useLocal && localOpt?.configured
    const endpoint = local ? localOpt.endpoint : ''
    const localModel = local ? form.localModel.trim() : ''
    const model = local ? '' : form.model.trim()
    form.busy = true
    error = ''
    try {
      if (!form.preflightChecked) {
        const check = await api.preflightExecution(form.agentID, 'studio', form.cli, model, 'edits', form.folder, endpoint, localModel)
        form = { ...form, busy: false, preflight: check, preflightChecked: true }
        if (!check?.ok) {
          error = (check?.issues || []).filter((issue) => issue.severity === 'error').map((issue) => issue.message).join('\n') || 'Preflight failed.'
          return
        }
        const warnings = (check?.issues || []).filter((i) => i.severity !== 'error')
        if (warnings.length > 0) {
          preflightWarnings = warnings
          return
        }
        form.busy = true
      }
      const cli = form.cli
      const folder = form.folder
      showToast({ title: 'Opening Documents', message: `Starting ${agentName({ AgentID: form.agentID })} with ${cli} in ${folder}`, tone: 'busy', duration: 0, dismissible: false })
      const createdChatId = await api.prepareStudioChat(folder, form.agentID, cli, model, endpoint, localModel, form.skillChoices)
      if (createdChatId) await api.setChatMCPServers(createdChatId, form.mcps || [])
      await api.openEditorWindow(folder, form.agentID, cli, model, createdChatId, endpoint, '', localModel)
      if (form.name && createdChatId) await api.renameChat(createdChatId, form.name)
      form = null
      await load()
      showToast({ title: 'Document Session Opened', message: 'The session is ready in a separate editor window.', tone: 'ok' })
    } catch (e) {
      error = String(e)
      showToast({ title: 'Failed to open', message: String(e), tone: 'err', page: 'documents', duration: 0 })
      if (form) form.busy = false
    }
  }

  async function reopen(chat) {
    error = ''
    try {
      openChatId.set(chat.ID)
      showToast({ title: 'Opening Document Session', message: chat.Title, tone: 'busy', duration: 0, dismissible: false })
      await api.openEditorWindow(chat.WorkspacePath, chat.AgentID || '', chat.CLIAgent || '', chat.Settings?.model || '', chat.ID, '', '', '')
      showToast({ title: 'Session Reopened', message: chat.Title, tone: 'ok' })
    } catch (e) {
      error = String(e)
      showToast({ title: 'Failed to open', message: String(e), tone: 'err', page: 'documents', duration: 0 })
    }
  }

  async function remove(chat) {
    const ok = await showConfirm({
      title: 'Delete Document Session',
      message: `Delete session "${chat.Title}"? This will permanently remove its transcript and session state.`
    })
    if (!ok) return
    try {
      await api.deleteChat(chat.ID)
      await load()
    } catch (e) {
      error = String(e)
    }
  }

  onMount(load)
</script>

<div class="row" style="margin-bottom:4px; align-items:center">
  <h1 class="grow" style="margin:0">Document Sessions</h1>
  <button class="btn" on:click={load} disabled={loading}>{loading ? 'Refreshing…' : 'Refresh'}</button>
  <button class="btn primary" on:click={openNew}>+ New Document Session</button>
</div>
<p class="subtitle">Live markdown co-editing and document-focused agent sessions. Reopen an existing workspace or start a new co-editing session.</p>

{#if error}<div class="banner">{error}</div>{/if}
{#if loading && !chats.length}<div class="empty">Loading document sessions…</div>{/if}
{#if !loading && sessions.length === 0}<div class="empty">No document sessions yet. Press “+ New Document Session” to open one.</div>{/if}

{#each sessions as chat}
  <div class="card row" style="margin-bottom: 8px;">
    <div class="grow">
      <div class="row" style="gap:8px; align-items:center">
        <strong>{chat.Title || 'Untitled session'}</strong>
        <span class="badge">{chat.CLIAgent}</span>
        {#if chat.AgentID}<span class="badge">{agentName(chat)}</span>{/if}
      </div>
      <div class="card-sub" style="margin-top:4px">
        {chat.WorkspacePath || 'No workspace'} · {fmtDate(chat.UpdatedAt || chat.CreatedAt)}
      </div>
    </div>
    <div class="row" style="gap:6px">
      <button class="btn primary" on:click={() => reopen(chat)}>Open Editor</button>
      <button class="btn" on:click={() => openConfig(chat)}>Edit</button>
      <button class="btn danger" on:click={() => remove(chat)}>Delete</button>
    </div>
  </div>
{/each}

<!-- NEW DOCUMENT SESSION MODAL -->
{#if form}
  <!-- svelte-ignore a11y-click-events-have-key-events -->
  <!-- svelte-ignore a11y-no-static-element-interactions -->
  <div class="modal-backdrop" on:click|self={() => !form.busy && (form = null)}>
    <div class="modal-content studio-modal" role="dialog" aria-modal="true" aria-labelledby="new-doc-title">
      <h2 id="new-doc-title">New Document Session</h2>
      <p class="subtitle">Choose the workspace, optional persona, and CLI used by the document assistant.</p>

      <label class="lbl" for="doc-name">Session Name</label>
      <input id="doc-name" class="field" bind:value={form.name} placeholder="e.g. Documentation Project" />

      <label class="lbl" for="doc-agent">Agent persona</label>
      <select id="doc-agent" class="field" bind:value={form.agentID} on:change={agentChanged}>
        <option value="">No agent persona</option>
        {#each editorAgents as agent}<option value={agent.id}>{agent.name}</option>{/each}
      </select>

      <label class="lbl" for="doc-cli">CLI</label>
      <select id="doc-cli" class="field" bind:value={form.cli} on:change={cliChanged}>
        {#each compatibleCLIs as cli}
          <option value={cli.id} disabled={!cli.available}>{cli.label}{cli.available ? '' : ' — not installed'}</option>
        {/each}
      </select>

      {#if localOpt?.configured && localRoutable}
        <label class="row local-toggle">
          <input type="checkbox" bind:checked={form.useLocal} on:change={invalidatePreflight} />
          <span>Use a configured local LLM</span>
        </label>
      {:else if localOpt?.configured}
        <div class="card-sub" style="margin-top:10px">{localRoutingUnavailableMessage(form.cli)}</div>
      {/if}

      {#if form.useLocal && localOpt?.configured && localRoutable}
        <label class="lbl" for="doc-local-model">Local model</label>
        {#if localOpt.allModels?.length}
          <select class="field mono" style="max-width:420px; margin-bottom:6px" bind:value={form.localModel} on:change={invalidatePreflight}>
            <option value="">Select a detected model…</option>
            {#each localOpt.hosts || [] as host}
              <optgroup label={`${host.name} (${host.endpoint})`}>
                {#each host.models as m}
                  <option value={m}>{m}</option>
                {/each}
              </optgroup>
            {/each}
          </select>
        {/if}
        <input id="doc-local-model" class="field mono" list="doc-local-models" bind:value={form.localModel} on:input={invalidatePreflight} placeholder="or type model name" />
        <datalist id="doc-local-models">{#each localOpt.models || [] as model}<option value={model}></option>{/each}</datalist>
      {:else}
        <label class="lbl" for="doc-model">Model <span class="card-sub">(blank = CLI default)</span></label>
        <input id="doc-model" class="field mono" list="doc-models" bind:value={form.model} on:input={invalidatePreflight} />
        <datalist id="doc-models">{#each form.suggestions || [] as model}<option value={model}></option>{/each}</datalist>
        {#if form.modelLoading}<div class="card-sub">Loading models…</div>{/if}
      {/if}

      <label class="lbl" for="doc-folder">Project folder *</label>
      <div class="row">
        <input id="doc-folder" class="field grow mono" bind:value={form.folder} on:input={invalidatePreflight} placeholder="folder the session may read and edit" />
        <button class="btn" on:click={pickFolder}>Browse…</button>
      </div>

      <SkillChoiceDraft bind:choices={form.skillChoices} />

      <div class="lbl" style="margin-top:10px">MCP servers <span class="card-sub" style="font-weight:400">— optional, exposed only to this session.</span></div>
      {#if mcpServers.length === 0}
        <div class="card-sub">No enabled MCP servers. Connect and enable one on the MCP page first.</div>
      {:else}
        <div class="mcp-grid">
          {#each mcpServers as s}
            <label class="mcp-card">
              <input type="checkbox" value={s.id} bind:group={form.mcps} />
              <div>
                <strong>{s.name}</strong>
                <div class="card-sub">{s.command || s.transport}</div>
              </div>
            </label>
          {/each}
        </div>
      {/if}

      {#if error}<div class="banner error-banner" role="alert" style="margin-top:12px">{error}</div>{/if}

      <div class="row actions" style="margin-top:20px">
        <button class="btn" on:click={() => { form = null; error = '' }} disabled={form.busy}>Cancel</button>
        <button class="btn primary" on:click={launch} disabled={form.busy}>{form.busy ? 'Opening…' : 'Open Document Session'}</button>
      </div>
    </div>
  </div>
{/if}

<!-- PREFLIGHT WARNINGS MODAL -->
{#if preflightWarnings}
  <!-- svelte-ignore a11y-click-events-have-key-events -->
  <!-- svelte-ignore a11y-no-static-element-interactions -->
  <div class="modal-backdrop" style="z-index: 12000" on:click|self={() => (preflightWarnings = null)}>
    <div class="modal-content warning-modal" role="dialog" aria-modal="true" style="border: 1px solid #d39e00; max-width: 500px">
      <h2 style="color: #d39e00; display:flex; align-items:center; gap:8px">
        <span style="font-size:20px">⚠️</span> Preflight Warnings
      </h2>
      <div style="margin: 16px 0; max-height: 50vh; overflow-y: auto">
        {#each preflightWarnings as issue}
          <div style="background: rgba(211, 158, 0, 0.1); color: var(--text); padding: 10px 14px; border-radius: 6px; margin-bottom: 8px; font-size: 13px;">
            {issue.message}
          </div>
        {/each}
      </div>
      <div class="row actions" style="justify-content: flex-end; margin-top:20px">
        <button class="btn" on:click={() => (preflightWarnings = null)}>Cancel</button>
        <button class="btn" style="background: #d39e00; color: #fff; border-color: #d39e00" on:click={continueLaunch}>Continue</button>
      </div>
    </div>
  </div>
{/if}

<!-- EDIT CONFIG MODAL -->
{#if cfg}
  <!-- svelte-ignore a11y-no-static-element-interactions -->
  <!-- svelte-ignore a11y-click-events-have-key-events -->
  <div class="modal-backdrop" on:click|self={() => (cfg = null)}>
    <div class="modal-content" role="dialog" aria-modal="true" style="max-width:640px; max-height:90vh; overflow-y:auto">
      <div class="row" style="align-items:center; justify-content:space-between; margin-bottom:12px">
        <h2 style="margin:0">Session Settings — {cfg.name || cfg.chat.Title || cfg.chat.WorkspacePath}</h2>
        <button class="btn sm" on:click={() => (cfg = null)}>✕</button>
      </div>
      <div class="card-sub" style="margin-bottom:12px">Switching the CLI starts a fresh session on the next message; the history stays.</div>

      <label class="lbl" for="cfg-doc-name">Session Name</label>
      <input id="cfg-doc-name" class="field" style="max-width:320px; margin-bottom:12px" bind:value={cfg.name} />

      <label class="lbl" for="cfg-doc-ws">Workspace folder</label>
      <div class="row" style="margin-bottom:12px">
        <input id="cfg-doc-ws" class="field grow mono" bind:value={cfg.workspacePath} placeholder="/path/to/workspace" />
        <button class="btn" type="button" on:click={async () => { const p = await api.pickFolder(); if (p) cfg.workspacePath = p }}>Browse…</button>
      </div>

      <label class="lbl" for="cfg-doc-cli">CLI</label>
      <select id="cfg-doc-cli" class="field" style="max-width:320px" bind:value={cfg.cli} on:change={cfgCliChanged}>
        {#if clis.length === 0}<option value={cfg.cli}>{cfg.cli} (probing CLIs…)</option>{/if}
        {#each clis as c}
          <option value={c.id} disabled={!c.available && c.id !== cfg.chat.CLIAgent}>
            {c.label}{c.available ? '' : ' — not installed'}
          </option>
        {/each}
      </select>

      <label class="lbl" for="cfg-doc-model" style="margin-top:10px">Model (blank = CLI default)</label>
      <input id="cfg-doc-model" class="field mono" style="max-width:420px" list="cfg-doc-model-suggestions" bind:value={cfg.model} />
      <datalist id="cfg-doc-model-suggestions">
        {#each cfg.suggestions as m}<option value={m}></option>{/each}
      </datalist>
      {#if cfg.modelLoading}<div class="card-sub">Loading models...</div>{/if}

      <label class="lbl" style="margin-top:10px">Tools</label>
      <div class="row">
        {#each toolLevelsForCli(cfg.cli) as lvl}
          <button class="btn sm" class:primary={cfg.tools === lvl.id} on:click={() => (cfg.tools = lvl.id)}>{lvl.label}</button>
        {/each}
      </div>

      <label class="row local-toggle">
        <input type="checkbox" checked={!!cfg.localEndpoint} on:change={(e) => {
          if (e.currentTarget.checked) {
            if (!localOpt?.configured) { error = 'Configure a Local LLM endpoint in settings first.'; e.currentTarget.checked = false; return }
            if (!supportsLocalRouting(cfg.cli)) { error = localRoutingUnavailableMessage(cfg.cli); e.currentTarget.checked = false; return }
            cfg.localEndpoint = localOpt.endpoint; cfg.localApiKey = localOpt.apiKey || ''; cfg.localModel = localOpt.defaultModel || ''
          } else { cfg.localEndpoint = ''; cfg.localApiKey = ''; cfg.localModel = '' }
        }} />
        <span>Route through local LLM</span>
      </label>
      {#if cfg.localEndpoint}
        <label class="lbl" for="cfg-doc-local-model" style="margin-top:8px">Local model</label>
        {#if localOpt.allModels?.length}
          <select id="cfg-doc-local-model" class="field mono" style="max-width:420px; margin-bottom:6px" bind:value={cfg.localModel}>
            {#each localOpt.allModels as m}<option value={m.name}>{m.name} ({m.provider})</option>{/each}
          </select>
        {:else}
          <input id="cfg-doc-local-model" class="field mono" style="max-width:420px; margin-bottom:6px" bind:value={cfg.localModel} placeholder="e.g. llama3.2:latest" />
        {/if}
      {/if}

      {#key cfg.chat.ID}<SkillBindingsEditor chatID={cfg.chat.ID} />{/key}

      <label class="lbl" style="margin-top:10px">MCP servers</label>
      {#if mcpServers.length === 0}
        <div class="card-sub">No enabled MCP servers.</div>
      {:else}
        <div class="mcp-grid">
          {#each mcpServers as s}
            <label class="mcp-card">
              <input type="checkbox" checked={cfg.mcps?.includes(s.id)} on:change={(e) => {
                if (e.currentTarget.checked) cfg.mcps = [...(cfg.mcps || []), s.id]
                else cfg.mcps = (cfg.mcps || []).filter((id) => id !== s.id)
              }} />
              <div>
                <strong>{s.name}</strong>
                <div class="card-sub">{s.command}</div>
              </div>
            </label>
          {/each}
        </div>
      {/if}

      <div class="row actions">
        <button class="btn" on:click={() => (cfg = null)}>Cancel</button>
        <button class="btn primary" on:click={saveConfig} disabled={cfgSaving}>Save changes</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .subtitle { color: var(--text-dim); margin-top: 0; margin-bottom: 16px; font-size: 13px; }
  .banner { background: var(--err-soft, rgba(248, 81, 73, 0.15)); border: 1px solid var(--err, #f85149); color: var(--text); padding: 8px 12px; border-radius: 6px; margin-bottom: 12px; white-space: pre-wrap; font-size: 13px; }
  .empty { color: var(--text-dim); text-align: center; padding: 24px; }
  .badge { background: var(--bg-surface); padding: 2px 8px; border-radius: 4px; font-size: 11px; }
  .studio-modal { max-width: 640px; max-height: 90vh; overflow-y: auto; }
  .local-toggle { margin-top: 12px; cursor: pointer; }
  .actions { justify-content: flex-end; margin-top: 16px; }
  .mcp-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 8px; margin-top: 8px; }
  .mcp-card { display: flex; align-items: flex-start; gap: 8px; padding: 8px; border: 1px solid var(--border); border-radius: 6px; cursor: pointer; }
</style>
