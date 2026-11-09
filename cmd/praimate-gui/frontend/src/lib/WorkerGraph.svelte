<script>
  import { createEventDispatcher } from 'svelte'
  import { api } from './api.js'
  import { showConfirm } from './stores.js'
  import { workerTaskRoute, workerTaskHasOverrides } from './workerActivity.js'
  export let snapshot
  export let clis = []
  export let models = {}
  const dispatch = createEventDispatcher()
  let tasks = []
  let parallel = 2
  let draftID = ''
  let busy = false
  let error = ''
  let localCatalog = null
  let loadingLocal = false
  $: editable = snapshot?.status === 'draft'
  $: active = ['running', 'planning', 'merging'].includes(snapshot?.status)
  $: needsReset = snapshot?.dag?.tasks?.some(t => ['failed', 'cancelled', 'blocked'].includes(t.status))
  $: if (snapshot?.dag && snapshot.id !== draftID) {
    draftID = snapshot.id
    tasks = JSON.parse(JSON.stringify(snapshot.dag.tasks || []))
    parallel = snapshot.dag.maxParallel || 2
  }
  $: if (editable && !tasks.length && snapshot?.dag?.tasks?.length) tasks = JSON.parse(JSON.stringify(snapshot.dag.tasks))
  $: if (editable && tasks.some(t => runtimeFor(t) === 'native') && localCatalog === null && !loadingLocal) loadLocalModels()
  async function loadLocalModels() {
    loadingLocal = true
    try { localCatalog = (await api.localLLMHostsModels()) || [] }
    catch { localCatalog = [] }
    finally { loadingLocal = false }
  }
  function profileFor(task) { return snapshot.profiles.find(p => p.tier === (task.worker?.profile || 'middle')) || {} }
  function cliFor(task) { return task.worker.cli || profileFor(task).cli || '' }
  function runtimeFor(task) { return task.worker.runtime || profileFor(task).runtime || 'cli' }
  function routeLabel(task, recorded = false) { const route = workerTaskRoute(task, snapshot, recorded); return `${route.runtime === 'native' ? 'Local' : route.cli} / ${route.model || 'Backend default'}` }
  function profileChanged(task) { task.worker = { profile: task.worker.profile }; tasks = tasks; dispatch('models', cliFor(task)) }
  async function action(callback) {
    if (busy) return
    busy = true; error = ''
    try { await callback(); dispatch('refresh') }
    catch (e) { error = String(e) }
    finally { busy = false }
  }
  export async function execute() {
    await action(async () => {
      if (editable) await api.saveWorkerDAG(snapshot.id, tasks, parallel)
      await api.executeWorkerDAG(snapshot.id)
    })
  }
  async function merge() {
    if (await showConfirm({ title: 'Merge accepted worker changes?', message: 'Accepted commits will be integrated into the target branch. The workspace must be clean. Conflicts stop integration and remain in a separate review worktree.', confirmLabel: 'Merge accepted', tone: 'primary' })) await action(() => api.mergeWorkerDAG(snapshot.id))
  }
  async function reset(task) {
    if (await showConfirm({ title: 'Discard failed task work?', message: 'This removes the task’s temporary worktree and all uncommitted changes inside it. Inspect the path shown below before continuing.', confirmLabel: 'Discard and reset' })) await action(() => api.resetWorkerDAGTask(snapshot.id, task.id))
  }
</script>

