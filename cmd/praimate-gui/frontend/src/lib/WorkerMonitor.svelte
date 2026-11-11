<script>
  import { createEventDispatcher, tick } from 'svelte'
  import { api } from './api.js'
  import { focusDialog } from './focusDialog.js'
  import { showConfirm } from './stores.js'
  import WorkerGraph from './WorkerGraph.svelte'
  import WorkerBoard from './WorkerBoard.svelte'
  import WorkerProfiles from './WorkerProfiles.svelte'
  import VoiceButton from './VoiceButton.svelte'
  import { workerBoardItems, mergeWorkerEvents, workerRunSummary } from './workerBoard.js'
  import { activeWorkerRun, workerExecutions, workerEventLabel, workerLabel, workerUsage, shouldRevealWorkerPlan } from './workerActivity.js'
  export let snapshot
  export let clis = []
  export let models = {}
  export let approvals = []
  const dispatch = createEventDispatcher()
  let selected = ''
  let selectedTask = ''
  let filter = 'all'
  let tab = 'activity'
  let follow = true
  let focusActive = true
  let timeline
  let now = Date.now()
  let busy = false
  let error = ''
  let settings = null
  let showGraph = false
  let graph
  async function runPlan() {
    if (busy || active) return
    const runID = snapshot.id
    showGraph = true
    await tick()
    if (snapshot.id === runID && snapshot.status === 'draft') await graph?.execute()
  }
  let revealedPlan = ''
  let draft = ''
  let lastRun = ''
  let lastActivity = ''
  let visibleCount = 120
  let viewedWorker = ''
  let history = []
  let before = 0
  let hasMore = false
  let historyBusy = false
  let preview = null
  let previewKey = ''
  let previewBusy = false
  $: active = activeWorkerRun(snapshot)
  $: executions = workerExecutions(snapshot)
  $: tasks = snapshot.dag?.tasks || []
  $: board = workerBoardItems(snapshot)
  $: summary = workerRunSummary(snapshot)
  $: if (snapshot.id !== lastRun) { selected = ''; selectedTask = ''; lastRun = snapshot.id; viewedWorker = ''; history = []; error = ''; draft = ''; settings = null; showGraph = snapshot.status === 'draft' }
  $: if (shouldRevealWorkerPlan(snapshot, revealedPlan)) { revealedPlan = snapshot.id; showGraph = true }
  $: if (!selectedTask && !executions.some(e => e.id === selected)) {
    const live = executions.find(e => ['running', 'waiting'].includes(e.status))
    const attention = board.find(item => item.status === 'retrying' && item.attemptID) || board.find(item => item.column === 'attention' && item.attemptID)
    selected = live?.id || attention?.attemptID || executions.at(-1)?.id || ''
    selectedTask = live?.taskID || attention?.taskID || ''
  }
  $: current = executions.find(e => e.id === selected)
  $: currentTask = tasks.find(task => task.id === (selectedTask || current?.taskID))
  $: taskAttempts = currentTask ? executions.filter(attempt => attempt.taskID === currentTask.id) : executions.filter(attempt => !attempt.taskID && attempt.tier === current?.tier)
  $: if (focusActive && active && executions.length) selectActive(executions)
  $: if (viewedWorker !== snapshot.id + ':' + selected) { viewedWorker = snapshot.id + ':' + selected; visibleCount = 120; history = []; before = 0; hasMore = false; historyBusy = false; loadHistory(true) }
  $: allEvents = mergeWorkerEvents(history, current?.events || [])
  $: events = allEvents.filter(e => filter === 'all' || (filter === 'io' ? ['input','request','stream','output','response'].includes(e.kind) : filter === 'delegation' ? ['delegation','delegated_result','delegated_error'].includes(e.kind) : filter === 'errors' ? ['error','failed','delegated_error'].includes(e.kind) : e.kind === 'reasoning'))
  $: usage = workerUsage(snapshot)
  $: activityKey = selected + ':' + filter + ':' + events.length + ':' + events.at(-1)?.text?.length
  $: if (activityKey !== lastActivity) { lastActivity = activityKey; if (follow) scrollLatest() }
  $: nextPreviewKey = snapshot.id + ':' + currentTask?.id + ':' + currentTask?.status + ':' + currentTask?.result?.resultCommit
  $: if (tab === 'changes' && currentTask && nextPreviewKey !== previewKey) { previewKey = nextPreviewKey; loadPreview() }
  function clock(node) { const timer = setInterval(() => now = Date.now(), 1000); return { destroy: () => clearInterval(timer) } }
  function elapsed(start, end = now) { if (!start || new Date(start).getTime() <= 0) return '0m 0s'; const seconds = Math.max(0, Math.floor((Number(end) - new Date(start).getTime()) / 1000)); return Math.floor(seconds / 60) + 'm ' + seconds % 60 + 's' }
  async function scrollLatest() { await tick(); if (timeline) timeline.scrollTop = timeline.scrollHeight }
  function selectActive(list) { const worker = list.find(e => e.id === selected && e.status === 'running') || list.find(e => e.status === 'running') || list.find(e => e.status === 'waiting') || list.find(e => tasks.some(task => task.id === e.taskID && task.status === 'retrying')); if (worker) { selected = worker.id; selectedTask = worker.taskID || '' } }
  function chooseCard(item) { selectedTask = item.taskID; selected = item.attemptID; focusActive = false; tab = 'activity' }
  async function loadHistory(reset = false) {
    const key = snapshot.id + ':' + selected
    if (!selected || !snapshot.activityVersion) return
    historyBusy = true
    try {
      const page = await api.workerRunActivity(snapshot.id, selected, reset ? 0 : before, 100)
      if (key !== snapshot.id + ':' + selected) return
      history = mergeWorkerEvents(reset ? [] : history, page.events || [])
      before = page.before; hasMore = page.hasMore
    } catch (e) { if (key === snapshot.id + ':' + selected) error = String(e) }
    finally { if (key === snapshot.id + ':' + selected) historyBusy = false }
  }
  async function earlier() { follow = false; if (visibleCount >= events.length && hasMore) await loadHistory(); visibleCount += 120 }
  async function loadPreview() {
    const key = nextPreviewKey
    preview = null
    previewBusy = false
    if (currentTask?.result) { preview = {diff:currentTask.result.diff,status:currentTask.result.changedFiles?.join('\n'),truncated:currentTask.result.diffTruncated}; return }
    if (!currentTask?.worktree) return
    previewBusy = true
    try { const value = await api.workerTaskPreview(snapshot.id, currentTask.id); if (key === nextPreviewKey) preview = value }
    catch (e) { if (key === nextPreviewKey) error = String(e) }
    finally { if (key === nextPreviewKey) previewBusy = false }
  }
  async function action(fn) { busy = true; error = ''; const id = snapshot.id; try { await fn(); if (snapshot.id === id) dispatch('refresh') } catch (e) { if (snapshot.id === id) error = String(e) } finally { busy = false } }
  function openSettings() { settings = JSON.parse(JSON.stringify({ workspace: snapshot.workspace, profiles: snapshot.profiles, accessMode:snapshot.accessMode || 'supervised',maxRetries:snapshot.maxRetries || 0,retryDelaySeconds:snapshot.retryDelaySeconds ?? 0 })); for (const p of settings.profiles) if (p.runtime === 'cli') dispatch('models', p.cli) }
  async function saveSettings() { await action(async () => { await api.updateWorkerRunConfig(snapshot.id, settings); settings = null }) }
  async function retryWithoutTimeout() {
    const id = snapshot.id
    const config = { workspace:snapshot.workspace,accessMode:snapshot.accessMode || 'supervised',maxRetries:snapshot.maxRetries || 0,retryDelaySeconds:snapshot.retryDelaySeconds || 0, profiles:snapshot.profiles.map(p => ({...p,timeoutSeconds:p.tier === 'primary' ? 0 : p.timeoutSeconds})) }
    await action(async () => { await api.updateWorkerRunConfig(id, config); await api.retryWorkerPlanning(id) })
  }
  async function continueRun() { if (draft.trim()) await action(async () => { await api.continueWorkerRun(snapshot.id, draft); draft = '' }) }
  async function continueTask() { if (currentTask) await action(async () => { await api.retryWorkerDAGTask(snapshot.id,currentTask.id); await api.executeWorkerDAG(snapshot.id) }) }
  async function resolveConflicts(task = currentTask) { if (task) await action(async () => { await api.resolveWorkerDAGTaskConflicts(snapshot.id,task.id); await api.executeWorkerDAG(snapshot.id) }) }
  async function startOver() { if (currentTask && await showConfirm({title:'Discard this task’s changes?',message:`Delete the worktree for ${currentTask.id} and start this task again? Completed tasks and their results are retained. Inspect Changes first.`,confirmLabel:'Discard & reset'})) await action(() => api.resetWorkerDAGTask(snapshot.id,currentTask.id)) }
