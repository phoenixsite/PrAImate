<script>
  import { hideChatActivity, setHideChatActivity } from './chatActivityPref.js'
</script>

<button type="button" class="btn sm" class:primary={$hideChatActivity} aria-pressed={$hideChatActivity}
  title="Hide or show the agent's intermediate activity (tool calls, thoughts, status)"
  on:click={() => setHideChatActivity(!$hideChatActivity)}>{$hideChatActivity ? 'Show activity' : 'Hide activity'}</button>
