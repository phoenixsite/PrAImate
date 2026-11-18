<script>
  import { onMount, onDestroy } from 'svelte'
  import { api } from '../lib/api.js'
  import { activePage, showToast, showConfirm } from '../lib/stores.js'
  import { createStudioSession, studioProjectName } from '../lib/studioSession.js'
  import { focusDialog } from '../lib/focusDialog.js'
  import { toolLevelsForCli, normalizeToolsForCli } from '../lib/chatTools.js'

  let state
  const session = createStudioSession(api, next => { state = next })
  state = session.state
  let showOpen = false
  let preparing = false
  let disposed = false
  let extensionsDirectory = '', managedExtensionsDirectory = '', extensionsError = ''
  let extensionsLoading = true, extensionsSaving = false
  let form = { workspacePath: '', cli: 'praimate-code', model: '', agentID: '', tools: 'edits' }

  $: busy = preparing || extensionsSaving || !!state.operation || !!state.openingPath
  $: refreshing = state.loadingStatus || state.loadingProjects
  $: statusLabel = state.statusError ? 'Check failed' : state.loadingStatus ? 'Checking…' : ({
    installed: 'Ready', update_available: 'Integration needs refresh', broken: 'Needs repair', not_installed: 'Setup needed',
  }[state.status?.state] || 'Status unavailable')
  $: selectedCLI = state.clis.find(cli => cli.id === form.cli)
  $: permissionLevels = toolLevelsForCli(form.cli)
  $: permissionHint = permissionLevels.find(level => level.id === form.tools)?.hint || ''

  // Reactive button state updates on the next render. Check the live values
  // as well so two events in the same tick cannot open duplicate dialogs.
  function isBusy() { return preparing || extensionsSaving || state.operation || state.openingPath }
  function closeDialog() { if (!isBusy()) showOpen = false }

  function openDialog() {
    if (isBusy()) return
    showOpen = true
    session.loadChoices()
    session.loadModels(form.cli)
  }

  function changeCLI() {
    form.model = ''
    form.tools = normalizeToolsForCli(form.cli, form.tools)
    session.loadModels(form.cli)
  }

  async function maintain(operation) {
    const ok = await session.maintain(operation)
    if (ok && !disposed) showToast({ title: 'Studio ready', message: 'The editor environment is ready to use.', page: 'studio' })
    return ok
  }

  async function pickFolder() {
    if (isBusy()) return
    preparing = true
    try {
      const folder = await api.pickFolder()
      if (!disposed && folder) form.workspacePath = folder
    } catch (error) {
      if (!disposed) showToast({ title: 'Folder selection failed', message: String(error), tone: 'err', page: 'studio' })
    } finally { preparing = false }
  }

  async function openProject(projectPath = form.workspacePath) {
    if (isBusy() || state.loadingStatus || state.statusError || !state.status) return
    preparing = true
    // Snapshot the configuration before any confirmation or native dialog.
    const config = { ...form, workspacePath: projectPath.trim() }
    try {
      if (['not_installed', 'broken'].includes(state.status.state)) {
        const repair = state.status.state === 'broken'
        const accepted = await showConfirm({
          title: repair ? 'Repair Studio' : 'Set up Studio',
          message: repair ? 'Repair the editor environment before opening this project?' : 'Set up the Studio integration and download the editor if needed before opening this project?',
          confirmLabel: repair ? 'Repair and open' : 'Set up and open', tone: 'primary',
        })
        if (!accepted || disposed || !(await maintain(repair ? 'repair' : 'install'))) return
      }
      if (disposed) return
      const opened = await session.launch(config)
      if (opened && !disposed) {
        showOpen = false
        showToast({ title: 'Studio opened', message: studioProjectName(config.workspacePath), page: 'studio' })
      }
    } finally { preparing = false }
  }

  async function loadEditorPreferences() {
    if (disposed || state.status?.remote) { extensionsLoading = false; return }
    try {
      const prefs = await api.studioGetEditorPreferences()
      if (!disposed) { extensionsDirectory = prefs.extensionsDirectory || ''; managedExtensionsDirectory = prefs.managedDirectory || ''; extensionsError = '' }
    } catch (error) { if (!disposed) extensionsError = String(error) }
    finally { extensionsLoading = false }
  }

  async function pickExtensionsDirectory() {
    if (isBusy() || extensionsLoading) return
    preparing = true
    try {
      const folder = await api.pickFolder()
      if (!disposed && folder) extensionsDirectory = folder
    } catch (error) { if (!disposed) extensionsError = String(error) }
    finally { preparing = false }
  }

  async function saveEditorPreferences() {
    if (isBusy() || extensionsLoading || state.status?.remote) return
    extensionsSaving = true
    extensionsError = ''
    try {
      const prefs = await api.studioSaveEditorPreferences(extensionsDirectory.trim())
      if (!disposed) {
        extensionsDirectory = prefs.extensionsDirectory || ''
        managedExtensionsDirectory = prefs.managedDirectory || ''
        await session.refresh()
        if (!disposed) showToast({ title: 'Studio extensions saved', message: 'New Studio windows will use this extensions folder.', page: 'studio' })
      }
    } catch (error) { if (!disposed) extensionsError = String(error) }
    finally { extensionsSaving = false }
  }

  onMount(() => { session.refresh().then(loadEditorPreferences) })
  onDestroy(() => { disposed = true; session.dispose() })
