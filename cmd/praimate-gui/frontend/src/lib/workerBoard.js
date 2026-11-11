import { activeWorkerRun, workerExecutions, workerTaskRoute, workerLabel } from './workerActivity.js'

export const workerColumns = [
  { id: 'attention', label: 'Needs you', icon: '?' },
  { id: 'working', label: 'Working', icon: '◌' },
  { id: 'done', label: 'Done', icon: '✓' },
  { id: 'queued', label: 'Queued', icon: '·' },
]

function column(status) {
  if (['failed', 'blocked', 'cancelled', 'interrupted'].includes(status)) return 'attention'
  if (['running', 'waiting', 'planning', 'retrying'].includes(status)) return 'working'
  if (['completed', 'merged'].includes(status)) return 'done'
  return 'queued'
}

function reviewLabel(review, runActive) {
  if (review !== 'pending') return review === 'merged' ? 'Merged' : review === 'accepted' ? 'Approved' : 'Completed'
  return runActive ? 'Completed · review when execution ends' : 'Ready to review'
}

export function workerBoardItems(run) {
  const attempts = workerExecutions(run)
  const runActive = activeWorkerRun(run)
  const items = (run?.dag?.tasks || []).map(task => {
    const history = attempts.filter(attempt => attempt.taskID === task.id)
    const latest = history.at(-1)
    const statusLabel = task.status === 'completed' ? reviewLabel(task.review, runActive) : ''
    return { id: task.id, taskID: task.id, attemptID: latest?.id || '', title: task.id, description: task.description,
      status: task.status, column: task.status === 'completed' && task.review === 'pending' && !runActive ? 'attention' : column(task.status), route: workerTaskRoute(task, run), tier: task.worker?.profile || 'middle',
      preview: task.description || task.output || latest?.output, error: task.error, autoRetriesUsed: task.autoRetriesUsed || 0, nextRetryAt: task.nextRetryAt, attempts: history.length, review: task.review, reviewLabel: statusLabel, runActive, dependencies: task.dependencies || [] }
  })
  if (!(run?.dag?.tasks || []).length) {
    const tiers = [...new Set(attempts.filter(a => !a.taskID).map(a => a.tier))]
    for (const tier of tiers) {
      const history = attempts.filter(a => !a.taskID && a.tier === tier)
      const attempt = history.at(-1)
      const retrying = run?.nextRetryAt && tier === (run.entryTier || 'primary')
      items.push({ id:'tier:' + tier, taskID:'', attemptID:attempt.id,
        title:run?.dag ? 'Plan the work' : workerLabel(tier), description:attempt.assignment,
        status:retrying ? 'retrying' : attempt.status, column:retrying ? 'working' : column(attempt.status), route:attempt, tier,
        preview:attempt.assignment || attempt.output, error:attempt.error, nextRetryAt:retrying ? run.nextRetryAt : null,
        autoRetriesUsed:run.autoRetriesUsed || 0, parentID:attempt.parentID, attempts:history.length, dependencies:[] })
    }
  }
  return items
}

export function mergeWorkerEvents(history, live) {
  const events = new Map()
  for (const event of [...history, ...live]) {
    const key = event.sequence ? `s:${event.sequence}` : `${event.workerID}:${event.timestamp}:${event.kind}:${event.text}`
    events.set(key, event)
  }
  return [...events.values()].sort((a,b) => a.sequence && b.sequence ? a.sequence - b.sequence : new Date(a.timestamp) - new Date(b.timestamp))
}

export function workerStatusLabel(item) {
  if (item.status === 'retrying') return 'Retry scheduled'
  if (item.status === 'completed') return reviewLabel(item.review, item.runActive)
  return {pending:item.dependencies?.length ? 'Waiting for dependencies' : 'Queued',running:'Working',waiting:'Delegating',planning:'Planning',failed:'Failed',blocked:'Dependency failed',cancelled:'Stopped',interrupted:'Interrupted'}[item.status] || item.status
}
export function workerRunSummary(run) {
  const tasks = run?.dag?.tasks || []
  const failed = tasks.filter(t => t.status === 'failed')
  const blocked = tasks.filter(t => t.status === 'blocked')
  const retrying = tasks.filter(t => t.status === 'retrying')
  const completed = tasks.filter(t => t.status === 'completed')
  const working = tasks.filter(t => t.status === 'running')
  const review = completed.filter(t => t.review === 'pending')
  const label = run?.nextRetryAt || retrying.length && !working.length ? 'Retrying automatically' : ({running:'Working',planning:'Planning',draft:'Plan ready',review:'Ready to review',failed:'Needs attention',cancelled:'Stopped',completed:'Completed',merging:'Integrating changes'}[run?.status] || run?.status)
  return {label,total:tasks.length,failed,blocked,retrying,completed,working,review}
}
