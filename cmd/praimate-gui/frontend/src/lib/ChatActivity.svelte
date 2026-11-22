<script>
  import { activityName, activityStatus } from './chatActivity.js'
  export let events = []
  export let collapsible = true
  export let hidden = false
</script>

{#if events.length}
  <!-- The global Hide/Show toggle drives the open state of every block, finished or live. -->
  <svelte:element this={collapsible || hidden ? 'details' : 'div'} open={collapsible || hidden ? !hidden : undefined} class="activity-block" aria-label="Assistant activity">
    {#if hidden}<summary>{events.length} action{events.length === 1 ? '' : 's'} hidden</summary>
    {:else if collapsible}<summary>Activity · {events.length} event{events.length === 1 ? '' : 's'}</summary>{/if}
    <div class="tool-feed">
      {#each events as item}
        <div class="tool-row" class:err={item.ok === false || item.type === 'error'}>
          <span class="tool-status">{activityStatus(item)}</span>
          <span class="tool-name">{activityName(item)}</span>
          <span class="tool-detail mono" class:reasoning-detail={item.type === 'reasoning'}>{item.text || item.detail || ''}</span>
        </div>
      {/each}
    </div>
  </svelte:element>
{/if}

<style>
  .activity-block { margin: 4px 0 8px; color: var(--text-dim); font-size: 12px; }
  summary { cursor: pointer; user-select: none; }
  .tool-feed { margin-top: 8px; border-left: 2px solid var(--border); padding-left: 8px; }
  .tool-row { display: flex; align-items: baseline; gap: 6px; margin: 4px 0; overflow-wrap: anywhere; }
  .tool-status { flex: 0 0 14px; }
  .tool-name { font-weight: 600; flex-shrink: 0; }
  .tool-detail { min-width: 0; white-space: pre-wrap; }
  .reasoning-detail { max-height: 200px; overflow-y: auto; }
  .err { color: var(--err); }
</style>