</script>

<section class="studio" aria-label="Studio projects">
  <header class="studio-heading">
    <div><h1>Studio</h1><p class="subtitle">Open a project in your editor with PrAImate chats, agents and workers.</p></div>
    <div class="toolbar">
      <button class="btn" disabled={refreshing || busy} on:click={() => session.refresh()}>{refreshing ? 'Refreshing…' : 'Refresh'}</button>
      <button class="btn primary" disabled={busy} on:click={openDialog}>Open project</button>
    </div>
  </header>

  {#if state.error && !showOpen}<div class="banner" role="alert">{state.error}</div>{/if}
  {#if state.statusError}<div class="banner" role="alert">Could not check the editor: {state.statusError}<button class="btn sm" disabled={busy || state.loadingStatus} on:click={() => session.refresh()}>Retry check</button></div>{/if}

  <section aria-labelledby="recent-projects-title" aria-busy={state.loadingProjects}>
    <div class="section-heading"><h2 id="recent-projects-title">Recent projects</h2><span class="card-sub">{state.projects.length ? `${state.projects.length} workspace${state.projects.length === 1 ? '' : 's'}` : ''}</span></div>
    {#if state.projectsError}<div class="banner" role="alert">Could not load recent projects: {state.projectsError}</div>{/if}
    {#if state.loadingProjects && !state.projects.length}
      <div class="card empty-state" role="status"><p>Loading recent projects…</p></div>
    {:else if state.projects.length}
      <div class="project-grid">
        {#each state.projects as projectPath (projectPath)}
          <button class="card project-card" disabled={busy || state.loadingStatus || !!state.statusError || !state.status} on:click={() => openProject(projectPath)} aria-label={`Open ${studioProjectName(projectPath)} in Studio`} title={projectPath}>
            <svg class="project-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M3 7V5a2 2 0 0 1 2-2h5l2 3h7a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z" /></svg>
            <span class="project-info"><strong>{studioProjectName(projectPath)}</strong><span class="project-path">{projectPath}</span><span class="project-action">{state.openingPath === projectPath ? 'Opening…' : 'Open in Studio'}</span></span>
          </button>
        {/each}
      </div>
    {:else if !state.projectsError}
      <div class="card empty-state">
        <svg class="empty-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M3 3h18v18H3zM3 9h18M9 21V9" /></svg>
        <h3>Your projects start here</h3>
        <p>Choose a project folder and a CLI. Recently opened workspaces will appear here.</p>
        <button class="btn primary" disabled={busy} on:click={openDialog}>Open project</button>
      </div>
    {/if}
  </section>

  <details class="card environment">
    <summary><span>Editor environment</span><span class="pill" class:ok={state.status?.state === 'installed' && !state.statusError} class:err={!!state.statusError || state.status?.state === 'broken'}>{statusLabel}</span></summary>
    <div class="environment-body">
      {#if state.statusError}
        <p class="card-sub">The editor status is unavailable. Retry the check above before opening a project.</p>
      {:else if state.status}
        <p class="card-sub">{state.status.state === 'not_installed' ? 'Use an installed VS Code or VSCodium editor, or set up the managed editor.' : 'Maintain the editor and its PrAImate integration.'}</p>
        <dl class="environment-facts">
          <div><dt>Editor</dt><dd>{state.status.binaryPath ? studioProjectName(state.status.binaryPath) : 'Not detected'}</dd></div>
          <div><dt>PrAImate</dt><dd>{state.status.version || '—'}</dd></div>
          <div><dt>Extension</dt><dd>{state.status.state === 'installed' ? 'Installed and current' : 'Setup or refresh needed'}</dd></div>
          <div><dt>Backend</dt><dd>{state.status.backendRunning ? 'Active' : 'Starts when opened'}</dd></div>
        </dl>
        <div class="toolbar">
          {#if state.status.state === 'not_installed'}
            <button class="btn" disabled={busy || state.loadingStatus} on:click={() => maintain('install')}>Set up editor</button>
          {:else}
            <button class="btn" disabled={busy || state.loadingStatus} on:click={() => maintain('update')}>Update integration</button>
            <button class="btn" disabled={busy || state.loadingStatus} on:click={() => maintain('repair')}>Repair</button>
          {/if}
          <button class="btn" disabled={busy} on:click={() => activePage.set('clis')}>CLI &amp; Tools</button>
        </div>
      {:else}<p class="card-sub" role="status">Checking the editor environment…</p>{/if}
      {#if state.operation}<p class="card-sub" role="status">{state.operation === 'install' ? 'Setting up Studio…' : state.operation === 'repair' ? 'Repairing Studio…' : 'Updating integration…'}</p>{/if}
      {#if !state.status?.remote}
        <fieldset class="extensions-settings" disabled={busy || extensionsLoading}>
          <legend>Extensions folder</legend>
          <p class="card-sub">Use an existing VS Code / VSCodium extensions folder to load your personal extensions. Leave blank for PrAImate's managed folder. Studio settings and history stay private.</p>
          <label class="lbl" for="studio-extensions">Existing extensions directory</label>
          <div class="folder-row"><input id="studio-extensions" class="field mono" bind:value={extensionsDirectory} placeholder={managedExtensionsDirectory || 'Managed Studio extensions'} /><button class="btn" type="button" on:click={pickExtensionsDirectory}>Browse…</button></div>
          <p class="card-sub">PrAImate installs or refreshes its integration in the selected folder. Other extensions are preserved. Existing windows keep their current extensions.</p>
          <div class="toolbar"><button class="btn primary" type="button" on:click={saveEditorPreferences}>{extensionsSaving ? 'Saving…' : 'Save and refresh'}</button><button class="btn" type="button" on:click={() => { extensionsDirectory = ''; saveEditorPreferences() }}>Use managed folder</button></div>
        </fieldset>
        {#if extensionsError}<p class="banner" role="alert">{extensionsError}</p>{/if}
      {/if}
    </div>
  </details>
</section>

{#if showOpen}
  <!-- svelte-ignore a11y-click-events-have-key-events -->
  <!-- svelte-ignore a11y-no-static-element-interactions -->
  <div class="modal-backdrop" on:click|self={closeDialog}>
    <form class="modal-content studio-modal" role="dialog" aria-modal="true" aria-labelledby="open-project-title" use:focusDialog={{ onClose: closeDialog }} on:submit|preventDefault={() => openProject()}>
      <div class="dialog-heading"><h2 id="open-project-title">Open project</h2><button class="btn sm" type="button" aria-label="Close project dialog" disabled={busy} on:click={closeDialog}>Close</button></div>
      <p class="card-sub">Choose your workspace and how you want to work.</p>
      {#if state.error}<div class="banner" role="alert">{state.error}</div>{/if}
      {#if state.choicesError}<div class="banner" role="alert">Could not load CLI and agent options: {state.choicesError}<button class="btn sm" type="button" disabled={state.loadingChoices} on:click={() => session.loadChoices(true)}>Retry</button></div>{/if}
      <label class="lbl" for="studio-folder">Project folder</label>
      <div class="folder-row"><input id="studio-folder" class="field mono" bind:value={form.workspacePath} placeholder="Path to your project" required disabled={busy} /><button class="btn" type="button" disabled={busy} on:click={pickFolder}>Browse…</button></div>
      <div class="config-grid">
        <div><label class="lbl" for="studio-cli">CLI</label><select id="studio-cli" class="field" bind:value={form.cli} disabled={busy || state.loadingChoices} on:change={changeCLI}>
          {#each state.clis as cli}<option value={cli.id}>{cli.label || cli.id}{cli.available ? '' : ' (not installed)'}</option>{/each}
          {#if !state.clis.some(cli => cli.id === 'praimate-code')}<option value="praimate-code">PrAImate Code</option>{/if}
        </select></div>
        <div><label class="lbl" for="studio-agent">Agent</label><select id="studio-agent" class="field" bind:value={form.agentID} disabled={busy || state.loadingChoices}>
          <option value="">No agent</option>{#each state.agents as agent}<option value={agent.id}>{agent.name}</option>{/each}
        </select></div>
      </div>
      {#if state.loadingChoices}<p class="card-sub" role="status">Detecting CLIs and loading agents…</p>{/if}
      <label class="lbl" for="studio-model">Model</label>
      <input id="studio-model" class="field" list="studio-models" bind:value={form.model} placeholder="CLI default" disabled={busy} />
      <datalist id="studio-models">{#each state.models as model}<option value={model}></option>{/each}</datalist>
      <div class="model-actions"><span class="card-sub">{state.loadingModels ? 'Loading models…' : 'Leave blank to use the CLI default.'}</span><button class="btn sm" type="button" disabled={busy || state.loadingModels} on:click={() => session.loadModels(form.cli, true)}>Refresh models</button></div>
      {#if state.modelError}<p class="model-error" role="alert">{state.modelError}</p>{/if}
      <label class="lbl" for="studio-permissions">Tools</label>
      <select id="studio-permissions" class="field" bind:value={form.tools} disabled={busy}>{#each permissionLevels as level}<option value={level.id}>{level.label}</option>{/each}</select>
      <p class="card-sub">{permissionHint}</p>
      {#if state.statusError}<div class="banner" role="alert">Could not check the editor. Close this dialog and retry the environment check.</div>
      {:else if state.status?.state === 'not_installed'}<p class="card-sub">Studio will ask to set up the editor before opening your project.</p>{/if}
      <div class="dialog-actions"><button class="btn" type="button" disabled={busy} on:click={closeDialog}>Cancel</button><button class="btn primary" type="submit" disabled={busy || state.loadingChoices || state.loadingStatus || !!state.statusError || !state.status || selectedCLI?.available === false}>{busy ? (state.operation ? 'Setting up…' : 'Opening…') : 'Open in Studio'}</button></div>
    </form>
  </div>
{/if}

<style>
  .extensions-settings { margin: 20px 0 0; padding: 16px; border: 1px solid var(--border); border-radius: var(--radius-sm); min-width: 0; }
  .extensions-settings legend { font-weight: 600; }
  .studio-heading, .section-heading, .dialog-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
  .studio-heading { align-items: flex-start; margin-bottom: 26px; }
  .studio-heading .subtitle { margin-bottom: 0; max-width: 65ch; }
  .toolbar { display: flex; flex-wrap: wrap; gap: 8px; }
  .section-heading { margin-bottom: 12px; }
  .section-heading h2 { margin: 0; font-size: 15px; font-weight: 600; }
  .project-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 280px), 1fr)); gap: 12px; }
  .project-card { display: flex; gap: 14px; align-items: flex-start; text-align: left; color: var(--text); margin: 0; min-width: 0; }
  .project-card:hover:enabled { border-color: var(--accent); background: var(--bg-raised); }
  .project-icon, .empty-icon { width: 26px; height: 26px; fill: none; stroke: var(--accent); stroke-width: 1.5; flex: none; }
  .project-info { display: flex; flex-direction: column; gap: 8px; min-width: 0; }
  .project-info strong, .project-path { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .project-path { color: var(--text-dim); font-size: 12px; }
  .project-action { font-size: 12px; color: var(--accent); margin-top: 5px; }
  .empty-state { text-align: center; padding: 36px 20px; }
  .empty-state h3 { margin: 12px 0 6px; font-size: 16px; }
  .empty-state p { color: var(--text-dim); margin: 0 auto 18px; max-width: 56ch; }
  .environment { margin-top: 28px; box-shadow: none; }
  summary { display: flex; align-items: center; gap: 12px; cursor: pointer; font-size: 13px; }
  summary::before { content: '›'; color: var(--text-dim); font-size: 18px; }
  details[open] summary::before { transform: rotate(90deg); }
  .environment-body { border-top: 1px solid var(--border); margin-top: 14px; padding-top: 12px; }
  .environment-facts { display: flex; flex-wrap: wrap; gap: 16px 36px; margin: 16px 0; }
  dt { color: var(--text-dim); font-size: 12px; margin-bottom: 4px; }
  dd { margin: 0; font-size: 13px; overflow-wrap: anywhere; }
  .banner { border: 1px solid var(--err); background: color-mix(in srgb, var(--err) 8%, var(--bg-panel)); padding: 12px; border-radius: var(--radius); margin: 12px 0; white-space: pre-wrap; overflow-wrap: anywhere; }
  .banner button { margin-left: 8px; }
  .studio-modal { max-width: 560px; }
  .folder-row { display: flex; gap: 8px; }
  .folder-row input { flex: 1; min-width: 0; }
  .config-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
  .config-grid select, .studio-modal > .field { width: 100%; }
  .model-actions { display: flex; justify-content: space-between; align-items: center; gap: 12px; margin-top: 8px; }
  .model-error { color: var(--err); font-size: 12px; }
  .dialog-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 24px; }
  @media (max-width: 700px) { .studio-heading { flex-direction: column; } .config-grid { grid-template-columns: 1fr; gap: 0; } .studio-modal { margin: 16px; width: calc(100% - 32px); } }
</style>
