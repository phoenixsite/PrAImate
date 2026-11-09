<script>
  import { createEventDispatcher } from 'svelte'
  import { workerColumns, workerStatusLabel } from './workerBoard.js'
  import { workerLabel } from './workerActivity.js'
  export let items = []
  export let selected = ''
  const dispatch = createEventDispatcher()
  let query = ''
  let backend = ''
  $: backends = [...new Set(items.map(item => item.route.cli || 'Local'))]
  $: visible = items.filter(item => (!backend || (item.route.cli || 'Local') === backend) && `${item.title} ${item.description} ${item.route.cli} ${item.route.model}`.toLowerCase().includes(query.toLowerCase()))
</script>

<section class="board" aria-label="Worker task board">
  <div class="board-tools">
    <div><strong>Tasks & agents</strong><span>{visible.length} assignments</span></div>
    <label><span class="sr-only">Search tasks and models</span><input type="search" bind:value={query} placeholder="Search tasks, CLI or model…" /></label>
    <label><span class="sr-only">Filter by CLI</span><select bind:value={backend}><option value="">All backends</option>{#each backends as cli}<option value={cli}>{cli}</option>{/each}</select></label>
  </div>
  <div class="columns">
    {#each workerColumns as column}
      {@const cards = visible.filter(item => item.column === column.id)}
      <section class="column" class:attention={column.id === 'attention'} class:working={column.id === 'working'} class:done={column.id === 'done'} aria-label={column.label}>
        <header><span class="glyph">{column.icon}</span><h3>{column.label}</h3><span class="count">{cards.length}</span></header>
        <div class="cards">
          {#each cards as item (item.id)}
            <button class="agent-card" class:selected={selected === item.id || selected === item.attemptID} on:click={() => dispatch('select', item)}>
              <div class="card-title"><span class="agent-icon">{item.route.cli === 'codex' ? '⌘' : item.route.cli === 'claude' || item.route.cli === 'openclaude' ? '✳' : '◈'}</span><strong>{item.title}</strong><span class="state">{workerStatusLabel(item)}</span></div>
              <div class="route"><span>{item.route.cli || 'Local'}</span><span title={item.route.model}>{item.route.model || 'Backend default'}</span></div>
              <p>{item.preview || item.description || 'Waiting for assignment'}</p>
              {#if item.status === 'retrying'}<span class="retry-note">Retry {item.autoRetriesUsed} · {item.nextRetryAt ? new Date(item.nextRetryAt).toLocaleTimeString() : 'Scheduled'}</span>{:else if item.error}<span class="error-note" title={item.error}>{item.error.slice(0, 140)}</span>{/if}
              <footer><span>{workerLabel(item.tier)}</span><span>{item.attempts > 1 ? `${item.attempts} attempts` : item.review === 'pending' ? item.reviewLabel : item.dependencies.length ? `${item.dependencies.length} dependencies` : item.parentID ? 'Delegated assignment' : 'Independent'}</span></footer>
            </button>
          {/each}
          {#if !cards.length}<p class="empty">{column.id === 'attention' ? 'No action needed' : column.id === 'working' ? 'No active assignments' : column.id === 'done' ? 'Results will appear here' : 'No queued tasks'}</p>{/if}
        </div>
      </section>
    {/each}
  </div>
</section>

<style>
  .board{min-width:0;border:1px solid var(--border);border-radius:var(--radius);background:var(--bg-panel);overflow:hidden}
  .board-tools{display:flex;gap:10px;align-items:center;padding:14px 16px;border-bottom:1px solid var(--border);flex-wrap:wrap}
  .board-tools>div{display:flex;align-items:center;gap:10px;margin-right:auto}.board-tools strong{font-size:13px}.board-tools span{font-size:11px;color:var(--text-dim)}
  input,select{font:inherit;font-size:12px;background:var(--bg);border:1px solid var(--border-bright);color:var(--text);border-radius:8px;padding:7px 10px;max-width:100%}input{width:235px}
  .columns{display:grid;grid-template-columns:repeat(4,minmax(210px,1fr));overflow-x:auto;align-items:start;min-height:165px}
  .column{min-width:0;padding:12px;border-right:1px solid var(--border)}.column:last-child{border-right:0}.column>header{display:flex;gap:8px;align-items:center;margin-bottom:12px;color:var(--text-dim)}h3{font-size:12px;font-weight:600;margin:0}.count{margin-left:auto;font-size:10px;border:1px solid var(--border);border-radius:5px;padding:1px 5px}.glyph{font-size:13px}.attention .glyph{color:var(--warn,#d5a145)}.working .glyph{color:var(--accent)}.done .glyph{color:var(--ok)}
  .cards{scrollbar-color:var(--border-bright) var(--bg-panel);scrollbar-width:thin;display:grid;gap:10px;max-height:340px;overflow-y:auto;scrollbar-gutter:stable}.agent-card{display:grid;gap:10px;width:100%;text-align:left;font:inherit;cursor:pointer;border:1px solid var(--border);background:var(--bg);color:var(--text);padding:12px;border-radius:10px;transition:border-color 150ms,box-shadow 150ms,transform 150ms}.agent-card:hover{border-color:var(--accent);box-shadow:var(--shadow-soft);transform:translateY(-1px)}.agent-card.selected{border-color:var(--accent);box-shadow:inset 0 0 0 1px var(--accent)}.attention .agent-card{background:color-mix(in srgb,var(--warn,#d5a145) 5%,var(--bg))}.done .agent-card{background:color-mix(in srgb,var(--ok) 4%,var(--bg))}.card-title{display:flex;align-items:center;gap:7px;min-width:0}.card-title strong{font-size:12px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.agent-icon{color:var(--text-dim);font-size:16px}.state{margin-left:auto;white-space:nowrap;font-size:10px;color:var(--text-dim)}.retry-note,.error-note{font-size:11px;line-height:1.45;overflow-wrap:anywhere}.retry-note{color:var(--accent)}.error-note{color:var(--err)}.route{display:flex;gap:6px;font-size:10px;color:var(--text-dim);min-width:0}.route span:first-child{border:1px solid var(--border);border-radius:4px;padding:1px 4px}.route span:last-child{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.agent-card p{font-size:12px;line-height:1.55;margin:0;color:var(--text-dim);display:-webkit-box;-webkit-line-clamp:3;-webkit-box-orient:vertical;overflow:hidden;overflow-wrap:anywhere}footer{display:flex;justify-content:space-between;gap:6px;font-size:10px;color:var(--text-dim);border-top:1px solid var(--border);padding-top:9px}.empty{font-size:11px;color:var(--text-dim);text-align:center;padding:18px 5px}.sr-only{position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0}
  @media(prefers-reduced-motion:reduce){.agent-card{transition:none}.agent-card:hover{transform:none}}
</style>
