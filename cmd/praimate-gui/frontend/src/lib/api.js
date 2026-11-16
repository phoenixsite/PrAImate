import { systemLinkOpener } from './externalLinks.js'

// Thin wrapper over the Wails-injected window.go.main.App bindings.
// Every call returns a promise; rejections carry the Go error string.
//
// In a plain browser (vite dev without Wails) window.go is absent —
// calls reject with a clear message so pages render their error state
// instead of crashing.

function app() {
  if (typeof window !== 'undefined' && window.go?.main?.App) {
    return window.go.main.App
  }
  return null
}

function call(method, ...args) {
  const a = app()
  if (!a) return Promise.reject(new Error('PrAImate backend not available (running outside Wails?)'))
  return a[method](...args)
}

export const api = {
  assistantConfig: () => call('AssistantConfig'),
  saveAssistantConfig: config => call('SaveAssistantConfig', JSON.stringify(config)),
  assistantSnapshot: () => call('AssistantSnapshot'),
  clearAssistantHistory: () => call('ClearAssistantHistory'),
  sendAssistant: (message, context = {}) => call('SendAssistant', message, JSON.stringify(context)),
  cancelAssistant: () => call('CancelAssistant'),
  assistantHealth: () => call('AssistantHealth'),
  transcribeVoice: (audio, context = {}) => call('TranscribeVoice', audio, JSON.stringify(context)),
  cancelVoice: () => call('CancelVoice'),
  beginVoiceCapture: () => call('BeginVoiceCapture'),
  endVoiceCapture: id => call('EndVoiceCapture', id),
  finishNativeVoiceCapture: id => call('FinishNativeVoiceCapture', id),
	assistantArtifactCatalog: () => call('AssistantArtifactCatalog'),
	installAssistantArtifact: (id) => call('InstallAssistantArtifact', id),
	verifyAssistantArtifact: (id) => call('VerifyAssistantArtifact', id),
	removeAssistantArtifact: (id) => call('RemoveAssistantArtifact', id),
	cancelAssistantArtifactInstall: () => call('CancelAssistantArtifactInstall'),
  usageDashboard: (month = '') => call('UsageDashboard', month),
  workerConfig: () => call('WorkerConfig'),
  saveWorkerConfig: (config) => call('SaveWorkerConfig', JSON.stringify(config)),
  startWorkerRun: (task) => call('StartWorkerRun', task),
  startWorkerRunWithConfig: (task, config) => call('StartWorkerRunWithConfig', task, JSON.stringify(config)),
  workerRuns: () => call('WorkerRuns'),
  workerRunSnapshot: (id) => call('WorkerRunSnapshot', id),
	workerRunActivity: (id, workerID = '', before = 0, limit = 100) => call('WorkerRunActivity', id, workerID, before, limit),
	retryWorkerDAGTask: (id, taskID) => call('RetryWorkerDAGTask', id, taskID),
	resolveWorkerDAGTaskConflicts: (id, taskID) => call('ResolveWorkerDAGTaskConflicts', id, taskID),
	workerTaskPreview: (id, taskID) => call('WorkerTaskPreview', id, taskID),
  renameWorkerRun: (id, title) => call('RenameWorkerRun', id, title),
  deleteWorkerRun: (id) => call('DeleteWorkerRun', id),
  cancelWorkerRun: (id) => call('CancelWorkerRun', id),
  continueWorkerRun: (id, task) => call('ContinueWorkerRun', id, task),
  updateWorkerRunConfig: (id, config) => call('UpdateWorkerRunConfig', id, JSON.stringify(config)),
  retryWorkerPlanning: id => call('RetryWorkerPlanning', id),
  workerRunApprovals: id => call('WorkerRunApprovals', id),
  skillLibraryV2: (request) => call('SkillLibraryV2', JSON.stringify(request)),
  pickSkillSourceV2: (kind) => call('PickSkillSourceV2', kind),
  exportSkillPackageV2: (ref, digest) => call('ExportSkillPackageV2', ref, digest),
  previewAgentSkillsV2: (body) => call('PreviewAgentSkillsV2', body),
  skillsV2RolloutState: () => call('SkillsV2RolloutState'),
  setSkillsV2RolloutState: (enabled) => call('SetSkillsV2RolloutState', !!enabled),
  chatSkillsV2: (id) => call('ChatSkillsV2', id),
  previewChatSkillsV2: (id, body) => call('PreviewChatSkillsV2', id, body),
  setChatSkillsV2: (id, body) => call('SetChatSkillsV2', id, body),
  installedSkillVersionsV2: () => call('InstalledSkillVersionsV2'),
  buildInstalledSkillSelectionV2: (choices) => call('BuildInstalledSkillSelectionV2', JSON.stringify(choices)),
  saveChatSkillChoicesV2: (id, choices) => call('SaveChatSkillChoicesV2', id, JSON.stringify(choices || [])),
  databaseLockStatus: () => call('DatabaseLockStatus'),
  detachedMode: () => call('DetachedMode'),
  detachSession: (kind, sessionID, title) => call('DetachSession', kind, sessionID, title || ''),
  detachedWindows: () => call('DetachedWindows'),
  detachedSessionActive: () => call('DetachedSessionActive'),
  detachedChatConfig: () => call('DetachedChatConfig'),
  detachedRendererReady: () => call('DetachedRendererReady'),
  studioListCLIs: () => call('StudioListCLIs'),
  studioListCLIModels: (cli) => call('StudioListCLIModels', cli),
  studioRefreshCLIModels: (cli) => call('StudioRefreshCLIModels', cli),
  studioLocalLLMModels: () => call('StudioLocalLLMModels'),
  studioMCPServers: () => call('StudioMCPServers'),
  saveStudioConfig: (chatID, name, cli, model, tools, localEndpoint, localModel, mcpServers, workspacePath) =>
    call('SaveStudioConfig', chatID, name || '', cli, model || '', tools || '', localEndpoint || '', localModel || '', mcpServers || [], workspacePath || ''),
  initializeDatabasePassword: (password, confirmation, remember) =>
    call('InitializeDatabasePassword', password, confirmation, !!remember),
  unlockDatabase: (password, remember) =>
    call('UnlockDatabase', password, !!remember),
  changeDatabasePassword: (currentPassword, newPassword, confirmation, remember) =>
    call('ChangeDatabasePassword', currentPassword, newPassword, confirmation, !!remember),
  forgetDatabasePassword: () => call('ForgetDatabasePassword'),
  health: () => call('Health'),
  about: () => call('About'),
  privacyNotice: () => call('PrivacyNotice'),
  acceptPrivacyNotice: () => call('AcceptPrivacyNotice'),
  firstRun: () => call('FirstRun'),
  completeFirstRun: (root, samples, agents, cloneURL) =>
    call('CompleteFirstRun', root, samples, agents, cloneURL || ''),

  listChats: () => call('ListChats'),
  chatMessages: (id) => call('ChatMessages', id),
  deleteChat: (id) => call('DeleteChat', id),
  startChat: (agentID, cli, cwd) => call('StartChat', agentID, cli, cwd),
  startCleanChat: (cli, model, cwd) => call('StartCleanChat', cli, model, cwd),
  sendChat: (chatID, message) => call('SendChat', chatID, message),
  sendChatWithAttachments: (chatID, message, paths) =>
    call('SendChatWithAttachments', chatID, message, paths),
  sendChatStream: (chatID, message, paths) =>
    call('SendChatStream', chatID, message, paths || []),
  cancelChatTurn: (chatID) => call('CancelChatTurn', chatID),
  resolveApproval: (id, allow, always) => call('ResolveApproval', id, allow, always),
  runChatCommand: (chatID, command) => call('RunChatCommand', chatID, command),
  setChatTools: (chatID, tools) => call('SetChatTools', chatID, tools),
  pickChatAttachments: (chatID) => call('PickChatAttachments', chatID),
  attachmentDataURL: (path) => call('AttachmentDataURL', path),

  listCLIs: () => call('ListCLIs'),
  listCLIModels: (cli) => call('ListCLIModels', cli),
  refreshCLIModels: (cli) => call('RefreshCLIModels', cli),
  executionCapabilities: (cli) => call('ExecutionCapabilities', cli),
  preflightExecution: (agentID, surface, cli, model, tools, cwd, localEndpoint, localModel) =>
    call('PreflightExecution', agentID || '', surface, cli, model || '', tools || '', cwd || '', localEndpoint || '', localModel || ''),
  listWorkspaceChats: () => call('ListWorkspaceChats'),
  openWorkspaceChat: (id) => call('OpenWorkspaceChat', id),

  listAgents: () => call('ListAgents'),
  importAgentDialog: () => call('ImportAgentDialog'),
  reviewAgentImportDialog: () => call('ReviewAgentImportDialog'),
  importReviewedAgentPack: (path, reviewDigest) => call('ImportReviewedAgentPack', path, reviewDigest),
  exportAgentDialog: (id) => call('ExportAgentDialog', id),
  deleteAgent: (id) => call('DeleteAgent', id),
  agentYAML: (id) => call('AgentYAML', id),
  saveAgentYAML: (yaml) => call('SaveAgentYAML', yaml),
  newAgentTemplateYAML: () => call('NewAgentTemplateYAML'),
  previewGuidedAgent: (request) => call('PreviewGuidedAgent', request),
  createGuidedAgent: (request) => call('CreateGuidedAgent', request),
  agentRuntimeJSON: (id) => call('AgentRuntimeJSON', id),
  agentRuntimeConfig: (id) => call('AgentRuntimeConfig', id),
  enableAgentRuntime: (id) => call('EnableAgentRuntime', id),
  saveAgentRuntimeJSON: (id, body) => call('SaveAgentRuntimeJSON', id, body),
  listManagedRuns: (agentID) => call('ListManagedRuns', agentID || ''),
  managedRunDetails: (runID) => call('ManagedRunDetails', runID),
  managedArtifactText: (runID, name) => call('ManagedArtifactText', runID, name),
  resumeManagedRun: (runID) => call('ResumeManagedRun', runID),
  cancelManagedRun: (runID) => call('CancelManagedRun', runID),
  planWorkerDAG: (task, config, parallel) => call('PlanWorkerDAG', task, JSON.stringify(config), parallel),
  saveWorkerDAG: (id, tasks, parallel) => call('SaveWorkerDAG', id, JSON.stringify(tasks), parallel),
  executeWorkerDAG: (id) => call('ExecuteWorkerDAG', id),
  reviewWorkerDAGTask: (id, taskID, decision) => call('ReviewWorkerDAGTask', id, taskID, decision),
  mergeWorkerDAG: (id) => call('MergeWorkerDAG', id),
  resetWorkerDAGTask: (id, taskID) => call('ResetWorkerDAGTask', id, taskID),
  useWorkerDAGTaskProfile: (id, taskID) => call('UseWorkerDAGTaskProfile', id, taskID),
  cleanupWorkerDAG: (id) => call('CleanupWorkerDAG', id),
  getAgentKnowledge: (id) => call('GetAgentKnowledge', id),
  saveAgentKnowledgeConfig: (id, config, key, removeKey) => call('SaveAgentKnowledgeConfig', id, JSON.stringify(config), key || '', !!removeKey),
  testAgentKnowledgeRemote: (id) => call('TestAgentKnowledgeRemote', id),
  setAgentKnowledgeMode: (id, mode) => call('SetAgentKnowledgeMode', id, mode),
  enableAgentKnowledge: (id) => call('EnableAgentKnowledge', id),
  pickAgentKnowledgeFiles: (id) => call('PickAgentKnowledgeFiles', id),
  pickAgentKnowledgeFolder: (id) => call('PickAgentKnowledgeFolder', id),
  deleteAgentKnowledgeFile: (id, rel) => call('DeleteAgentKnowledgeFile', id, rel),
  pickAgentRequirementsScript: (id, os, instructions) => call('PickAgentRequirementsScript', id, os, instructions || ''),
  runAgentRequirements: (id) => call('RunAgentRequirements', id),
  cancelAgentRequirements: (id) => call('CancelAgentRequirements', id),
  buildAgentRAG: (id, backend, apiKey, model) => call('BuildAgentRAG', id, backend || '', apiKey || '', model || ''),
  cancelAgentRAG: (id) => call('CancelAgentRAG', id),
  installBundledGraphify: () => call('InstallBundledGraphify'),
  exportAgentPackDialog: (id) => call('ExportAgentPackDialog', id),

  updateChatConfig: (chatID, cli, model, tools, localEndpoint, localApiKey, localModel) =>
    call('UpdateChatConfig', chatID, cli, model, tools, localEndpoint || '', localApiKey || '', localModel || ''),
  setNativeChatLimits: (chatID, contextTokens, outputTokens) => call('SetNativeChatLimits', chatID, Number(contextTokens) || 0, Number(outputTokens) || 0),
  nativeChatContext: (chatID) => call('NativeChatContext', chatID),
  updateChatWorkspace: (chatID, workspacePath) => call('UpdateChatWorkspace', chatID, workspacePath || ''),
  renameChat: (chatID, newTitle) => call('RenameChat', chatID, newTitle),
  searchChats: (q) => call('SearchChats', q),

  listCLIBackends: () => call('ListCLIBackends'),
  listInstallMethods: (cli) => call('ListInstallMethods', cli),
  installCLI: (cli, methodID) => call('InstallCLI', cli, methodID),
  listManagedTools: () => call('ListManagedTools'),
  listToolInstallMethods: (tool) => call('ListToolInstallMethods', tool),
  installManagedTool: (tool, methodID) => call('InstallManagedTool', tool, methodID),
  buildRequirements: (tool) => call('BuildRequirements', tool),
  buildToolFromSource: (tool) => call('BuildToolFromSource', tool),
  checkUpdate: () => call('CheckUpdate'),
  performUpdate: () => call('PerformUpdate'),

  getLocalLLM: () => call('GetLocalLLM'),
  setLocalLLM: (d) => call('SetLocalLLM', d),
  testLocalLLM: (endpoint, apiKey) => call('TestLocalLLM', endpoint, apiKey),
  testLocalHost: (hostId, endpoint, apiKey) => call('TestLocalHost', hostId, endpoint, apiKey),
  inspectLocalHostCertificate: endpoint => call('InspectLocalHostCertificate', endpoint),
  localHostCertificateTrusted: endpoint => call('LocalHostCertificateTrusted', endpoint),
  trustLocalHostCertificate: (endpoint, certificate) => call('TrustLocalHostCertificate', endpoint, certificate),
  removeLocalHostCertificate: endpoint => call('RemoveLocalHostCertificate', endpoint),
  listLocalHosts: () => call('ListLocalHosts'),
  saveLocalHost: (h) => call('SaveLocalHost', h),
  deleteLocalHost: (id) => call('DeleteLocalHost', id),
  setDefaultLocalHost: (id) => call('SetDefaultLocalHost', id),
  localLLMHostsModels: () => call('LocalLLMHostsModels'),
  applyModelsToCLI: (cli, hostId, models) => call('ApplyModelsToCLI', cli, hostId || '', models || []),
  removeModelFromCLI: (cli, hostId, model) => call('RemoveModelFromCLI', cli, hostId || '', model || ''),
  listAppliedCLIModels: () => call('ListAppliedCLIModels'),
  removeAppliedModelFromCLI: (cli, hostId, providerKey, model) => call('RemoveAppliedModelFromCLI', cli, hostId, providerKey, model),

  editorMode: () => call('EditorMode'),
  editorListFiles: () => call('EditorListFiles'),
  editorReadFile: (rel) => call('EditorReadFile', rel),
  editorWriteFile: (rel, content) => call('EditorWriteFile', rel, content),
  editorCreateFile: (rel) => call('EditorCreateFile', rel),
  editorRenameFile: (src, dst) => call('EditorRenameFile', src, dst),
  editorDeleteFile: (rel) => call('EditorDeleteFile', rel),
  agentRenameKnowledgeFile: (id, src, dst) => call('AgentRenameKnowledgeFile', id, src, dst),
  refreshPATH: () => call('RefreshPATH'),
  skillsList: () => call('SkillsList'),
  skillsForCLI: (cli) => call('SkillsForCLI', cli),
  skillsUserList: () => call('SkillsUserList'),
  addUserSkill: (input) => call('AddUserSkill', input),
  deleteUserSkill: (id) => call('DeleteUserSkill', id),
  importSkillFromURL: (url) => call('ImportSkillFromURL', url),
  importSkillFromZipFile: (path) => call('ImportSkillFromZipFile', path),
  pickSkillZipFile: () => call('PickSkillZipFile'),
  skillsDefaults: () => call('SkillsDefaults'),
  setSkillsDefaults: (ids) => call('SetSkillsDefaults', ids || []),
  chatSkills: (chatID) => call('ChatSkills', chatID),
  setChatSkills: (chatID, ids) => call('SetChatSkills', chatID, ids || []),
  setChatMCPServers: (chatID, ids) => call('SetChatMCPServers', chatID, ids || []),
  testMCPServer: (id) => call('TestMCPServer', id),
  importMCPServersJSON: (jsonText) => call('ImportMCPServersJSON', jsonText),
  openSkillInStudio: (ref, digest) => call('OpenSkillInStudio', ref, digest),
  createSkillInStudio: (name) => call('CreateSkillInStudio', name),
  activeChatIDs: () => call('ActiveChatIDs'),
  openEditorFolder: () => call('OpenEditorFolder'),
  openExternalURL: url => {
    const open = systemLinkOpener()
    return open ? Promise.resolve().then(() => open(url)) : Promise.reject(new Error('No local browser opener is available'))
  },
  openAgentKnowledgeFolder: (id) => call('OpenAgentKnowledgeFolder', id),
  openEditorWindow: (folder, agentID, cli, model, chatID, localEndpoint, localApiKey, localModel) =>
    call('OpenEditorWindow', folder, agentID, cli, model || '', chatID, localEndpoint || '', localApiKey || '', localModel || ''),
  prepareStudioChat: (folder, agentID, cli, model, localEndpoint, localModel, choices) =>
    call('PrepareStudioChat', folder, agentID || '', cli, model || '', localEndpoint || '', localModel || '', choices === null ? '' : JSON.stringify(choices)),
  startTerminal: (agentID, cli, model, cwd, localEndpoint, localApiKey, localModel, resume = false, skills = []) =>
    call('StartTerminal', agentID, cli, model || '', cwd, localEndpoint || '', localApiKey || '', localModel || '', !!resume, skills || []),
  localLLMModels: () => call('LocalLLMModels'),
  recordCodeSession: (agentID, cli, model, cwd, localEndpoint, localApiKey, localModel) =>
    call('RecordCodeSession', agentID || '', cli, model || '', cwd, localEndpoint || '', localApiKey || '', localModel || ''),
  startCodeSessionWithSkills: (agentID, cli, model, cwd, localEndpoint, localModel, choices) =>
    call('StartCodeSessionWithSkills', agentID || '', cli, model || '', cwd, localEndpoint || '', localModel || '', choices === null ? '' : JSON.stringify(choices)),
  startTerminalForChat: (chatID, resume = false) => call('StartTerminalForChat', chatID, !!resume),
  bindChatToTerminal: (termID, chatID) => call('BindChatToTerminal', termID, chatID),
  listTerminalSessions: () => call('ListTerminalSessions'),
  startAgentHelperChat: (cli, model, cwd, agentID) => call('StartAgentHelperChat', cli, model || '', cwd || '', agentID || ''),
  syncAgentYAMLToDisk: (id, cwd) => call('SyncAgentYAMLToDisk', id, cwd || ''),
  readAgentYAMLFromDisk: (id, cwd) => call('ReadAgentYAMLFromDisk', id, cwd || ''),
  writeAgentYAMLDraftToDisk: (id, cwd, body) => call('WriteAgentYAMLDraftToDisk', id, cwd || '', body),
  createAgentFromName: (name) => call('CreateAgentFromName', name),
  agentKnowledgeTree: (id) => call('AgentKnowledgeTree', id),
  agentReadKnowledgeFile: (id, rel) => call('AgentReadKnowledgeFile', id, rel),
  agentWriteKnowledgeFile: (id, rel, content) => call('AgentWriteKnowledgeFile', id, rel, content),
  agentCreateKnowledgeFile: (id, rel) => call('AgentCreateKnowledgeFile', id, rel),
  localCLIStatus: () => call('LocalCLIStatusNow'),
  applyLocalToCLI: (cli, model) => call('ApplyLocalToCLI', cli, model || ''),
  disableLocalForCLI: (cli) => call('DisableLocalForCLI', cli),

  pickFolder: () => call('PickFolder'),
  runWorkflow: (agentID, workflow, cli, model, cwd, inputs, localEndpoint, localApiKey, localModel) =>
    call('RunWorkflow', agentID, workflow, cli, model || '', cwd, inputs || {}, localEndpoint || '', localApiKey || '', localModel || ''),
  runAllWorkflows: (agentID, cli, model, cwd, inputsByWorkflow, localEndpoint, localApiKey, localModel) =>
    call('RunAllWorkflows', agentID, cli, model || '', cwd, inputsByWorkflow || {}, localEndpoint || '', localApiKey || '', localModel || ''),
  runWorkflowTracked: (runID, agentID, workflow, cli, model, cwd, inputs, endpoint, localModel, tools) =>
    call('RunWorkflowTracked', runID, agentID, workflow, cli, model || '', cwd, inputs || {}, endpoint || '', localModel || '', tools || ''),
  runAllWorkflowsTracked: (runID, agentID, cli, model, cwd, inputs, endpoint, localModel, tools) =>
    call('RunAllWorkflowsTracked', runID, agentID, cli, model || '', cwd, inputs || {}, endpoint || '', localModel || '', tools || ''),
  cancelWorkflowRun: (runID) => call('CancelWorkflowRun', runID),
  privacyPreview: (text) => call('PrivacyPreview', text),
  storedDataInfo: () => call('StoredDataInfo'),
  deleteAllStoredData: (projectsRoot, phrase) =>
    call('DeleteAllStoredData', projectsRoot || '', phrase || ''),

  mcpCatalogue: () => call('MCPCatalogue'),
  mcpServers: () => call('MCPServers'),
  addCustomMCP: (name, transport, command, url, envText, headersText) =>
    headersText === undefined ? call('AddCustomMCP', name, transport, command, url, envText) : call('AddCustomMCPWithHeaders', name, transport, command, url, envText, headersText),
  updateMCPServer: (id, name, transport, command, url, envText, headersText) =>
    headersText === undefined ? call('UpdateMCPServer', id, name, transport, command, url, envText) : call('UpdateMCPServerWithHeaders', id, name, transport, command, url, envText, headersText),
  setMCPEnabled: (id, on) => call('SetMCPEnabled', id, on),
  deleteMCPServer: (id) => call('DeleteMCPServer', id),

  listWatchers: () => call('ListWatchers'),
  addWatcher: (agentID, path, workflow, patterns) =>
    call('AddWatcher', agentID, path, workflow, patterns),
  setWatcherEnabled: (id, on) => call('SetWatcherEnabled', id, on),
  deleteWatcher: (id) => call('DeleteWatcher', id),

  listSchedules: () => call('ListSchedules'),
  addCronSchedule: (agentID, cron, workflow) => call('AddCronSchedule', agentID, cron, workflow),
  setScheduleEnabled: (id, on) => call('SetScheduleEnabled', id, on),
  deleteSchedule: (id) => call('DeleteSchedule', id),

  listPrivacyPatterns: () => call('ListPrivacyPatterns'),
  addPrivacyPattern: (p) => call('AddPrivacyPattern', p),
  deletePrivacyPattern: (i) => call('DeletePrivacyPattern', i),

  getGUISetting: (k) => call('GetGUISetting', k),
  setGUISetting: (k, v) => call('SetGUISetting', k, v),

  praimateCodeInstalled: () => call('PraimateCodeInstalled'),
  installPraimateCode: () => call('InstallPraimateCode'),

  backupStatus: () => call('BackupStatus'),

  restoreBackupState: (password) => call('RestoreBackupState', password || ''),
  setBackupEnabled: (on) => call('SetBackupEnabled', on),
  configureBackup: (mode, remoteURL) => call('ConfigureBackup', mode, remoteURL || ''),
  setBackupRemote: (url) => call('SetBackupRemote', url),
  testBackupRemote: (url) => call('TestBackupRemote', url),
  backupSyncNow: () => call('BackupSyncNow'),
  resolveBackupDivergence: (strategy) => call('ResolveBackupDivergence', strategy),
  backupForcePush: () => call('BackupForcePush'),
  backupResetFromRemote: () => call('BackupResetFromRemote'),
  backupDisconnect: () => call('BackupDisconnect'),
  setBackupAutoSync: (on) => call('SetBackupAutoSync', on),
  setBackupForceLocal: (on) => call('SetBackupForceLocal', on),
  studioGetStatus: () => call('StudioGetStatus'),
  studioInstall: () => call('StudioInstall'),
  studioUpdate: () => call('StudioUpdate'),
  studioRepair: () => call('StudioRepair'),
  studioOpenProject: (workspacePath, cli, model, agentID, tools) =>
    call('StudioOpenProject', workspacePath || '', cli || '', model || '', agentID || '', tools || ''),
  studioListRecentProjects: () => call('StudioListRecentProjects'),
}