<section class="graph" aria-label="Parallel task graph">
  <div class="graph-head"><h3>Parallel task graph</h3><span>{snapshot.dag.targetBranch} · base {snapshot.dag.baseCommit?.slice(0, 10)}</span></div>
  {#if editable}
    <p class="hint">Review the plan and each worker route before starting. Independent tasks run in separate worktrees; dependencies start from their combined changes.</p>
    <div class="actions"><button disabled={busy} on:click={() => action(() => api.saveWorkerDAG(snapshot.id, tasks, parallel))}>Save plan</button><button class="primary" disabled={busy || !tasks.length} on:click={execute}>Run task graph</button></div>
    <label>Concurrent tasks <input type="number" min="1" max="4" bind:value={parallel} /></label>
    {#each tasks as task, index (task.id)}
      <fieldset disabled={busy}>
        <legend>{task.id}</legend>
        <label>Assignment and checks <textarea rows="3" bind:value={tasks[index].description}></textarea></label>
        <label>Dependencies <input value={(task.dependencies || []).join(', ')} placeholder="task-a, task-b" on:input={e => { task.dependencies = e.currentTarget.value.split(',').map(x => x.trim()).filter(Boolean); tasks = tasks }} /></label>
        <div class="route">
          <label>Worker profile <select bind:value={task.worker.profile} on:change={() => profileChanged(task)}><option value="primary">Reasoner</option><option value="middle">Middle</option><option value="fast">Fast</option></select></label>
          <label>Backend <select value={runtimeFor(task)} on:change={e => { task.worker.runtime = e.currentTarget.value; task.worker.cli = task.worker.runtime === 'cli' ? profileFor(task).cli || clis.find(c => c.available && c.capabilities?.managedWorker !== false)?.id || '' : ''; task.worker.endpoint = task.worker.runtime === 'native' ? profileFor(task).endpoint || snapshot.profiles.find(p => p.runtime === 'native')?.endpoint || 'http://localhost:11434/v1' : ''; tasks = tasks; dispatch('models', cliFor(task)) }}><option value="cli">CLI</option><option value="native">Local model</option></select></label>
          {#if runtimeFor(task) === 'cli'}
            <label>CLI <select value={cliFor(task)} on:change={e => { task.worker.cli = e.currentTarget.value; task.worker.model = ''; tasks = tasks; dispatch('models', task.worker.cli) }}>
              {#each clis as cli}<option value={cli.id} disabled={!cli.available || cli.capabilities?.managedWorker === false}>{cli.label}{!cli.available ? ' (unavailable)' : ''}</option>{/each}
            </select></label>
          {:else}
            <label>Endpoint <input bind:value={task.worker.endpoint} placeholder={profileFor(task).endpoint || 'http://localhost:11434/v1'} list={'dag-hosts-' + task.id} /></label>
            <datalist id={'dag-hosts-' + task.id}>{#each localCatalog || [] as host}<option value={host.endpoint}>{host.name}</option>{/each}</datalist>
          {/if}
          <label>Model <input bind:value={task.worker.model} placeholder={profileFor(task).model || 'Model ID or alias'} list={'dag-models-' + task.id} /></label>
          <datalist id={'dag-models-' + task.id}>{#each (runtimeFor(task) === 'native' ? (localCatalog || []).filter(host => host.endpoint === (task.worker.endpoint || profileFor(task).endpoint)).flatMap(host => host.models || []) : models[cliFor(task)] || []) as model}<option value={model}></option>{/each}</datalist>
        </div>
        <p class="hint">Provider routing follows the selected CLI's existing configuration or local endpoint. Permissions and limits come from this worker profile.</p>
      </fieldset>
    {/each}
  {:else}
    {#if active && (snapshot.dag.tasks || []).some(t => t.status === 'completed' && t.review === 'pending')}<p class="hint">Accept and reject unlock when the task graph finishes executing.</p>{/if}
    <div class="task-cards">
      {#each snapshot.dag.tasks || [] as task (task.id)}
        <article>
          <div class="task-title"><strong>{task.id}</strong><span class="status">{task.status}{task.review ? ' · ' + task.review : ''}</span></div>
          <p>{task.description}</p>
          <p class="hint">Depends on: {(task.dependencies || []).join(', ') || 'none'}</p>
          {#if ['pending', 'failed', 'cancelled', 'blocked'].includes(task.status)}
            <p class="route-label">Next attempt: {routeLabel(task)}<br />{workerTaskHasOverrides(task) ? 'Task-specific route overrides the run profile.' : 'Uses the current run profile.'}</p>
            {#if task.worker?.provider}<p class="hint">Last attempt: {routeLabel(task, true)}</p>{/if}
            {#if !active && workerTaskHasOverrides(task)}<button disabled={busy} on:click={() => action(() => api.useWorkerDAGTaskProfile(snapshot.id, task.id))}>Use current profile</button>{/if}
          {:else}
            <p class="route-label">{task.worker.profile || 'middle'} · {routeLabel(task)}<br />{task.worker.provider || 'Provider configured by backend'}</p>
          {/if}
          {#if task.worktree}<p class="path">{task.worktree.path}<br />{task.worktree.branch}</p>{/if}
          {#if task.error}<p class="error" role="alert">{task.error}</p>{/if}
          {#if task.output}<details><summary>Worker result</summary><pre>{task.output}</pre></details>{/if}
          {#if task.result}
            <p class="hint">{task.result.baseCommit.slice(0, 10)} → {task.result.resultCommit.slice(0, 10)}</p>
            <p class="path">{task.result.changedFiles?.join(', ') || 'No file changes'}</p>
            <details><summary>Git diff{task.result.diffTruncated ? ' (preview)' : ''}</summary><pre class="diff">{task.result.diff || 'No changes'}</pre></details>
            {#if task.review !== 'merged'}<div class="actions"><button disabled={busy || active} title={active ? 'Available when the task graph finishes executing' : ''} on:click={() => action(() => api.reviewWorkerDAGTask(snapshot.id, task.id, 'accepted'))}>Accept</button><button disabled={busy || active} title={active ? 'Available when the task graph finishes executing' : ''} on:click={() => action(() => api.reviewWorkerDAGTask(snapshot.id, task.id, 'rejected'))}>Reject</button></div>{/if}
          {/if}
          {#if !active && ['failed', 'cancelled', 'blocked'].includes(task.status)}<div class="actions">{#if task.failurePhase === 'dependencies'}<button class="primary" disabled={busy} on:click={() => action(async () => {await api.resolveWorkerDAGTaskConflicts(snapshot.id,task.id);await api.executeWorkerDAG(snapshot.id)})}>Resolve conflicts with worker</button>{/if}<button disabled={busy} on:click={() => action(async () => { await api.retryWorkerDAGTask(snapshot.id,task.id);await api.executeWorkerDAG(snapshot.id) })}>{task.failurePhase === 'dependencies' ? 'Continue after manual resolution' : 'Continue with existing changes'}</button><button disabled={busy} on:click={() => reset(task)}>Start over…</button></div>{/if}
          <details><summary>Task activity</summary>{#each (snapshot.events || []).filter(e => e.taskID === task.id).slice(-40) as event}<small>{event.kind}</small><pre>{event.text}</pre>{/each}</details>
        </article>
      {/each}
    </div>
    {#if !active}
      {#if needsReset}<p class="hint">Continue unsuccessful tasks with their existing changes, or explicitly start over. Previous attempts remain in the history.</p>{/if}
      <div class="actions">
        {#if snapshot.dag.tasks?.some(t => t.status === 'pending')}<button disabled={busy} on:click={execute}>Run ready tasks</button>{/if}
        <button disabled={busy || !snapshot.dag.tasks?.some(t => t.review === 'accepted')} on:click={merge}>Merge accepted changes</button>
        <button disabled={busy} on:click={async () => { if (await showConfirm({ title: 'Clean reviewed worktrees?', message: 'This removes rejected and merged task worktrees, plus any temporary merge attempt. Saved diffs and commits remain in the run history.', confirmLabel: 'Clean worktrees' })) action(() => api.cleanupWorkerDAG(snapshot.id)) }}>Clean reviewed worktrees</button>
      </div>
    {/if}
    {#if snapshot.dag.mergeWorktree}<p class="path">Merge worktree: {snapshot.dag.mergeWorktree.path}</p>{/if}
  {/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</section>

<style>
  .graph { margin: 16px 0; padding: 16px; border: 1px solid var(--border); border-radius: var(--radius); background: var(--bg-panel); }
  .graph-head, .task-title, .actions { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
  .graph-head { justify-content: space-between; } h3 { margin: 0; }
  .graph-head span, .hint { color: var(--text-dim); font-size: 12px; line-height: 1.5; }
  fieldset { min-width: 0; border: 1px solid var(--border); border-radius: 10px; margin: 12px 0; padding: 12px; display: grid; gap: 10px; }
  label { display: grid; gap: 5px; font-size: 12px; } input, select, textarea { min-width: 0; max-width: 100%; background: var(--bg); color: var(--text); border: 1px solid var(--border); border-radius: 7px; padding: 8px; }
  .route { display: grid; gap: 10px; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); }
  .task-cards { display: grid; gap: 12px; margin: 12px 0; grid-template-columns: repeat(auto-fit, minmax(min(100%, 300px), 1fr)); }
  article { min-width: 0; border: 1px solid var(--border); border-radius: 10px; padding: 12px; background: var(--bg); }
  .task-title { justify-content: space-between; } .status { font-size: 11px; color: var(--text-dim); }
  .route-label { font-size: 12px; } .path { font: 11px var(--font-mono, monospace); overflow-wrap: anywhere; color: var(--text-dim); }
  summary { cursor: pointer; margin: 10px 0; font-size: 12px; }
  pre { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 320px; overflow: auto; font-size: 11px; } .diff { background: var(--bg-elevated, var(--bg)); padding: 8px; }
  .error { color: var(--danger, #d74453); overflow-wrap: anywhere; } button { padding: 7px 12px; border-radius: var(--radius-sm); background: var(--bg-panel); border: 1px solid var(--border-bright); color: var(--text); cursor: pointer; transition: background 140ms var(--ease); } button:hover:not(:disabled) { background: var(--bg-raised); } button:disabled { opacity: .5; cursor: default; } .primary { background: var(--accent); color: var(--accent-fg); }
</style>
