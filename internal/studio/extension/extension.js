'use strict';
const vscode = require('vscode');
const net = require('node:net');
const path = require('node:path');
const fs = require('node:fs');
const { spawn } = require('node:child_process');
const { RPCClient } = require('./rpc');
const { terminalOptions } = require('./terminal');
const terminalUsage = new Map();

let rpc, transport, backend, context, output, statusBar, provider;
let reconnectTimer, stopped = false, generation = 0, launchInfo = {}, connectionFile, stopWatching;
let currentStatus = { connected: false, activeCLI: 'praimate-code', activeModel: '', activeAgent: '', activeTools: 'safe', activeWorkspace: '', availableCLIs: [], agents: [], models: [], desktopWindow:{available:false,hidden:false} };
const workspace = () => currentStatus.activeWorkspace || vscode.workspace.workspaceFolders?.[0]?.uri.fsPath || '';
const call = (method, params) => rpc ? rpc.request(method, params) : Promise.reject(new Error('PrAImate Core is offline'));
const report = err => vscode.window.showErrorMessage('PrAImate: ' + err.message);

function disposeConnection() {
  clearTimeout(reconnectTimer);
  if (rpc) { const old = rpc; rpc = null; old.close(); }
  if (transport) { transport.destroy(); transport = null; }
  if (backend) { backend.kill(); backend = null; }
}
function updateStatus() {
  statusBar.text = currentStatus.connected ? '$(pass-filled) PrAImate' : '$(circle-slash) PrAImate: Offline';
  provider?.notifyStatus();
}
function refresh() {
  provider?.post({type:'refresh'});
}
async function refreshModels(force = false) {
  const cli = currentStatus.activeCLI;
  const models = await call(force ? 'models.refresh' : 'models.list', { cli });
  if (cli === currentStatus.activeCLI) { currentStatus.models = models || []; provider.notifyStatus(); }
}
function sessionStatus(st) {
  Object.assign(currentStatus, {
    activeCLI: st.cli, activeModel: st.model || '', activeAgent: st.agentId || '',
    activeTools: st.tools || 'safe', skills: st.skills, mcpServers: st.mcpServers,
    localEndpoint: st.localEndpoint || '', localModel: st.localModel || '',
    localContextTokens: st.localContextTokens || 0, localOutputTokens: st.localOutputTokens || 0,
    activeWorkspace: st.workspace || currentStatus.activeWorkspace || workspace()
  });
}
async function configure(config) {
  if (provider.busy) throw new Error('Stop the active run before changing configuration');
  const previousAgent = currentStatus.activeAgent;
  const previousSkills = JSON.stringify(currentStatus.skills ?? null);
  const previousWorkspace = currentStatus.activeWorkspace;
  const st = await call('session.update', config);
  sessionStatus(st);
  // Agent instructions belong to the persisted chat and its native session.
  if (previousAgent !== currentStatus.activeAgent || previousSkills !== JSON.stringify(currentStatus.skills ?? null) || previousWorkspace !== currentStatus.activeWorkspace) provider.newChat();
  await context.workspaceState.update('session', st);
  provider.notifyStatus();
  refresh();
  refreshModels().catch(err => output.appendLine(err.message));
}
function readLaunchInfo() {
  try { return JSON.parse(fs.readFileSync(connectionFile, 'utf8')); }
  catch (err) {
    if (err.code !== 'ENOENT') throw new Error('Cannot read Studio connection descriptor. Reopen the project from Desktop.');
    return {endpoint:process.env.PRAIMATE_SOCK || '', token:process.env.PRAIMATE_STUDIO_TOKEN || '',
      backend:process.env.PRAIMATE_BIN, config:JSON.parse(process.env.PRAIMATE_STUDIO_CONFIG || 'null')};
  }
}
function connect() {
  const ownGeneration = ++generation;
  disposeConnection();
  if (stopped) return;
  currentStatus.connected = false;
  currentStatus.error = '';
  currentStatus.connecting = true;
  if (!vscode.workspace.isTrusted) {
    currentStatus.connecting = false;
    currentStatus.error = 'Trust this workspace to connect to PrAImate.';
    updateStatus(); return;
  }
  updateStatus();
  try { launchInfo = readLaunchInfo(); }
  catch (err) { currentStatus.connecting = false; currentStatus.error = err.message; updateStatus(); return; }
  const descriptor = launchInfo, endpoint = descriptor.endpoint || '';
  function disconnected(err) {
    if (stopped || ownGeneration !== generation) return;
    currentStatus.connected = false;
    currentStatus.connecting = false;
    currentStatus.error = err.message + (endpoint ? ' Reopen the project from PrAImate Desktop if it was restarted.' : ' Check PrAImate Output and the backend installation.');
    updateStatus();
    output.appendLine(err.message);
    clearTimeout(reconnectTimer);
    reconnectTimer = setTimeout(connect, 3000);
  }
  async function ready(input, writable) {
    if (ownGeneration !== generation) return;
    const client = new RPCClient(input, writable, incoming, disconnected);
    rpc = client;
    const request = (method, params) => client.request(method, params);
    try {
      const init = await request('system.initialize', { client:'praimate-studio', protocolVersion:'1', workspace:workspace(), token:descriptor.token || '' });
      if (ownGeneration !== generation) return;
      let config = context.workspaceState.get('session', {});
      const savedSession = !!config.cli;
      let explicitLaunch = false;
      if (descriptor.config?.workspace === workspace() && context.workspaceState.get('launchId') !== (descriptor.launchId || 'environment')) {
        config = descriptor.config;
        explicitLaunch = true;
      }
      try {
        const st = await request('session.update', { ...config, workspace:workspace() });
        if (ownGeneration !== generation) return;
        sessionStatus(st);
        await context.workspaceState.update('session', st);
        await context.workspaceState.update('launchId', descriptor.launchId || 'environment');
      } catch (err) {
        throw new Error('Saved session configuration could not be restored: '+err.message);
      }
      currentStatus.version = init.serverVersion;
      const agents = await request('agents.list');
      if (ownGeneration !== generation) return;
      currentStatus.agents = agents || [];
      currentStatus.connected = true;
      currentStatus.connecting = false;
      currentStatus.error = '';
      updateStatus(); refresh();
      request('desktop.window.status').then(state => { if (ownGeneration === generation) { currentStatus.desktopWindow = state; provider.notifyStatus(); } }).catch(err => output.appendLine(err.message));
      // CLI/model probes must not block connection or transcript recovery.
      request('clis.list').then(list => { if (ownGeneration === generation) { currentStatus.availableCLIs = list || []; provider.notifyStatus(); } }).catch(err => output.appendLine(err.message));
      refreshModels().catch(err => output.appendLine(err.message));
      const chatID = context.workspaceState.get('chatId');
      if (explicitLaunch && chatID) provider.newChat();
      else if (chatID) {
        try {
          if (savedSession) {
            // Restore the transcript without replacing unsent configuration
            // changes with the older settings persisted at the last send.
            await request('chats.get',{id:chatID});
            provider.chatId = chatID;
            await provider.loadMessages();
          } else await provider.resumeChat(chatID);
        }
        catch (err) { report(err); provider.newChat(); }
      }
    } catch (err) {
      if (ownGeneration !== generation) return;
      disposeConnection();
      disconnected(err);
    }
  }
  if (endpoint) {
    let target = endpoint;
    if (endpoint.startsWith('tcp://')) {
      const match = /^tcp:\/\/127\.0\.0\.1:(\d+)$/.exec(endpoint);
      if (!match) { disconnected(new Error('Studio endpoint must be loopback')); return; }
      target = { host:'127.0.0.1', port:Number(match[1]) };
    }
    transport = net.createConnection(target);
    const socket = transport;
    socket.setTimeout(6000, () => socket.destroy(new Error('Studio connection timed out')));
    socket.once('connect', () => { socket.setTimeout(0); ready(socket, socket); });
    transport.on('error', disconnected);
  } else {
    backend = spawn(descriptor.backend || (process.platform === 'win32' ? 'praimate.exe' : 'praimate'), ['serve','--stdio'], {
      cwd:workspace() || undefined, shell:false, windowsHide:true, stdio:['pipe','pipe','pipe']
    });
    backend.stderr.setEncoding('utf8');
    backend.stderr.on('data', text => output.append(text));
    backend.on('error', disconnected);
    backend.on('exit', () => disconnected(new Error('Backend exited; see PrAImate output for storage/unlock errors')));
    ready(backend.stdout, backend.stdin);
  }
}
function incoming(msg) {
	if (msg.method.startsWith('assistant.')) provider.post({type:'assistantEvent',method:msg.method,value:msg.params});
  if (msg.method === 'run.event') provider.stream(msg.params);
  if (msg.method === 'install.output') output.appendLine(msg.params?.line || '');
  if (msg.method === 'knowledge.progress' && knowledgeProgress?.id === msg.params?.id) knowledgeProgress.report({message:'Semantic enrichment: '+msg.params.done+'/'+msg.params.total+' passages (including cache hits)'});
  if (msg.method === 'desktop.window.changed') { currentStatus.desktopWindow = msg.params || {available:false,hidden:false}; provider.notifyStatus(); }
  if (msg.method === 'run.finished') refresh();
  if (msg.method === 'run.approval.required') {
    const p = msg.params;
    vscode.window.showWarningMessage(p.description, { modal:true }, 'Allow once', 'Allow for this run', 'Deny')
      .then(choice => call(choice === 'Allow once' || choice === 'Allow for this run' ? 'runs.approve' : 'runs.deny', { approvalId:p.approvalId,remember:choice === 'Allow for this run' })).catch(report);
  }
}
function editorWorkspace() {
  const editor = vscode.window.activeTextEditor;
  if (!editor || editor.document.uri.scheme && editor.document.uri.scheme !== 'file') return '';
  const folder = vscode.workspace.getWorkspaceFolder?.(editor.document.uri);
  if (folder) return folder.uri.fsPath;
  const file = path.resolve(editor.document.uri.fsPath);
  return vscode.workspace.workspaceFolders?.map(folder => folder.uri.fsPath).find(root => {
    const rel = path.relative(path.resolve(root),file);
    return rel === '' || (!rel.startsWith('..'+path.sep) && rel !== '..' && !path.isAbsolute(rel));
  }) || '';
}
async function alignEditorWorkspace() {
  const root = editorWorkspace();
  if (!root || path.resolve(root) === path.resolve(workspace())) return true;
  const choice = await vscode.window.showWarningMessage('The active file belongs to another workspace folder. Switch PrAImate to '+path.basename(root)+' and start a new chat?',{modal:true},'Switch workspace');
  if (choice !== 'Switch workspace') return false;
  await configure({workspace:root});
  return true;
}
function editorContext() {
  const editor = vscode.window.activeTextEditor;
  if (!editor) return { workspace:workspace() };
  const root = editorWorkspace();
  if (!root || path.resolve(root) !== path.resolve(workspace())) return {workspace:workspace(),workspaceMismatch:root || 'outside the open workspace'};
  const doc = editor.document, sel = editor.selection;
  return { workspace:workspace(), activeFile:doc.uri.fsPath, activeFileRel:path.relative(workspace(),doc.uri.fsPath), language:doc.languageId,
    content:sel.isEmpty ? doc.getText().slice(0, 64000) : '',
    selection:{ text:sel.isEmpty ? '' : doc.getText(sel), startLine:sel.start.line+1, endLine:sel.end.line+1 } };
}

