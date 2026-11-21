// Studio's overview must not wait for CLI probes. Keep resource errors and
// pending operations separate so a slow catalogue cannot hide recent projects.
export function createStudioSession(api, changed = () => {}) {
  let state = {
    status: null, projects: [], clis: [], agents: [], models: [],
    loadingStatus: true, loadingProjects: true, loadingChoices: false, loadingModels: false,
    statusError: '', projectsError: '', choicesError: '', modelError: '', error: '',
    operation: '', openingPath: '',
  }
  let disposed = false, statusSeq = 0, modelSeq = 0
  let overviewFlight, choicesFlight, choicesLoaded = false
  const patch = next => {
    if (disposed) return
    state = { ...state, ...next }
    changed(state)
  }

  function refresh() {
    if (disposed || state.operation || state.openingPath) return Promise.resolve()
    if (overviewFlight) return overviewFlight
    const seq = ++statusSeq
    patch({ loadingStatus: true, loadingProjects: true, statusError: '', projectsError: '' })
    overviewFlight = Promise.all([
      api.studioGetStatus().then(status => {
        if (seq === statusSeq) patch({ status, loadingStatus: false })
      }).catch(error => {
        if (seq === statusSeq) patch({ statusError: String(error), loadingStatus: false })
      }),
      api.studioListRecentProjects().then(projects => patch({ projects: normalizeRecentProjects(projects), loadingProjects: false }))
        .catch(error => patch({ projectsError: String(error), loadingProjects: false })),
    ]).finally(() => { overviewFlight = null })
    return overviewFlight
  }

  function loadChoices(force = false) {
    if (disposed) return Promise.resolve()
    if (choicesFlight) return choicesFlight
    if (choicesLoaded && !force) return Promise.resolve()
    patch({ loadingChoices: true, choicesError: '' })
    choicesFlight = Promise.allSettled([api.listCLIs(), api.listAgents()])
      .then(([clis, agents]) => {
        choicesLoaded = clis.status === 'fulfilled' && agents.status === 'fulfilled'
        patch({
          ...(clis.status === 'fulfilled' ? { clis: clis.value || [] } : {}),
          ...(agents.status === 'fulfilled' ? { agents: agents.value || [] } : {}),
          choicesError: [clis, agents].filter(result => result.status === 'rejected').map(result => String(result.reason)).join('\n'),
        })
      })
      .finally(() => { choicesFlight = null; patch({ loadingChoices: false }) })
    return choicesFlight
  }

  async function loadModels(cli, force = false) {
    if (disposed) return
    const seq = ++modelSeq
    patch({ models: [], loadingModels: true, modelError: '' })
    try {
      const models = await (force ? api.refreshCLIModels(cli) : api.listCLIModels(cli))
      if (seq === modelSeq) patch({ models: models || [] })
    } catch (error) {
      if (seq === modelSeq) patch({ modelError: String(error) })
    } finally {
      if (seq === modelSeq) patch({ loadingModels: false })
    }
  }

  async function maintain(operation) {
    if (disposed || state.operation || state.openingPath) return false
    const action = { install: api.studioInstall, update: api.studioUpdate, repair: api.studioRepair }[operation]
    if (!action) return false
    ++statusSeq
    patch({ operation, error: '' })
    try {
      const status = await action()
      patch({ status, statusError: '', loadingStatus: false })
      if (!status || status.state !== 'installed') {
        throw new Error('Studio is not ready. Check the editor environment and try again.')
      }
      return !disposed
    } catch (error) {
      patch({ error: String(error) })
      return false
    } finally { patch({ operation: '', loadingStatus: false }) }
  }

  async function launch({ workspacePath, cli, model, agentID, tools }) {
    if (disposed || state.operation || state.openingPath) return false
    if (!workspacePath?.trim()) { patch({ error: 'Choose a project folder.' }); return false }
    if (!state.status || state.statusError || !['installed', 'update_available'].includes(state.status.state)) {
      patch({ error: 'Studio is not ready. Check the editor environment and try again.' })
      return false
    }
    ++statusSeq
    patch({ openingPath: workspacePath, error: '' })
    try {
      await api.studioOpenProject(workspacePath, cli, model, agentID, tools)
      // Refreshing history can fail after a successful launch; report that
      // separately rather than suggesting that the user launch a second editor.
      if (!disposed) {
        try { patch({ status: await api.studioGetStatus(), statusError: '', loadingStatus: false }) }
        catch (error) { patch({ statusError: String(error), loadingStatus: false }) }
        try { patch({ projects: normalizeRecentProjects(await api.studioListRecentProjects()), projectsError: '' }) }
        catch (error) { patch({ projectsError: String(error) }) }
      }
      return !disposed
    } catch (error) {
      patch({ error: String(error) })
      return false
    } finally { patch({ openingPath: '' }) }
  }

  return { get state() { return state }, refresh, loadChoices, loadModels, maintain, launch,
    dispose() { disposed = true; ++statusSeq; ++modelSeq },
  }
}

// Older backends return plain paths. Those entries are unconfigured, so the
// UI asks for a configuration instead of guessing one.
export function normalizeRecentProjects(projects) {
  return (projects || []).map(project => typeof project === 'string'
    ? { path: project, cli: '', model: '', agentID: '', tools: '', configured: false }
    : { path: project?.path || '', cli: project?.cli || '', model: project?.model || '', agentID: project?.agentID || '', tools: project?.tools || '', configured: !!project?.configured })
    .filter(project => project.path)
}

export function studioProjectName(path) {
  return path.replace(/[\\/]+$/, '').split(/[\\/]/).pop() || path
}