</script>

<section class="monitor" aria-label="Worker execution monitor" use:clock>
  <div class="toolbar">
    <div class="run-state"><span class:live={active} class:failed={snapshot.status === 'failed'} class="dot"></span><strong>{summary.label}</strong><span class="muted">{tasks.length ? tasks.filter(t => t.status === 'completed').length + '/' + tasks.length + ' tasks complete' : executions.length + ' assignments'}</span><span class="access-chip">{snapshot.accessMode === 'full' ? 'Full access' : 'Supervised'}</span><span class="usage-chip">{usage.calls ? (usage.input + usage.output).toLocaleString() + ' tokens · ' + usage.calls + ' calls' : 'Usage not reported'}</span></div>
    <div class="buttons"><label><input type="checkbox" bind:checked={focusActive} /> Follow active</label><button disabled={busy || active} on:click={openSettings}>Run settings</button>{#if active}<button class="stop" disabled={busy} on:click={() => action(() => api.cancelWorkerRun(snapshot.id))}>Stop</button>{/if}</div>
  </div>
  {#if approvals.length}<div class="attention-banner" role="status"><strong>{approvals.length} approval request{approvals.length === 1 ? '' : 's'} need your attention.</strong><span>Execution resumes after you respond.</span></div>{/if}
  {#if summary.total}<div class="progress-summary" aria-label="Task progress"><div class="progress-track"><span style={'width:' + summary.completed.length / summary.total * 100 + '%'}></span></div><div><span>{summary.completed.length}/{summary.total} complete</span><span>{summary.working.length} working</span><span>{summary.retrying.length} retrying</span><span>{summary.failed.length} failed</span><span>{summary.blocked.length} blocked</span></div></div>{/if}
  {#if active && summary.review.length}<p class="muted" role="status">{summary.review.length} completed result{summary.review.length === 1 ? '' : 's'} can be accepted or rejected when the task graph finishes executing.</p>{/if}
  {#if snapshot.nextRetryAt}<div class="retry-banner" role="status"><strong>Automatic retry {snapshot.autoRetriesUsed}/{snapshot.maxRetries}</strong><span>Next attempt at {new Date(snapshot.nextRetryAt).toLocaleTimeString()}. Stop cancels the retry.</span></div>{/if}
  {#if snapshot.error}<section class="failure" role="alert">
    <header><strong>{summary.failed.length ? summary.failed.length + ' task(s) need attention' : 'Execution needs attention'}</strong><button disabled={busy || active} on:click={openSettings}>Adjust run settings</button></header>
    {#if summary.failed.length}{#each summary.failed as task}<div class="failed-task"><div><strong>{task.id}</strong><p>{task.error}</p><small>{task.autoRetriesUsed ? task.autoRetriesUsed + '/' + (snapshot.maxRetries || 0) + ' automatic retries used. ' : ''}Existing changes are retained.</small></div><div class="buttons">{#if task.failurePhase === 'dependencies'}<button class="btn primary" disabled={busy || active} on:click={() => resolveConflicts(task)}>Resolve conflicts with worker</button>{/if}<button on:click={() => { chooseCard(board.find(item => item.taskID === task.id)); tab = task.failurePhase === 'dependencies' ? 'changes' : 'activity'; filter = 'errors' }}>Inspect task</button></div></div>{/each}
      {#if summary.blocked.length}<p class="muted">{summary.blocked.length} dependent task(s) will become available after their failed dependency completes.</p>{/if}
      {#if summary.completed.length}<small>{summary.completed.length} completed result(s) are available in Changes for review.</small>{/if}
    {:else}<p>{snapshot.error}</p><small>Previous attempts are saved. Review them before continuing.</small>{/if}
    {#if snapshot.dag && !tasks.length && !active}<div class="buttons"><button disabled={busy} on:click={() => action(() => api.retryWorkerPlanning(snapshot.id))}>Retry planning</button>{#if snapshot.profiles?.some(p => p.tier === 'primary' && p.timeoutSeconds > 0) && /deadline|timeout/i.test(snapshot.error)}<button disabled={busy} on:click={retryWithoutTimeout}>Retry without call timeout</button>{/if}</div>{/if}
  </section>{/if}
  {#if error}<p class="failure" role="alert">{error}</p>{/if}
  {#if snapshot.status === 'draft'}<div class="plan-ready" role="status"><div><strong>Plan ready · {tasks.length} assignment{tasks.length === 1 ? '' : 's'}</strong><span>Review the assignments below, then run the task graph to start the workers.</span></div><div class="buttons"><button on:click={() => showGraph = true}>Review plan</button><button class="btn primary" disabled={busy || !tasks.length} on:click={runPlan}>Run task graph</button></div></div>{/if}
  <WorkerBoard items={board} selected={selectedTask || (current ? 'tier:' + current.tier : selected)} on:select={event => chooseCard(event.detail)} />
  <div class="console">
    <header class="console-head">
      <div><strong>{currentTask?.id || workerLabel(current?.tier) || 'Assignment'}</strong><p>{current?.cli || (current?.runtime === 'native' ? 'Local / API' : 'No attempt yet')} · {current?.model || 'Backend default'} · {currentTask?.status === 'retrying' ? 'Retry scheduled' : current?.status || currentTask?.status || 'idle'}</p></div>
      <div class="buttons">
        {#if taskAttempts.length > 1}<label class="attempt-picker">Attempt<select value={selected} on:change={event => { selected = event.currentTarget.value; focusActive = false }}>{#each taskAttempts as attempt,index}<option value={attempt.id}>{index + 1} · {attempt.cli || 'Local'} · {attempt.status}</option>{/each}</select></label>{/if}
        {#if current}<span class="muted">{elapsed(current.startedAt, ['running','waiting'].includes(current.status) ? now : new Date(current.updatedAt).getTime())}</span>{/if}
      </div>
    </header>
    <nav class="console-tabs" aria-label="Assignment details">{#each [['activity','Activity'],['output','Result'],['changes','Changes'],['assignment','Assignment']] as [value,label]}<button class:selected={tab === value} on:click={() => tab = value}>{label}</button>{/each}</nav>
    {#if currentTask?.status === 'retrying'}<div class="retry-banner" role="status"><strong>Retry {currentTask.autoRetriesUsed}/{snapshot.maxRetries} scheduled</strong><span>Next attempt at {new Date(currentTask.nextRetryAt).toLocaleTimeString()}. Existing changes will be reused.</span></div>{/if}
    {#if currentTask && !active && ['failed','blocked','cancelled'].includes(currentTask.status)}<div class="recovery"><strong>{currentTask.failurePhase === 'dependencies' ? 'Recover dependency integration' : 'Continue this task without losing changes'}</strong>{#if currentTask.failurePhase === 'dependencies'}<button class="btn primary" disabled={busy} on:click={() => resolveConflicts()}>Resolve conflicts with worker</button>{/if}<button disabled={busy} on:click={continueTask}>{currentTask.failurePhase === 'dependencies' ? 'Continue after manual resolution' : 'Continue task'}</button><button disabled={busy} on:click={startOver}>Start over…</button><button on:click={() => { showGraph = true }}>Route options</button></div>{/if}
    {#if tab === 'activity'}
      <div class="filters" role="group" aria-label="Activity filter">{#each [['all','All'],['io','Input / output'],['reasoning','Reported reasoning'],['delegation','Handoffs'],['errors','Errors']] as [value,label]}<button class:selected={filter === value} on:click={() => filter = value}>{label}</button>{/each}<label><input type="checkbox" bind:checked={follow} on:change={() => { if (follow) scrollLatest() }} /> Follow live</label></div>
      {#if current && ['running','waiting'].includes(current.status)}<div class="waiting"><span class="dot live"></span>{current.status === 'waiting' ? 'Waiting for a delegated assignment.' : current.phase === 'tool' ? 'Tool is running or awaiting approval.' : 'Model call active for ' + elapsed(current.callStartedAt || current.startedAt) + '. Last reported activity: ' + (current.lastProgressAt && new Date(current.lastProgressAt).getTime() > 0 ? elapsed(current.lastProgressAt) + ' ago.' : 'not yet available.')}{#if current.timeoutSeconds} Call timeout: {current.timeoutSeconds}s.{/if}</div>{/if}
      <div class="timeline" bind:this={timeline} role="region" aria-label="Worker activity" on:scroll={() => { if (timeline.scrollHeight - timeline.scrollTop - timeline.clientHeight > 70) follow = false }}>
        {#if events.length > visibleCount || hasMore}<button disabled={historyBusy} on:click={earlier}>{historyBusy ? 'Loading…' : 'Load earlier activity'}</button>{/if}
        {#each events.slice(-visibleCount) as event}
          <article class:error-event={['error','failed','delegated_error'].includes(event.kind)} class:handoff={event.kind === 'delegation'}><header><strong>{workerEventLabel(event)}</strong><time>{new Date(event.timestamp).toLocaleTimeString()}</time></header><pre>{event.text}</pre></article>
        {/each}
        {#if !events.length}<p class="muted">{historyBusy ? 'Loading saved activity…' : filter === 'reasoning' ? 'This backend has not reported reasoning.' : currentTask?.dependencies?.length && currentTask.status !== 'running' ? 'Waiting for dependencies: ' + currentTask.dependencies.join(', ') : 'Activity will appear when this assignment starts.'}</p>{/if}
      </div>
    {:else if tab === 'output'}
      <div class="detail-pane">{#if current?.error}<p class="failure">{current.error}</p>{/if}<pre>{allEvents.filter(e => e.kind === 'result').at(-1)?.text || (currentTask && (!current || current.id === executions.filter(e => e.taskID === currentTask.id).at(-1)?.id) ? currentTask.output : '') || current?.output || 'No result has been reported yet.'}</pre>{#if current?.usage?.calls}<p class="muted">{current.usage.input.toLocaleString()} input · {current.usage.output.toLocaleString()} output tokens · {current.usage.calls} calls</p>{/if}</div>
    {:else if tab === 'changes'}
      <div class="detail-pane"><div class="buttons"><strong>Workspace changes</strong><button disabled={previewBusy || !currentTask?.worktree} on:click={loadPreview}>Refresh</button></div>{#if preview}{#if preview.conflicts?.length}<p role="alert">Conflicted files: {preview.conflicts.join(', ')}. Resolve with the worker, or edit and stage these files before continuing.</p>{/if}<code>{preview.path || currentTask?.worktree?.path || ''}</code><pre class="file-status">{preview.status || 'No tracked file changes'}</pre><pre>{preview.diff || 'No tracked diff. New files are listed above.'}</pre>{#if preview.truncated}<p class="muted">Preview truncated. Inspect the worktree for the complete diff.</p>{/if}{:else}<p class="muted">{previewBusy ? 'Reading changes…' : 'This assignment has no isolated worktree yet.'}</p>{/if}
        {#if currentTask?.result && currentTask.review !== 'merged'}<div class="buttons"><button class="btn primary" disabled={active || busy} title={active ? 'Available when the task graph finishes executing' : ''} on:click={() => action(() => api.reviewWorkerDAGTask(snapshot.id,currentTask.id,'accepted'))}>Accept changes</button><button disabled={active || busy} title={active ? 'Available when the task graph finishes executing' : ''} on:click={() => action(() => api.reviewWorkerDAGTask(snapshot.id,currentTask.id,'rejected'))}>Reject</button></div>{/if}
      </div>
    {:else}
      <div class="detail-pane"><h3>Assignment</h3><pre>{currentTask?.description || current?.assignment || snapshot.task}</pre>{#if currentTask?.dependencies?.length}<p>Depends on: {currentTask.dependencies.join(', ')}</p>{/if}<code>{current?.workspace || currentTask?.worktree?.path || snapshot.workspace}</code>{#if current?.sessionID}<p class="muted">CLI session: {current.sessionID}</p>{/if}{#if current?.parentID}<p class="muted">Delegated by {workerLabel(executions.find(e => e.id === current.parentID)?.tier)}. Results return to that assignment.</p>{/if}</div>
    {/if}
  </div>
  {#if snapshot.dag && tasks.length}<button class="graph-toggle" aria-expanded={showGraph} on:click={() => showGraph = !showGraph}>{showGraph ? 'Hide plan & integration' : 'Plan, routes & integration'}</button>{#if showGraph}<WorkerGraph bind:this={graph} {snapshot} {clis} {models} on:models on:refresh />{/if}{/if}
  {#if snapshot.result}<details class="result"><summary>Run summary</summary><pre>{snapshot.result}</pre></details>{/if}
  {#if !snapshot.dag}<div class="composer" data-voice-composer><VoiceButton disabled={active || busy} context={{page:'workers',worker_id:snapshot.id}} on:transcript={event => { draft = [draft,event.detail.text].filter(Boolean).join(' '); if (event.detail.autoSend) continueRun() }} /><textarea rows="2" bind:value={draft} disabled={active || busy} aria-label="Follow-up worker task" placeholder="Continue with a new instruction…"></textarea><button class="btn primary" disabled={active || busy || !draft.trim()} on:click={continueRun}>Continue</button></div>{/if}
</section>
{#if settings}
  <div class="backdrop"><div class="settings" role="dialog" aria-modal="true" aria-label="Worker run settings" use:focusDialog={{onClose:() => { if (!busy) settings = null }}}>
    <header><h2>Run settings</h2><button disabled={busy} aria-label="Close settings" on:click={() => settings = null}>×</button></header>
    <p>Access and retry settings apply to the next execution. Worker routes update tasks that inherit a profile; recorded attempts keep their original CLI and model.</p>
    <code>{settings.workspace}</code><WorkerProfiles bind:config={settings} {clis} {models} on:models />
    {#if error}<p class="failure" role="alert">{error}</p>{/if}<footer><button disabled={busy} on:click={() => settings = null}>Cancel</button><button class="btn primary" disabled={busy || active} on:click={saveSettings}>{busy ? 'Saving…' : 'Save run profiles'}</button></footer>
  </div></div>
{/if}
<style>
  .primary:hover:not(:disabled){background:var(--accent);border-color:var(--accent);color:var(--accent-fg);filter:brightness(0.92)}
  .progress-summary{display:grid;gap:8px}.progress-summary>div:last-child{display:flex;gap:18px;flex-wrap:wrap;color:var(--text-dim);font-size:12px}.progress-track{height:5px;border-radius:5px;background:var(--bg-raised);overflow:hidden}.progress-track span{display:block;height:100%;background:var(--ok);transition:width .2s}.failure>header,.failed-task{display:flex;gap:16px;align-items:flex-start;justify-content:space-between}.failed-task{padding:14px 0;border-top:1px solid var(--border);margin-top:12px}.failed-task>div{min-width:0}.failed-task p{font-size:12px;line-height:1.5;max-height:96px;overflow:auto}.failed-task button{flex-shrink:0}.access-chip{font-size:11px;color:var(--accent);border:1px solid var(--border);border-radius:20px;padding:4px 9px}.retry-banner{padding:12px 16px;display:flex;gap:12px;align-items:center;flex-wrap:wrap;background:var(--accent-soft);color:var(--text);border-radius:var(--radius);font-size:12px}.retry-banner span{color:var(--text-dim)}

  .monitor{display:grid;gap:16px;min-width:0}.toolbar,.buttons,.run-state,.console-head,.filters{display:flex;align-items:center;gap:12px;flex-wrap:wrap}.toolbar,.console-head{justify-content:space-between}.muted,small,time{color:var(--text-dim)}small,time{font-size:11px}.dot{width:8px;height:8px;border-radius:50%;background:var(--text-dim);display:inline-block;flex-shrink:0}.dot.live{background:var(--ok);box-shadow:0 0 0 4px color-mix(in srgb,var(--ok) 14%,transparent);animation:pulse 2s ease-in-out infinite}.dot.failed{background:var(--err)}button{font:inherit;font-size:12px;cursor:pointer;border:1px solid var(--border-bright);border-radius:var(--radius-sm);background:var(--bg-panel);color:var(--text);padding:8px 12px;transition:background 150ms var(--ease),border-color 150ms var(--ease)}button:hover:not(:disabled){background:var(--bg-raised);border-color:var(--accent)}button:disabled{opacity:.5;cursor:default}.primary,.selected{border-color:var(--accent);background:color-mix(in srgb,var(--accent) 9%,var(--bg-panel))}.primary{background:var(--accent);color:var(--accent-fg)}.stop{color:var(--err)}.failure{border:1px solid color-mix(in srgb,var(--err) 45%,var(--border));border-radius:var(--radius);background:color-mix(in srgb,var(--err) 6%,var(--bg-panel));padding:16px;overflow-wrap:anywhere}.failure p{margin:8px 0;white-space:pre-wrap}.failure .buttons{margin-top:12px}.console{min-width:0;border:1px solid var(--border);border-radius:var(--radius);background:var(--bg-panel);overflow:hidden}.console-head{padding:16px;border-bottom:1px solid var(--border);font-size:13px}.console-head p{font-size:12px;color:var(--text-dim);margin:5px 0 0}summary{cursor:pointer;color:var(--text-dim)}code{overflow-wrap:anywhere;white-space:pre-wrap;line-height:1.5}.filters{padding:10px 14px;border-bottom:1px solid var(--border);gap:6px}.filters button{padding:5px 8px;font-size:11px}.filters label{font-size:11px;margin-left:auto;color:var(--text-dim);display:flex;align-items:center;gap:4px}.timeline{scrollbar-color:var(--border-bright) var(--bg-panel);height:420px;overflow:auto;padding:14px;scrollbar-gutter:stable}.timeline article{margin-bottom:12px;padding:12px;border:1px solid var(--border);border-radius:9px;background:var(--bg)}.timeline article header{display:flex;justify-content:space-between;gap:8px;font-size:11px}.timeline pre,.result pre{white-space:pre-wrap;overflow-wrap:anywhere;font:12px/1.6 var(--font-mono,monospace);margin:10px 0 0}.error-event{border-color:var(--err)!important}.handoff{border-color:var(--accent)!important}.waiting{padding:12px 16px;color:var(--text-dim);font-size:11px;background:var(--bg-raised);line-height:1.6}.waiting .dot{margin-right:8px}.graph-toggle{justify-self:start}.result{padding:14px;border:1px solid var(--border);border-radius:var(--radius)}.composer{display:flex;gap:10px;align-items:flex-end}.composer textarea{flex:1}textarea{font:inherit;min-width:0;background:var(--bg);color:var(--text);border:1px solid var(--border-bright);border-radius:var(--radius-sm);padding:12px;resize:vertical}.backdrop{position:fixed;inset:0;z-index:200;background:var(--overlay);backdrop-filter:blur(8px);display:grid;place-items:center;padding:24px}.settings{width:min(1050px,100%);max-height:90vh;overflow:auto;display:grid;gap:16px;padding:24px;border:1px solid var(--border-bright);border-radius:var(--radius);background:var(--bg);box-shadow:var(--shadow-overlay)}.settings header,.settings footer{display:flex;gap:10px;align-items:center;justify-content:space-between}.settings h2,.settings p{margin:0}.settings p{font-size:13px;color:var(--text-dim);line-height:1.6}.settings footer{justify-content:flex-end}@keyframes pulse{50%{box-shadow:0 0 0 6px color-mix(in srgb,var(--ok) 4%,transparent)}}@media(prefers-reduced-motion:reduce){.dot.live{animation:none}button{transition:none}}@media(max-width:800px){.timeline{height:360px}}
.usage-chip{font-size:11px;color:var(--text-dim);border:1px solid var(--border);border-radius:20px;padding:4px 9px}.console-tabs{display:flex;gap:4px;padding:9px 14px;border-bottom:1px solid var(--border)}.console-tabs button{border-color:transparent;background:transparent}.console-tabs button.selected{border-color:var(--border-bright);background:var(--bg-raised)}.detail-pane{padding:20px;min-height:250px;max-height:550px;overflow:auto}.detail-pane pre{white-space:pre-wrap;overflow-wrap:anywhere;font:12px/1.65 var(--font-mono,monospace)}.file-status{color:var(--text-dim);border-bottom:1px solid var(--border);padding-bottom:12px}.attempt-picker{font-size:11px;display:flex;align-items:center;gap:7px}.attempt-picker select{max-width:260px;background:var(--bg);color:var(--text);border:1px solid var(--border-bright);border-radius:7px;padding:6px;font:inherit}.recovery,.plan-ready,.attention-banner{display:flex;align-items:center;gap:12px;flex-wrap:wrap;padding:12px 16px;background:color-mix(in srgb,var(--warn,#d5a145) 7%,var(--bg-panel));font-size:12px}.recovery{border-bottom:1px solid var(--border)}.recovery strong,.plan-ready>div{margin-right:auto}.plan-ready{background:color-mix(in srgb,var(--accent) 6%,var(--bg-panel));border:1px solid var(--border);border-radius:var(--radius)}.plan-ready>div{display:grid;gap:5px}.plan-ready span,.attention-banner span{color:var(--text-dim);font-size:11px}.console-head{min-height:72px}.timeline{height:380px}

</style>