class ChatView {
  constructor() { this.messages = []; this.chatId = ''; this.busy = false; this.current = null; this.attachments = []; this.views = new Set(); this.readyViews = new Set(); this.sidebarViews = new Set(); this.requestedPage = ''; }
  resolveWebviewView(view, workerArg = '') {
    // VS Code calls this as (view, context, token) for sidebar views; only the worker panel passes a run id string.
    const workerID = typeof workerArg === 'string' ? workerArg : '';
    this.views.add(view);
    if (!workerID) this.sidebarViews.add(view);
    view.webview.options = { enableScripts:true, localResourceRoots:[context.extensionUri] };
    const mascot = view.webview.asWebviewUri(vscode.Uri.joinPath(context.extensionUri,'resources','monke-mascot.png'));
    view.webview.html = fs.readFileSync(path.join(context.extensionPath,'resources','chat.html'),'utf8')
      .replace('__RESOURCE_SOURCE__',view.webview.cspSource).replace('__MASCOT_URI__',String(mascot))
      .replace('/*__ASSISTANT_SCRIPT__*/',fs.readFileSync(path.join(context.extensionPath,'resources','assistant.js'),'utf8'));
    view.webview.onDidReceiveMessage(data => {
      const handlers = {
        assistantRPC:async () => {
          const allowed = new Set(['assistant.available','assistant.config','assistant.configure','assistant.snapshot','assistant.send','assistant.cancel','assistant.clear','assistant.health','assistant.approve','assistant.artifacts','assistant.artifacts.install','assistant.artifacts.verify','assistant.artifacts.remove','assistant.artifacts.cancel','voice.begin','voice.end','voice.finish','voice.transcribe','voice.cancel']);
          if (!allowed.has(data.method)) throw new Error('Unknown Assistant operation');
          if (['assistant.clear','assistant.artifacts.remove','assistant.artifacts.install'].includes(data.method)) {
            const message = data.method === 'assistant.clear' ? 'Clear the shared Assistant conversation and activity? Prepared tasks and worker history are kept.' : data.method.endsWith('install') ? 'Download and verify this package and its runtime from PrAImate GitHub Releases?' : 'Remove this local model package?';
            if (await vscode.window.showWarningMessage(message,{modal:true},'Continue') !== 'Continue') throw new Error('Operation cancelled');
          }
          try { const result = await call(data.method,data.params || {}); view.webview.postMessage({type:'assistantReply',id:data.requestId,result}); }
          catch(error) { view.webview.postMessage({type:'assistantReply',id:data.requestId,error:error.message}); throw error; }
        },
        ready:() => { this.readyViews.add(view); this.notifyStatus(); this.render(); if(workerID) view.webview.postMessage({type:'navigate',page:'workers',workerID}); else if(this.requestedPage) { view.webview.postMessage({type:'navigate',page:this.requestedPage}); this.requestedPage = ''; } },
        send:() => this.send(data.text, data.useContext),
        updateConfig:() => configure(data.config),
        quickAction:() => this.quickAction(data.action),
        applyCode:() => this.applyCode(data.code),
        attach:() => this.attachFiles(),
        openTerminal:() => openTerminal(),
        chooseTerminal:() => chooseTerminal(),
        stop:() => call('runs.cancel'),
        newChat:() => this.newChat(),
        resumeChat:() => vscode.commands.executeCommand('praimate.resumeChat'),
        selectSkills:() => selectSkills(),
        selectMCP:() => selectMCP(),
        localRoute:() => localRoute(),
        chooseWorkspace:() => chooseWorkspace(),
        managePrivacy:() => managePrivacy(),
        manageLocalHosts:() => manageLocalHosts(),
        toggleDesktopWindow:() => toggleDesktopWindow(),
        reconnect:connect,
        rescan:async () => { currentStatus.availableCLIs = await call('clis.list'); currentStatus.agents = await call('agents.list'); this.notifyStatus(); await refreshModels(); },
        refreshModels:() => refreshModels(true),
        sessionModels:async () => {
          const cli = String(data.cli || currentStatus.activeCLI);
          const models = await call(data.force ? 'models.refresh' : 'models.list',{cli});
          view.webview.postMessage({type:'sessionModels',cli,models:models || []});
        },
        installCLI:() => installCLI(data.cli),
        resetSession:async () => { this.newChat(); await context.workspaceState.update('session', {}); await context.workspaceState.update('launchId', launchInfo.launchId || 'environment'); connect(); },
        output:() => output.show(),
        openPanel:() => vscode.commands.executeCommand('praimate.openPanel'),
        usageDashboard:async () => { const dashboard=await call('usage.dashboard',{month:data.month || ''}); view.webview.postMessage({type:'usageDashboard',dashboard}); },
        collection:() => this.loadCollection(data.name, view),
        selectAgent:() => configure({agentId:data.id}),
        agentDetails:async () => showJSON(await call('agents.get',{id:data.id})),
        editAgent:() => editAgent(data.id),
        deleteAgent:() => deleteAgent(data.id),
        agentKnowledge:() => manageKnowledge(data.id),
        importAgent:() => importAgentPack(),
        newAgent:() => newAgent(),
        importSkill:() => importSkill(),
        manageSkill:() => manageSkill(data.skill),
        addMCP:() => addMCP(),
        manageMCP:() => manageMCP(data.server),
        runWorkflow:() => this.workflow(data.workflow),
        openChat:() => this.resumeChat(data.id),
        renameChat:() => this.renameChat(data.id, data.title),
        deleteChat:() => this.deleteChat(data.id),
        inspectRun:async () => inspectRun(data.id),
        resumeRun:() => resumeManagedRun(data.id),
        workerLoad:async () => {
          const [config,runs] = await Promise.all([call('workers.config.get'),call('workers.list')]);
          view.webview.postMessage({type:'workerState',config:{...config,workspace:workspace()},runs:runs || []});
        },
        workerModels:async () => {
          const models = await call('models.list',{cli:data.cli});
          view.webview.postMessage({type:'workerModels',cli:data.cli,models:models || []});
        },
        workerStart:async () => {
          const id = await call(data.mode === 'parallel' ? 'workers.plan' : 'workers.start',{task:data.task,config:data.config,maxParallel:data.maxParallel || 2});
          view.webview.postMessage({type:'workerStarted',id});
        },
        workerRefresh:async () => {
          const runs = await call('workers.list');
          const id = (runs || []).some(row => row.id === data.id) ? data.id : runs?.[0]?.id;
          const selected = id ? await call('workers.get',{id}) : null;
          view.webview.postMessage({type:'workerSnapshot',runs:runs || [],selectedId:id || '',snapshot:selected});
        },
        workerCancel:async () => { await call('workers.cancel',{id:data.id}); },
        workerRunConfig:async () => { await call('workers.run.config',{id:data.id,config:data.config}); },
        workerPlanRetry:async () => { await call('workers.plan.retry',{id:data.id}); },
        workerOpenExecution:async () => {
          const panel = vscode.window.createWebviewPanel('praimate.workerExecution','PrAImate Worker execution',vscode.ViewColumn.Active,{enableScripts:true,retainContextWhenHidden:true});
          this.resolveWebviewView(panel,data.id);
        },
        workerContinue:async () => { await call('workers.continue',{id:data.id,task:data.task}); },
        workerRename:async () => { await call('workers.rename',{id:data.id,title:data.title}); },
        workerGraphSave:async () => { await call('workers.graph.save',{id:data.id,tasks:data.tasks,maxParallel:data.maxParallel}); },
        workerGraphExecute:async () => { await call('workers.graph.execute',{id:data.id}); },
        workerGraphProfile:async () => { await call('workers.graph.profile',{id:data.id,taskID:data.taskID}); },
        workerGraphReview:async () => { await call('workers.graph.review',{id:data.id,taskID:data.taskID,decision:data.decision}); },
        workerGraphMerge:async () => {
          if (await vscode.window.showWarningMessage('Merge accepted task commits into the target branch? Conflicts stop in a separate review worktree.',{modal:true},'Merge accepted') === 'Merge accepted') await call('workers.graph.merge',{id:data.id});
        },
        workerGraphRetry:async () => {
          await call('workers.graph.retry',{id:data.id,taskID:data.taskID});
          await call('workers.graph.execute',{id:data.id});
        },
        workerGraphResolve:async () => {
          await call('workers.graph.resolve',{id:data.id,taskID:data.taskID});
          await call('workers.graph.execute',{id:data.id});
        },
        workerTaskPreview:async () => {
          const preview = await call('workers.task.preview',{id:data.id,taskID:data.taskID});
          view.webview.postMessage({type:'workerTaskPreview',runId:data.id,taskID:data.taskID,preview});
        },
        workerActivity:async () => {
          const activity = await call('workers.activity',{id:data.id,workerID:data.workerID,before:data.before || 0,limit:100});
          view.webview.postMessage({type:'workerActivity',runId:data.id,workerID:data.workerID,activity});
        },
        workerGraphReset:async () => {
          if (await vscode.window.showWarningMessage('Discard this failed task’s worktree and its uncommitted changes? Inspect it before resetting.',{modal:true},'Discard and reset') === 'Discard and reset') await call('workers.graph.reset',{id:data.id,taskID:data.taskID});
        },
        workerGraphCleanup:async () => {
          if (await vscode.window.showWarningMessage('Remove rejected and merged worktrees, plus any temporary merge attempt? Saved diffs and commits remain.',{modal:true},'Clean worktrees') === 'Clean worktrees') await call('workers.graph.cleanup',{id:data.id});
        },
        workerDelete:async () => {
          const choice = await vscode.window.showWarningMessage('Delete this worker chat, saved history and temporary worktrees? Unmerged and failed task changes will be removed.',{modal:true},'Delete chat');
          if(choice !== 'Delete chat') return;
          await call('workers.delete',{id:data.id});
          view.webview.postMessage({type:'workerDeleted',id:data.id});
        }
      };
      if (handlers[data.type]) Promise.resolve().then(handlers[data.type]).then(() => {
        view.webview.postMessage({type:'actionResult',id:data.requestId,ok:true});
      }).catch(err => {
        view.webview.postMessage({type:'actionResult',id:data.requestId,ok:false,error:err.message});
        output.appendLine(err.message);
      });
    }, undefined, context.subscriptions);
    view.onDidDispose(() => { this.views.delete(view); this.readyViews.delete(view); this.sidebarViews.delete(view); });
  }
  post(message) { for (const view of this.views) view.webview.postMessage(message); }
  navigate(page) {
    // A dedicated worker window must neither consume a sidebar request nor
    // navigate away from its run when the sidebar changes page.
    const targets = [...this.readyViews].filter(view => this.sidebarViews.has(view));
    this.requestedPage = targets.length ? '' : page;
    for (const view of targets) view.webview.postMessage({type:'navigate',page});
  }
  notifyStatus() { this.post({type:'status',status:{...currentStatus,busy:this.busy,chatId:this.chatId,attachments:this.attachments.map(a => path.basename(a))},context:editorContext()}); }
  render() { this.post({type:'messages',messages:this.messages}); this.notifyStatus(); }
  async loadCollection(name, view) {
    const methods = {history:'chats.list',agents:'agents.list',workflows:'workflows.list',skills:'skills.list',mcp:'mcp.list',runs:'runs.list'};
    if (!methods[name]) throw new Error('Unknown section');
    try {
      if (!currentStatus.connected) throw new Error(currentStatus.error || 'Connecting to PrAImate…');
      const items = await call(methods[name]);
      view.webview.postMessage({type:'collection',name,items:items || []});
    } catch (err) { view.webview.postMessage({type:'collection',name,error:err.message}); }
  }
  async renameChat(id, title) {
    const value = await vscode.window.showInputBox({prompt:'Conversation title',value:title,validateInput:v => !v.trim() ? 'Required' : undefined});
    if (value === undefined) return;
    await call('chats.rename',{id,title:value.trim()}); refresh();
  }
  async deleteChat(id) {
    if (this.busy) throw new Error('Stop the active run first');
    if (await vscode.window.showWarningMessage('Delete this conversation and its messages? This cannot be undone.',{modal:true},'Delete') !== 'Delete') return;
    await call('chats.delete',{id});
    if (this.chatId === id) this.newChat();
    refresh();
  }
  newChat() {
    if (this.busy) throw new Error('Stop the active run first');
    this.chatId = ''; this.messages = []; this.current = null; this.attachments = [];
    context.workspaceState.update('chatId', undefined);
    this.render();
  }
  async resumeChat(id) {
    if (this.busy) throw new Error('Stop the active run first');
    const chat = await call('chats.get',{id});
    const settings = chat.Settings;
    await configure({cli:chat.CLIAgent,agentId:chat.AgentID,model:settings.model || '',tools:settings.tools || 'safe',
      mcpServers:settings.mcp_configured ? (settings.mcp_servers || []) : null,
      skills:settings.skills_v2?.configured ? (settings.skills_v2.bindings || []).map(b => ({ref:b.ref,activation:b.activation,
        digest:settings.skills_lock?.entries?.find(e => e.ref === b.ref)?.digest || ''})) : null,
      localEndpoint:settings.local?.endpoint || '',localModel:settings.local?.model || '',
      localContextTokens:settings.local?.context_tokens || 0,localOutputTokens:settings.local?.output_tokens || 0});
    this.chatId = chat.ID;
    await context.workspaceState.update('chatId',this.chatId);
    await this.loadMessages();
  }
  async loadMessages() {
    const rows = await call('chats.messages',{id:this.chatId});
    this.messages = (rows || []).map(m => ({role:m.Role,content:m.Content,
      attachments:Array.isArray(m.Meta?.attachments) ? m.Meta.attachments.map(value => path.basename(value)) : [],
      tools:Array.isArray(m.Meta?.activity) ? m.Meta.activity.filter(a => a.type !== 'reasoning').map((a,index) => ({id:index,name:a.tool || a.type,detail:a.detail,status:a.ok === false ? 'failed' : 'completed'})) : []}));
    this.render();
  }
  async attachFiles() {
    const uris = await vscode.window.showOpenDialog({title:'Attach files to this chat',canSelectMany:true,canSelectFiles:true,canSelectFolders:false});
    if (!uris?.length) return;
    this.attachments.push(...uris.map(uri => uri.fsPath));
    this.attachments = [...new Set(this.attachments)].slice(0,20);
    this.notifyStatus();
  }
  async send(text, includeContext) {
    if (!text?.trim()) return;
    if (this.busy) throw new Error('A run is already active');
    if (!currentStatus.connected) throw new Error('PrAImate Core is offline');
    if (includeContext && !await alignEditorWorkspace()) return;
    this.busy = true;
    this.notifyStatus();
    try {
      if (!this.chatId) {
        const chat = await call('chats.create',{title:text.slice(0,80)});
        this.chatId = chat.ID;
        await context.workspaceState.update('chatId',this.chatId);
      }
      let attachments = [];
      if (this.attachments.length) attachments = await call('attachments.stage',{chatId:this.chatId,sources:this.attachments});
      this.messages.push({role:'user',content:text});
      this.current = {role:'assistant',content:'',tools:[],inProgress:true};
      this.messages.push(this.current); this.render();
      const res = await call('chats.send',{chatId:this.chatId,prompt:text,attachments:attachments.map(a => a.path),context:includeContext ? editorContext() : null});
      this.attachments = [];
      this.current.content = res.reply || this.current.content;
      await this.loadMessages();
    } catch (err) {
      if (this.current) { this.current.content += '\n'+err.message; this.current.isError = true; }
      else report(err);
      throw err;
    } finally {
      if (this.current) this.current.inProgress = false;
      this.busy = false; this.current = null; this.render(); refresh();
    }
  }
  stream(event) {
    if (!this.current || (event.chatId && event.chatId !== this.chatId)) return;
    if (event.type === 'text') this.current.content += event.text || '';
    else if (event.type === 'tool_start') this.current.tools.push({id:event.id,name:event.tool,detail:event.detail,status:'running'});
    else if (event.type === 'tool_finish') {
      const tool = this.current.tools.find(t => (event.id ? t.id === event.id : t.name === event.tool) && t.status === 'running');
      if (tool) tool.status = event.ok ? 'completed' : 'failed';
    } else if (event.type === 'context_compacted' || event.type === 'context_recovery') this.current.tools.push({name:'Context',detail:event.detail,status:'completed'});
    else if (event.type === 'error') this.current.content += '\n'+(event.text || event.detail || '');
    this.render();
  }
  async workflow(wf) {
    if (this.busy) throw new Error('Stop the active run first');
    const inputs = {};
    for (const input of wf.inputs || []) {
      const value = await vscode.window.showInputBox({prompt:input.prompt || input.name,value:input.default || '',placeHolder:input.placeholder,
        validateInput:v => input.required && !v.trim() ? 'Required' : undefined});
      if (value === undefined) return;
      inputs[input.name] = value;
    }
    await configure({agentId:wf.agentId});
    this.newChat();
    this.busy = true;
    this.current = {role:'assistant',content:'',tools:[],inProgress:true};
    this.messages.push(this.current); this.render();
    try {
      const result = await call('workflows.run',{agentId:wf.agentId,name:wf.name,inputs});
      if (result.ChatID) {
        this.chatId = result.ChatID;
        await context.workspaceState.update('chatId',this.chatId);
        await this.loadMessages();
      }
      if (result.error) {
        const message = {role:'assistant',content:result.error,isError:true};
        this.messages.push(message);
      }
    } catch (err) { if(this.current) {this.current.content += '\n'+err.message; this.current.isError = true;} }
    finally { if (this.current) this.current.inProgress = false; this.busy = false; this.current = null; this.render(); refresh(); }
  }
  quickAction(action) {
    const prompts = {explain:'Explain this code.',fix:'Find and fix bugs in this code.',refactor:'Refactor this code for clarity.',test:'Generate tests for this code.',reviewFile:'Review this file for bugs and security issues.'};
    if (action === 'reviewDiff') return this.reviewDiff();
    return this.send(prompts[action],true);
  }
  async reviewDiff() {
    const git = vscode.extensions.getExtension('vscode.git');
    const api = git && (await git.activate()).getAPI(1);
    const repo = api?.repositories.find(r => r.rootUri.fsPath === workspace());
    if (!repo) throw new Error('No Git repository is open');
    const diff = (await repo.diff(false)) + '\n' + (await repo.diff(true));
    if (!diff.trim()) throw new Error('No staged or unstaged changes');
    if (diff.length > 64000) throw new Error('Diff is too large; select a file or smaller change');
    return this.send('Review these working-tree and staged changes:\n'+diff,false);
  }
  async applyCode(code) {
    const editor = vscode.window.activeTextEditor;
    if (!editor || typeof code !== 'string') throw new Error('Open an editor before applying code');
    if (await vscode.window.showWarningMessage('Apply this code to '+path.basename(editor.document.uri.fsPath)+'?',{modal:true},'Apply') !== 'Apply') return;
    const applied = await editor.edit(edit => editor.selection.isEmpty ? edit.insert(editor.selection.active,code) : edit.replace(editor.selection,code));
    if (!applied) throw new Error('The editor rejected the change');
  }
}