// onTurn subscribes to streamed workflow turns. Returns an unsubscribe
// function. No-op outside Wails.
export function onTurn(handler) {
  if (typeof window !== 'undefined' && window.runtime?.EventsOn) {
    return window.runtime.EventsOn('praimate:turn', handler)
  }
  return () => {}
}

// onWorkflowStream subscribes to live workflow events (state, text deltas
// and tool activity). Returns an unsubscribe function. No-op outside Wails.
export function onWorkflowStream(handler) {
  if (typeof window !== 'undefined' && window.runtime?.EventsOn) {
    return window.runtime.EventsOn('praimate:workflow-stream', handler)
  }
  return () => {}
}

// onChatStream subscribes to live chat turn events (text deltas, tool
// activity) emitted while SendChatStream runs. Returns an unsubscribe
// function. No-op outside Wails.
export function onChatStream(handler) {
  if (typeof window !== 'undefined' && window.runtime?.EventsOn) {
    return window.runtime.EventsOn('praimate:chat-stream', handler)
  }
  return () => {}
}

// onApproval subscribes to mid-turn permission requests ("ask" Tools
// level). Answer with api.resolveApproval(id, allow, always). Returns
// an unsubscribe function. No-op outside Wails.
export function onApproval(handler) {
  if (typeof window !== 'undefined' && window.runtime?.EventsOn) {
    return window.runtime.EventsOn('praimate:approval', handler)
  }
  return () => {}
}

// onRequirementsProgress subscribes to lifecycle and live-output events from
// an explicitly started agent requirements script.
export function onRequirementsProgress(handler) {
  if (typeof window !== 'undefined' && window.runtime?.EventsOn) {
    return window.runtime.EventsOn('praimate:requirements', handler)
  }
  return () => {}
}