async function selectSkills() {
  const all = await call('skills.list');
  const picked = await vscode.window.showQuickPick((all || []).map(sk => ({label:sk.name || sk.ref,
    description:sk.approved ? sk.ref : 'Not approved — use Manage to review',sk,
    picked:(currentStatus.skills || []).some(s => s.ref === sk.ref && s.digest === sk.digest)})),{canPickMany:true,placeHolder:'Select approved skill versions for this session'});
  if (!picked) return;
  if (picked.some(p => !p.sk.approved)) throw new Error('Review and approve these exact skill versions before selecting them');
  await configure({skills:picked.map(p => ({ref:p.sk.ref,digest:p.sk.digest,
    activation:(currentStatus.skills || []).find(s => s.ref === p.sk.ref && s.digest === p.sk.digest)?.activation || (picked.length > 3 ? 'auto' : 'pinned')}))});
}
async function selectMCP() {
  const servers = (await call('mcp.list')) || [];
  const list = Array.isArray(servers) ? servers : [];
  const picked = await vscode.window.showQuickPick(list.map(s => ({label:s.name,description:s.enabled ? 'Enabled globally' : 'Disabled globally',id:s.id,
    picked:currentStatus.mcpServers == null ? s.enabled : currentStatus.mcpServers.includes(s.id)})),{canPickMany:true,placeHolder:'MCP servers selected for this session'});
  if (picked) await configure({mcpServers:picked.map(p => p.id)});
}
async function localRoute() {
  const endpoint = await vscode.window.showInputBox({prompt:'OpenAI-compatible endpoint (empty disables local routing)',value:currentStatus.localEndpoint || ''});
  if (endpoint === undefined) return;
  let model = '';
  if (endpoint) {
    model = await vscode.window.showInputBox({prompt:'Local model name',value:currentStatus.localModel || ''});
    if (model === undefined) return;
  }
  await configure({localEndpoint:endpoint,localModel:model});
}
async function configurePicker() {
  const field = await vscode.window.showQuickPick(['CLI','Model','Agent','Permissions']);
  if (field === 'CLI') {
    const options = await call('clis.list');
    const pick = await vscode.window.showQuickPick(options.filter(c => c.available).map(c => ({label:c.label,id:c.id})));
    if (pick) await configure({cli:pick.id,agentId:'',model:'',tools:'safe',localEndpoint:'',localModel:''});
  } else if (field === 'Model') {
    const value = await vscode.window.showInputBox({prompt:'Model (empty uses CLI default)',value:currentStatus.activeModel});
    if (value !== undefined) await configure({model:value});
  } else if (field === 'Agent') {
    const agents = await call('agents.list');
    const pick = await vscode.window.showQuickPick([{label:'No persona',id:''},...agents.map(a => ({label:a.name,id:a.id}))]);
    if (pick) await configure({agentId:pick.id});
  } else if (field === 'Permissions') {
    const levels = currentStatus.availableCLIs.find(c => c.id === currentStatus.activeCLI)?.capabilities?.toolLevels || [''];
    const value = await vscode.window.showQuickPick(levels.map(level => level || 'safe'));
    if (value) await configure({tools:value});
  }
}

async function editAgent(id) {
  const yaml = id ? await call('agents.yaml',{id}) : `schema: praimate.agent/v2
id: my-agent
name: My Agent
description: Describe when to use this agent.
instructions: |
  You are a focused software assistant.
supports:
  - ${currentStatus.activeCLI || 'praimate-code'}
surfaces:
  - editor
workflows: []
`;
  const doc = await vscode.workspace.openTextDocument({content:yaml,language:'yaml'});
  await vscode.window.showTextDocument(doc);
  vscode.window.showInformationMessage('Edit the YAML, then run “PrAImate: Save Active Agent YAML”.');
}
async function newAgent() {
  const mode = await vscode.window.showQuickPick([{label:'Guided agent',id:'guided',description:'Choose a runtime preset and capabilities'},{label:'Manual YAML',id:'yaml',description:'Edit the complete agent and workflow schema'}],{placeHolder:'Create agent'});
  if (!mode) return;
  if (mode.id === 'yaml') return editAgent();
  const name = await vscode.window.showInputBox({prompt:'Agent name'}); if (!name) return;
  const purpose = await vscode.window.showInputBox({prompt:'What should this agent do?'}); if (!purpose) return;
  const preset = await vscode.window.showQuickPick([{label:'Simple',id:'simple'},{label:'Tool enabled',id:'tool-enabled'},{label:'Autonomous managed run',id:'autonomous'}],{placeHolder:'Runtime preset'}); if (!preset) return;
  const knowledge = await vscode.window.showQuickPick([{label:'No managed knowledge',id:''},{label:'Raw files',id:'raw'},{label:'Indexed RAG (built-in, optional Graphify)',id:'rag'}]); if (!knowledge) return;
  const options = [{label:'Read project',id:'read_project'},{label:'Analyze code',id:'analyze_code'},{label:'Use Git',id:'use_git'},{label:'Execute commands',id:'execute_commands'},
    {label:'Modify files',id:'modify_files'},{label:'Network',id:'network'},{label:'External services',id:'external_services'}];
  const selected = await vscode.window.showQuickPick(options,{canPickMany:true,placeHolder:'Allowed capabilities'}); if (!selected) return;
  const capabilities = Object.fromEntries(selected.map(item => [item.id,true]));
  const request = {name,purpose,preset:preset.id,knowledge:knowledge.id,capabilities};
  const preview = await call('agents.guided.preview',request); await showJSON(preview);
  if (await vscode.window.showWarningMessage('Create this agent with the reviewed runtime and capabilities?',{modal:true},'Create agent') !== 'Create agent') return;
  await call('agents.guided.create',request); currentStatus.agents = await call('agents.list'); refresh(); provider.notifyStatus();
}
async function saveAgentYAML() {
  const doc = vscode.window.activeTextEditor?.document;
  if (!doc || !['yaml','plaintext'].includes(doc.languageId)) throw new Error('Open an agent YAML document first');
  const agent = await call('agents.save',{yaml:doc.getText()});
  currentStatus.agents = await call('agents.list');
  refresh(); provider.notifyStatus();
  vscode.window.showInformationMessage('Saved agent '+agent.Name+'.');
}
async function deleteAgent(id) {
  if (await vscode.window.showWarningMessage('Delete this agent? Its managed knowledge files are retained.',{modal:true},'Delete') !== 'Delete') return;
  if (currentStatus.activeAgent === id) await configure({agentId:''});
  await call('agents.delete',{id});
  currentStatus.agents = await call('agents.list'); refresh(); provider.notifyStatus();
}
async function importAgentPack() {
  const picked = await vscode.window.showOpenDialog({title:'Import PrAImate agent',canSelectMany:false,filters:{'PrAImate agents':['praimate-agent','yaml','yml','md','markdown']}});
  if (!picked?.length) return;
  const selected = picked[0].fsPath;
  if (/\.(ya?ml|md|markdown)$/i.test(selected)) {
    await call('agents.import',{path:selected});
    if (/\.(md|markdown)$/i.test(selected)) vscode.window.showInformationMessage('Imported Markdown. External model/tool/permission options are retained for export; configure the PrAImate runtime separately.');
  } else {
    const review = await call('agents.pack.inspect',{path:selected});
    await showJSON(review);
    if (await vscode.window.showWarningMessage('Import this reviewed agent pack and approve exactly its bundled skill digests?',{modal:true},'Import reviewed pack') !== 'Import reviewed pack') return;
    await call('agents.pack.import',{path:selected,review:review.review_digest || review.ReviewDigest});
  }
  currentStatus.agents = await call('agents.list'); refresh(); provider.notifyStatus();
}
async function exportAgentPack(id) {
  const target = await vscode.window.showSaveDialog({title:'Export portable PrAImate agent',defaultUri:vscode.Uri.file(path.join(workspace(),id+'.praimate-agent')),filters:{'PrAImate agent':['praimate-agent'],'OpenCode-compatible Markdown':['md'],'Agent YAML':['yaml']}});
  if (target) await call(/\.(ya?ml|md|markdown)$/i.test(target.fsPath) ? 'agents.export' : 'agents.pack.export',{id,path:target.fsPath});
}
let knowledgeProgress = null;
async function manageKnowledge(id) {
  const rows = await call('agents.knowledge.list',{id});
  const action = await vscode.window.showQuickPick([{label:'$(add) Add files…',action:'add'},{label:'$(trash) Remove a file…',action:'remove',disabled:!rows.length},
    {label:'$(settings-gear) Configure source and model enrichment…',action:'config'},
    {label:'$(database) Build retrieval index',action:'index'},
    {label:'$(export) Export portable agent…',action:'export'}],{placeHolder:'Agent knowledge and distribution'});
  if (!action) return;
  if (action.action === 'config') {
    await configureKnowledge(id);
  } else if (action.action === 'add') {
    const picked = await vscode.window.showOpenDialog({title:'Add agent knowledge',canSelectMany:true,canSelectFiles:true,canSelectFolders:true});
    if (picked?.length) await call('agents.knowledge.add',{id,sources:picked.map(uri => uri.fsPath)});
  } else if (action.action === 'remove') {
    const selected = await vscode.window.showQuickPick(rows,{placeHolder:'Remove managed knowledge file'});
    if (selected && await vscode.window.showWarningMessage('Remove '+selected+'?',{modal:true},'Remove') === 'Remove') await call('agents.knowledge.delete',{id,path:selected});
  } else if (action.action === 'index') {
    if (provider.busy) throw new Error('Stop the active operation before indexing knowledge');
    const indexRPC = rpc;
    if (!indexRPC) throw new Error('PrAImate Core is offline');
    provider.busy = true;
    provider.notifyStatus();
    try {
      await vscode.window.withProgress({location:vscode.ProgressLocation.Notification,title:'Indexing agent knowledge',cancellable:true},async (progress,token) => {
        knowledgeProgress = {id,report:progress.report.bind(progress)};
        const cancellation = token.onCancellationRequested(() => {
          indexRPC.request('runs.cancel').catch(err => output.appendLine('Index cancellation: '+err.message));
        });
        try {
          const result = await indexRPC.request('agents.knowledge.index',{id});
          if (!token.isCancellationRequested) await showJSON(result);
        } catch (err) {
          if (!token.isCancellationRequested) throw err;
        } finally { knowledgeProgress = null; cancellation.dispose(); }
      });
    } finally { provider.busy = false; provider.notifyStatus(); }
  }
  else await exportAgentPack(id);
}
async function configureKnowledge(id) {
  const saved = await call('agents.knowledge.config.get',{id});
  const config = {...saved.config};
  const modes = [{label:'RAG · focused retrieval',id:'rag'},{label:'Raw · search and read sources',id:'raw'},{label:'Disabled · retain configuration',id:''}];
  const mode = await vscode.window.showQuickPick(modes.map(item => ({...item,description:item.id === (saved.mode || '') ? 'Current mode' : ''})),{placeHolder:'Agent knowledge mode'});
  if (!mode) return;
  const source = await vscode.window.showQuickPick(['Local documents','Remote knowledge service'],{placeHolder:'Knowledge source'});
  if (!source) return;
  config.source = source.startsWith('Remote') ? 'remote' : 'local';
  let apiKey = '', removeKey = false;
  if (config.source === 'remote') {
    const endpoint = await vscode.window.showInputBox({title:'Knowledge service URL',value:config.endpoint || '',prompt:'HTTP(S) base URL implementing /health, /query, /search and /read'});
    if (endpoint === undefined) return;
    config.endpoint = endpoint;
    apiKey = await vscode.window.showInputBox({title:'Optional knowledge API key',password:true,prompt:saved.hasAPIKey ? 'Leave blank to keep the saved key. Keys are never exported.' : 'Bearer token. Leave blank for no authentication.'});
    if (apiKey === undefined) return;
    if (saved.hasAPIKey && !apiKey) {
      const keyAction = await vscode.window.showQuickPick(['Keep saved key','Remove saved key'],{placeHolder:'Saved credential'});
      if (!keyAction) return;
      removeKey = keyAction === 'Remove saved key';
    }
  } else {
    const clis = await call('clis.list');
    const backend = await vscode.window.showQuickPick([{label:'Offline · no model',id:''},{label:'Local model',id:'local'},...(clis || []).filter(c => c.available && c.capabilities?.managedWorker !== false).map(c => ({label:c.label,id:c.id}))],{placeHolder:'Optional source enrichment (uses your CLI subscription or local host)'});
    if (!backend) return;
    config.enrichment_cli = backend.id;
    if (backend.id) {
      if (backend.id === 'local') {
        const endpoint = await vscode.window.showInputBox({title:'Configured local model endpoint',value:config.enrichment_endpoint || currentStatus.localEndpoint || 'http://localhost:11434/v1'});
        if (endpoint === undefined) return;
        config.enrichment_endpoint = endpoint;
      } else config.enrichment_endpoint = '';
      const models = await call('models.list',{cli:backend.id === 'local' ? 'praimate-cli' : backend.id});
      const model = await vscode.window.showInputBox({title:'Enrichment model',value:config.enrichment_model || '',prompt:'Model ID or alias'+(models?.length ? ' · available: '+models.slice(0,12).join(', ') : '')});
      if (model === undefined) return;
      config.enrichment_model = model;
      const limit = await vscode.window.showInputBox({title:'Maximum source passages',value:String(config.max_enrichment_chunks || 64),prompt:'1–512; unchanged passages reuse their cache',validateInput:v => /^\d+$/.test(v) && Number(v)>=1 && Number(v)<=512 ? undefined : 'Enter a number from 1 to 512'});
      if (limit === undefined) return;
      config.max_enrichment_chunks = Number(limit);
    }
  }
  await call('agents.knowledge.config.save',{id,config,apiKey,removeKey,mode:mode.id});
  if (config.source === 'remote') {
    await call('agents.knowledge.config.test',{id});
    vscode.window.showInformationMessage('Remote knowledge service is available.');
  } else vscode.window.showInformationMessage('Knowledge configuration saved. Build the index to apply enrichment.');
  currentStatus.agents = await call('agents.list'); provider.notifyStatus(); refresh();
}
async function skillLibrary(request) { return call('skills.library',request); }
async function ensureSkillLibrary() {
  const state = await call('skills.rollout.get');
  if (!state.enabled && !state.Enabled) {
    if (await vscode.window.showWarningMessage('Enable the controlled, versioned skills library on this device?',{modal:true},'Enable') !== 'Enable') return false;
    await call('skills.rollout.set',{enabled:true});
  }
  return true;
}
async function skillRevision() {
  const state = await skillLibrary({action:'list'});
  return state.view?.Revision ?? state.view?.revision ?? 0;
}
async function importSkill() {
  if (!await ensureSkillLibrary()) return;
  const kind = await vscode.window.showQuickPick([{label:'Folder',kind:'directory'},{label:'ZIP archive',kind:'zip'},{label:'GitHub repository',kind:'github'}],{placeHolder:'Skill source'});
  if (!kind) return;
  let source, gitRef = '', subpath = '';
  if (kind.kind === 'github') {
    source = await vscode.window.showInputBox({prompt:'GitHub repository or skill folder HTTPS URL (/tree/...)'}); if (!source) return;
    if (!/^https:\/\/github\.com\/[^/]+\/[^/]+\/tree\/[^/]+(?:\/|$)/.test(source.trim())) {
      gitRef = await vscode.window.showInputBox({prompt:'Git branch, tag or commit (optional)',value:''}); if (gitRef === undefined) return;
      subpath = await vscode.window.showInputBox({prompt:'Package subpath (optional)',value:''}); if (subpath === undefined) return;
    }
  } else {
    const picked = await vscode.window.showOpenDialog({title:'Choose skill '+kind.label.toLowerCase(),canSelectMany:false,canSelectFiles:kind.kind === 'zip',canSelectFolders:kind.kind === 'directory',filters:kind.kind === 'zip' ? {'ZIP':['zip']} : undefined});
    if (!picked?.length) return; source = picked[0].fsPath;
  }
  const base = {kind:kind.kind,source,git_ref:gitRef,subpath};
  let preview = await skillLibrary({action:'inspect',...base});
  if (kind.kind === 'github') Object.assign(base,{source:preview.source || source,git_ref:preview.git_ref || gitRef,subpath:preview.subpath ?? subpath});
  const usedRefs = new Set();
  let selections = (preview.packages || []).map(p => {
    let ref = p.ref, suffix = 2;
    while (usedRefs.has(ref)) ref = p.ref+'-'+suffix++;
    usedRefs.add(ref);
    return {index:p.index,ref};
  });
  if (selections.length > 1) {
    const picked = await vscode.window.showQuickPick(preview.packages.map((p,i) => ({label:p.manifest?.Name || p.manifest?.name || p.ref,description:p.subpath,picked:true,selection:selections[i]})),{canPickMany:true,placeHolder:'Choose skills to import (all selected by default)'});
    if (!picked?.length) return;
    selections = picked.map(p => p.selection);
    preview = await skillLibrary({action:'inspect',selections,...base});
  }
  await showJSON(preview);
  const action = await vscode.window.showInformationMessage(`Import ${selections.length} reviewed skill${selections.length === 1 ? '' : 's'}?`,{modal:true},'Import and enable','Import only');
  if (!action) return;
  await skillLibrary({action:'install',revision:await skillRevision(),review:preview.review,selections,approved:action === 'Import and enable',...base});
  refresh();
}
async function manageSkill(skill) {
  if (!await ensureSkillLibrary()) return;
  const action = await vscode.window.showQuickPick([{label:'Review files',id:'read'},{label:skill.approved ? 'Revoke approval' : 'Approve exact digest',id:'approve'},
    {label:'Forget this version',id:'forget'}],{placeHolder:skill.ref});
  if (!action) return;
  if (action.id === 'read') return showJSON(await skillLibrary({action:'read',ref:skill.ref,digest:skill.digest}));
  if (action.id === 'forget' && await vscode.window.showWarningMessage('Forget '+skill.ref+' at this digest?',{modal:true},'Forget') !== 'Forget') return;
  await skillLibrary({action:action.id,revision:await skillRevision(),ref:skill.ref,digest:skill.digest,review:skill.digest,approved:!skill.approved});
  refresh();
}
async function addMCP() {
  const type = await vscode.window.showQuickPick([{label:'Provider catalogue',id:'catalogue'},{label:'Custom stdio / HTTP / SSE',id:'custom'}]);
  if (!type) return;
  if (type.id === 'catalogue') {
    const catalogue = await call('mcp.catalogue');
    const item = await vscode.window.showQuickPick(catalogue.map(entry => ({label:entry.name,description:entry.description,entry})));
    if (!item) return;
    let apiKey = '';
    if (item.entry.auth?.type === 'api_key') { apiKey = await vscode.window.showInputBox({prompt:item.entry.auth.label || 'API key',password:true}); if (apiKey === undefined) return; }
    await call('mcp.connect',{catalogueKey:item.entry.key,apiKey});
  } else {
    const name = await vscode.window.showInputBox({prompt:'Server name'}); if (!name) return;
    const transport = await vscode.window.showQuickPick(['stdio','http','sse']); if (!transport) return;
    const target = await vscode.window.showInputBox({prompt:transport === 'stdio' ? 'Executable and arguments' : 'Endpoint URL'}); if (!target) return;
    const env = transport === 'stdio' ? await vscode.window.showInputBox({prompt:'Environment (KEY=value, comma/newline separated; optional)',password:true}) : '';
    await call('mcp.add',{name,transport,command:transport === 'stdio' ? target : '',url:transport === 'stdio' ? '' : target,env:parseEnv(env || '')});
  }
  refresh();
}
function parseEnv(value) { return Object.fromEntries(value.split(/[\r\n,]+/).map(line => line.split(/=(.*)/s)).filter(parts => parts[0]?.trim() && parts.length > 1).map(([key,val]) => [key.trim(),val.trim()])); }
async function manageMCP(server) {
  const action = await vscode.window.showQuickPick([{label:'Test connection and tools',id:'test'},{label:server.enabled ? 'Disable globally' : 'Enable globally',id:'toggle'},{label:'Edit definition',id:'edit'},{label:'Delete server',id:'delete'}],{placeHolder:server.name});
  if (!action) return;
  if (action.id === 'test') return showJSON(await call('mcp.probe',{id:server.id}));
  if (action.id === 'toggle') await call('mcp.enable',{id:server.id,enabled:!server.enabled});
  if (action.id === 'delete') {
    if (await vscode.window.showWarningMessage('Delete MCP server '+server.name+'?',{modal:true},'Delete') !== 'Delete') return;
    await call('mcp.delete',{id:server.id});
  }
  if (action.id === 'edit') {
    const current = await call('mcp.get',{id:server.id});
    const name = await vscode.window.showInputBox({prompt:'Server name',value:current.name}); if (!name) return;
    const target = await vscode.window.showInputBox({prompt:current.transport === 'stdio' ? 'Executable and arguments' : 'Endpoint URL',value:current.transport === 'stdio' ? [current.command,...(current.args || [])].join(' ') : current.url}); if (!target) return;
    const env = current.transport === 'stdio' ? await vscode.window.showInputBox({prompt:'Environment (re-enter secrets to replace)',password:true,value:Object.entries(current.env || {}).map(([k,v]) => k+'='+v).join('\n')}) : '';
    if (env === undefined) return;
    await call('mcp.update',{id:server.id,request:{name,transport:current.transport,command:current.transport === 'stdio' ? target : '',url:current.transport === 'stdio' ? '' : target,env:parseEnv(env || '')}});
  }
  refresh();
}
async function inspectRun(id) {
  const run = await call('runs.get',{id});
  const action = await vscode.window.showQuickPick([{label:'Execution details',id:'details'},...(run.artifacts || run.Artifacts || []).map(a => ({label:'Artifact: '+a.name,artifact:a.name}))],{placeHolder:'Managed run'});
  if (!action) return;
  if (action.artifact) return showText(await call('runs.artifact',{id,name:action.artifact}),'plaintext');
  return showJSON(run);
}
async function resumeManagedRun(id) {
  if (provider.busy) throw new Error('Another run is already active');
  provider.busy = true; provider.notifyStatus();
  try { const run = await call('runs.resume',{id}); await showJSON(run); refresh(); }
  finally { provider.busy = false; provider.notifyStatus(); }
}
async function managePrivacy() {
  const patterns = (await call('privacy.list')) || [];
  const list = Array.isArray(patterns) ? patterns : [];
  const action = await vscode.window.showQuickPick([{label:'$(add) Add redaction pattern',id:'add'},...list.map((pattern,index) => ({label:pattern,description:'Remove pattern',index}))],{placeHolder:'Custom privacy redaction patterns'});
  if (!action) return;
  if (action.id === 'add') { const pattern = await vscode.window.showInputBox({prompt:'Regular expression to redact before model calls'}); if (pattern) await call('privacy.add',{pattern}); }
  else if (await vscode.window.showWarningMessage('Remove this redaction pattern?',{modal:true},'Remove') === 'Remove') await call('privacy.delete',{index:action.index});
}
async function manageLocalHosts() {
  const hosts = (await call('local.hosts.list')) || [];
  const list = Array.isArray(hosts) ? hosts : [];
  const picked = await vscode.window.showQuickPick([{label:'$(add) Add local model host',add:true},...list.map(host => ({label:host.name,description:(host.isDefault ? 'Default · ' : '')+host.endpoint,host}))],{placeHolder:'Local model profiles'});
  if (!picked) return;
  if (picked.add) {
    const name = await vscode.window.showInputBox({prompt:'Profile name',value:'Local model'}); if (!name) return;
    const endpoint = await vscode.window.showInputBox({prompt:'OpenAI-compatible or Ollama endpoint',value:'http://localhost:11434'}); if (!endpoint) return;
    const apiKey = await vscode.window.showInputBox({prompt:'API key (optional)',password:true}); if (apiKey === undefined) return;
    const saved = await call('local.hosts.save',{name,endpoint,apiKey});
    const models = await call('local.hosts.test',{id:saved.id,endpoint:saved.endpoint});
    vscode.window.showInformationMessage('Saved '+saved.name+'. Models: '+(models.join(', ') || 'none reported'));
    return;
  }
  const host = picked.host;
  const action = await vscode.window.showQuickPick([{label:'Use for this session',id:'use'},{label:'Test and list models',id:'test'},{label:'Edit profile',id:'edit'},
    ...(!host.isDefault ? [{label:'Make default',id:'default'}] : []),{label:'Delete profile',id:'delete'}],{placeHolder:host.name});
  if (!action) return;
  if (action.id === 'use') {
    const models = await call('local.hosts.test',{id:host.id,endpoint:host.endpoint});
    const model = await vscode.window.showQuickPick(models,{placeHolder:'Local model'}); if (model) await configure({localEndpoint:host.endpoint,localModel:model});
  } else if (action.id === 'test') {
    const models = await call('local.hosts.test',{id:host.id,endpoint:host.endpoint}); await showJSON({host:host.name,endpoint:host.endpoint,models});
  } else if (action.id === 'edit') {
    const name = await vscode.window.showInputBox({prompt:'Profile name',value:host.name}); if (!name) return;
    const endpoint = await vscode.window.showInputBox({prompt:'Endpoint',value:host.endpoint}); if (!endpoint) return;
    const apiKey = await vscode.window.showInputBox({prompt:host.hasApiKey ? 'New API key (empty keeps current)' : 'API key (optional)',password:true}); if (apiKey === undefined) return;
    await call('local.hosts.save',{...host,name,endpoint,apiKey});
  } else if (action.id === 'default') await call('local.hosts.save',{...host,isDefault:true});
  else if (await vscode.window.showWarningMessage('Delete local host '+host.name+'?',{modal:true},'Delete') === 'Delete') await call('local.hosts.delete',{id:host.id});
}

async function openTerminal(cli) {
  if (!vscode.workspace.isTrusted) throw new Error('Trust this workspace before launching a CLI terminal');
  if (!currentStatus.connected) throw new Error('Connect to PrAImate Core before launching a CLI terminal');
  const plan = await call('terminals.prepare',typeof cli === 'string' ? {cli} : {});
  // Keep the launch plan (and host environment) in the extension host, not in
  // the HTML renderer. The terminal receives an executable and separate args.
  try {
    const term = vscode.window.createTerminal(terminalOptions(plan,process.platform,process.env));
    if (plan.usageId) terminalUsage.set(term,{id:plan.usageId,client:rpc});
    term.show();
    return term;
  } catch (error) {
    if (plan.usageId) await call('terminals.closed',{usageId:plan.usageId}).catch(() => {});
    throw error;
  }
}
async function chooseTerminal() {
  if (!vscode.workspace.isTrusted || !currentStatus.connected) throw new Error('Connect in a trusted workspace before choosing a CLI');
  const clis = await call('terminals.list');
  const choices = clis.filter(c => c.available).map(c => ({label:c.label,id:c.id,
    description:c.id === currentStatus.activeCLI ? 'Selected CLI · saved session model' : 'Native terminal · CLI default model'}));
  if (!choices.length) throw new Error('No CLI executables were found on PATH or in PrAImate managed tools.');
  const choice = await vscode.window.showQuickPick(choices,{placeHolder:'Open a native CLI in the editor terminal'});
  if (choice) return openTerminal(choice.id);
}
async function chooseWorkspace() {
  const folders = vscode.workspace.workspaceFolders || [];
  if (!folders.length) throw new Error('Open a workspace folder first');
  const selected = await vscode.window.showQuickPick(folders.map(folder => ({label:folder.name || path.basename(folder.uri.fsPath),description:folder.uri.fsPath,root:folder.uri.fsPath})),{placeHolder:'PrAImate workspace root'});
  if (selected && path.resolve(selected.root) !== path.resolve(workspace())) await configure({workspace:selected.root});
}
async function toggleDesktopWindow() {
  const state = await call('desktop.window.toggle');
  currentStatus.desktopWindow = state;
  provider.notifyStatus();
}
async function installCLI(cli) {
  if (provider.busy) throw new Error('Stop the active operation first');
  const methods = await call('clis.install.methods',{cli});
  if (!methods.length) throw new Error('No supported install method is available on this operating system');
  const selected = await vscode.window.showQuickPick(methods.map(method => ({label:method.label,description:method.recommended ? 'Recommended' : '',detail:method.command,method})),{placeHolder:'Install or update '+cli});
  if (!selected) return;
  const warning = 'Run this installer?\n\n'+selected.method.command+(selected.method.missingPrereqs?.length ? '\n\nIt may also install: '+selected.method.missingPrereqs.join(', ') : '');
  if (await vscode.window.showWarningMessage(warning,{modal:true},'Install') !== 'Install') return;
  provider.busy = true; provider.notifyStatus(); output.show(true);
  try { await call('clis.install',{cli,methodId:selected.method.id}); currentStatus.availableCLIs = await call('clis.list'); provider.notifyStatus(); }
  finally { provider.busy = false; provider.notifyStatus(); }
}
function activate(ctx) {
  context = ctx; stopped = false;
  output = vscode.window.createOutputChannel('PrAImate');
  statusBar = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left,100);
  statusBar.command = 'praimate.reconnect'; statusBar.show();
  provider = new ChatView();
  connectionFile = path.join(context.extensionPath,'connection.json');
  const connectionChanged = (current, previous) => {
    if (stopped) return;
    if (current.mtimeMs === previous.mtimeMs && current.size === previous.size) return;
    try {
      const next = readLaunchInfo();
      if (!currentStatus.connected || next.endpoint !== launchInfo.endpoint || next.token !== launchInfo.token || next.backend !== launchInfo.backend) connect();
    } catch (err) { output.appendLine(err.message); }
  };
  fs.watchFile(connectionFile,{persistent:false,interval:1500},connectionChanged);
  stopWatching = () => fs.unwatchFile(connectionFile,connectionChanged);
  context.subscriptions.push({dispose:stopWatching});
  if (vscode.window.onDidCloseTerminal) context.subscriptions.push(vscode.window.onDidCloseTerminal(term => {
    const usage=terminalUsage.get(term); terminalUsage.delete(term);
    if (usage) usage.client?.request('terminals.closed',{usageId:usage.id}).catch(() => {});
  }));
  if (vscode.workspace.onDidGrantWorkspaceTrust) context.subscriptions.push(vscode.workspace.onDidGrantWorkspaceTrust(connect));
  context.subscriptions.push(output,statusBar,vscode.window.registerWebviewViewProvider('praimate.chatView',provider));
  let panel;
  const commands = {
    openPanel:() => {
      if (panel) { panel.reveal(); return; }
      panel = vscode.window.createWebviewPanel('praimate.workspace','PrAImate',vscode.ViewColumn.One,{enableScripts:true,retainContextWhenHidden:true});
      provider.resolveWebviewView(panel);
      panel.onDidDispose(() => { panel = null; });
      context.subscriptions.push(panel);
    },
    focusChat:() => vscode.commands.executeCommand('praimate.chatView.focus'),
    focusWorkers:async () => { await vscode.commands.executeCommand('praimate.chatView.focus'); provider.navigate('workers'); },
    openAssistant:async () => { await vscode.commands.executeCommand('praimate.chatView.focus'); provider.navigate('assistant'); },
    reconnect:connect,refresh:() => { refresh(); refreshModels().catch(report); },
    newChat:() => provider.newChat(),stop:() => call('runs.cancel'),
    configure:configurePicker,selectSkills,selectMCP,localRoute,
    selectAgent:id => configure({agentId:id}),
    runWorkflow:w => provider.workflow(w),
    resumeChat:async id => {
      if (!id) {
        const rows = await call('chats.list');
        const pick = await vscode.window.showQuickPick(rows.map(c => ({label:c.title,description:c.cli,id:c.id})));
        if (!pick) return;
        id = pick.id;
      }
      await provider.resumeChat(id);
    },
    ask:async agentId => {
      if (typeof agentId === 'string') await configure({agentId});
      const prompt = await vscode.window.showInputBox({prompt:'Ask PrAImate about this project'});
      if (prompt) await provider.send(prompt,true);
    },
    explain:() => provider.quickAction('explain'),fix:() => provider.quickAction('fix'),
    refactor:() => provider.quickAction('refactor'),generateTests:() => provider.quickAction('test'),
    reviewFile:() => provider.quickAction('reviewFile'),reviewChanges:() => provider.reviewDiff(),
    openTerminal, chooseTerminal,
    chooseWorkspace,
    newAgent, saveAgentYAML, importAgent:importAgentPack,
    importSkill, addMCP, managePrivacy, manageLocalHosts, toggleDesktopWindow,
    inspectRun:async id => showJSON(await call('runs.get',{id})),
    diagnostics:async () => showJSON({...await call('system.status'),extensionVersion:require('./package.json').version,
      ui:{openViews:provider.views.size,readyViews:provider.readyViews.size}})
  };
  for (const [name,fn] of Object.entries(commands)) context.subscriptions.push(vscode.commands.registerCommand('praimate.'+name,(...args) => Promise.resolve().then(() => fn(...args)).catch(report)));
  context.subscriptions.push(vscode.window.onDidChangeActiveTextEditor(() => provider.notifyStatus()),vscode.window.onDidChangeTextEditorSelection(() => provider.notifyStatus()));
  connect();
}
async function showJSON(value) {
  return showText(JSON.stringify(value,null,2),'json');
}
async function showText(value, language = 'plaintext') {
  const doc = await vscode.workspace.openTextDocument({content:String(value),language});
  await vscode.window.showTextDocument(doc);
}
function deactivate() { stopped = true; ++generation; stopWatching?.(); disposeConnection(); }
module.exports = { activate,deactivate };
