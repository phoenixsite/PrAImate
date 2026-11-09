'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const {PassThrough} = require('node:stream');
const {RPCClient} = require('./rpc');
const {terminalOptions} = require('./terminal');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const {EventEmitter} = require('node:events');

test('RPC handles split UTF-8, notifications and out-of-order replies', async () => {
  const input = new PassThrough(), output = new PassThrough(), events = [];
  const client = new RPCClient(input,output,m => events.push(m),() => {});
  const a = client.request('first'), b = client.request('second');
  const bytes = Buffer.from(JSON.stringify({id:2,result:'🐒 Windows\\folder'})+'\n');
  const pos = bytes.indexOf(Buffer.from('🐒'));
  input.write(bytes.subarray(0,pos+1)); input.write(bytes.subarray(pos+1));
  input.write(JSON.stringify({method:'run.event',params:{text:'delta'}})+'\n');
  input.write(JSON.stringify({id:1,result:'first'})+'\n');
  assert.equal(await a,'first'); assert.equal(await b,'🐒 Windows\\folder');
  assert.equal(events[0].params.text,'delta'); client.close();
});
test('RPC rejects pending runs on disconnect and bounds ordinary calls', async () => {
  const input = new PassThrough(), output = new PassThrough();
  const client = new RPCClient(input,output,() => {},() => {},10);
  await assert.rejects(client.request('session.get'),/timed out/);
  const rejected = assert.rejects(client.request('chats.send'),/disconnected/);
  input.end(); await rejected; assert.equal(client.pending.size,0);
});
test('RPC permits Stop while a long request is pending', async () => {
  const input = new PassThrough(), output = new PassThrough();
  const client = new RPCClient(input,output,() => {},() => {});
  const run = client.request('chats.send'), stop = client.request('runs.cancel');
  input.write('{"id":2,"result":true}\n{"id":1,"error":{"message":"cancelled"}}\n');
  assert.equal(await stop,true); await assert.rejects(run,/cancelled/); client.close();
});
test('knowledge indexing survives ordinary RPC timeout and accepts cancellation', async () => {
  const input = new PassThrough(), output = new PassThrough();
  const client = new RPCClient(input,output,() => {},() => {},10);
  const run = client.request('agents.knowledge.index',{id:'review'});
  const rejected = assert.rejects(run,/cancelled/);
  await new Promise(resolve => setTimeout(resolve,30));
  assert.equal(client.pending.size,1);
  const stop = client.request('runs.cancel');
  input.write('{"id":2,"result":true}\n{"id":1,"error":{"message":"cancelled"}}\n');
  assert.equal(await stop,true); await rejected; client.close();
});

function extensionHarness(platform, trusted = true, descriptor = null, respond = null) {
  const commands = new Map(), stored = new Map(), requests = [], spawns = [], terminals = [], errors = [];
  const cwd = platform === 'win32' ? 'C:\\Users\\Test User\\Project' : '/tmp/project with spaces';
  const executable = platform === 'win32' ? 'C:\\Program Files\\PrAImate\\praimate.exe' : '/opt/PrAImate/praimate';
  const disposable = {dispose(){}};
  let choiceID, closeTerminal, terminalError, available = ['claude','openclaude','codex','opencode','praimate-code','copilot','antigravity'];
  let session = {cli:'praimate-code',tools:'safe'};
  const vscode = {
    workspace:{isTrusted:trusted,workspaceFolders:[{uri:{fsPath:cwd}}]},
    window:{
      createOutputChannel:() => ({append(){},appendLine(){},dispose(){}}),
      createStatusBarItem:() => ({show(){},dispose(){}}),
      registerWebviewViewProvider:(_,p) => {vscode.provider=p;return disposable;},
      registerTreeDataProvider:() => disposable,
      onDidChangeActiveTextEditor:() => disposable,onDidChangeTextEditorSelection:() => disposable,
      onDidCloseTerminal:fn => {closeTerminal=fn;return disposable;},
      showErrorMessage:m => errors.push(m),createTerminal:o => {if (terminalError) throw Error(terminalError); terminals.push(o);return {show(){}};}
      ,showQuickPick:async items => items.find(item => item.id === choiceID)
    },
    commands:{registerCommand:(id,fn) => {commands.set(id,fn);return disposable;},executeCommand:() => {}},
    EventEmitter:class{constructor(){this.event=()=>{};}fire(){}dispose(){}},
    TreeItem:class{constructor(label){this.label=label;}},StatusBarAlignment:{Left:0}
  };
  const spawn = (exe,args,opts) => {
    spawns.push({exe,args,opts});
    const child = new EventEmitter();
    child.stdout = new PassThrough();child.stdin = new PassThrough();child.stderr = new PassThrough();
    child.kill = () => {child.stdout.end();};
    let buffer = '';
    child.stdin.on('data',chunk => {
      buffer += chunk.toString();
      while (buffer.includes('\n')) {
        const end = buffer.indexOf('\n'), req = JSON.parse(buffer.slice(0,end));
        buffer = buffer.slice(end+1);requests.push(req);
        let result = {};
        if (req.method === 'system.initialize') result = {serverVersion:'test'};
        if (req.method === 'session.update') { session = {...session,...req.params}; result = session; }
        if (['agents.list','models.list','clis.list'].includes(req.method)) result = [];
        if (req.method === 'chats.create') result = {ID:'chat-1'};
        if (req.method === 'chats.send') result = {chatId:'chat-1',reply:'reply'};
        if (req.method === 'chats.messages') result = [{Role:'user',Content:'hello'},{Role:'assistant',Content:'reply'}];
        if (req.method === 'terminals.list') result = available.map(id => ({id,label:id,available:true}));
        if (req.method === 'terminals.prepare') {
          const cli = req.params.cli || session.cli;
          result = {usageId:'usage-'+requests.length,cli,cwd,command:platform === 'win32' ? 'C:\\CLI Tools\\'+cli+'.exe' : '/CLI Tools/'+cli,
            args:cli === session.cli && session.model ? [cli === 'codex' ? '-m' : '--model',session.model] : [],env:{PATH:'/managed/runtime'}};
        }
        if (respond) result = respond(req) ?? result;
        queueMicrotask(() => child.stdout.write(JSON.stringify({id:req.id,result})+'\n'));
      }
    });
    return child;
  };
  let watch;
  const mockFS = {...fs,readFileSync:(file,...args) => file.endsWith('connection.json') && descriptor ? JSON.stringify(descriptor) : fs.readFileSync(file,...args),
    watchFile:(_,opts,fn) => {watch=fn;},unwatchFile(){}};
  const sandbox = {require:name => name==='vscode'?vscode:name==='node:fs'?mockFS:name==='node:child_process'?{spawn}:require(name),
    module:{exports:{}},process:{platform,env:{PRAIMATE_BIN:executable}},console,setTimeout,clearTimeout,Buffer};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'extension.js'),'utf8'),sandbox);
  const ctx = {subscriptions:[],extensionPath:__dirname,extensionUri:{fsPath:__dirname},
    workspaceState:{get:(key,fallback) => stored.has(key)?stored.get(key):fallback,update:async(key,v)=>stored.set(key,v)}};
  sandbox.module.exports.activate(ctx);
  return {commands,requests,spawns,terminals,errors,cwd,executable,stored,provider:()=>vscode.provider,
    closeTerminal:term=>closeTerminal(term),failTerminal:message=>{terminalError=message;},
    choose:id=>{choiceID=id;},setAvailable:ids=>{available=ids;},vscode,
    changeDescriptor:next => {descriptor=next;watch({mtimeMs:2,size:100},{mtimeMs:1,size:100});},
    close:() => sandbox.module.exports.deactivate()};
}
for (const action of ['Import and enable', 'Import only', null]) {
test('skill folder import: '+(action || 'cancel')+' uses one source prompt and a pinned review', async () => {
  const root = 'https://github.com/owner/repo', subpath = 'skills/example', revision = 'a'.repeat(40);
  const h = extensionHarness('linux',true,null,req => {
    if (req.method === 'skills.rollout.get') return {enabled:true};
    if (req.method === 'skills.library' && req.params.action === 'list') return {view:{Revision:7}};
    if (req.method === 'skills.library' && req.params.action === 'inspect') return {source:root,subpath,git_ref:revision,review:'sha256:review',packages:[{index:0,ref:'imported/example'}]};
  });
  try {
    const inputs = [root+'/tree/main/'+subpath,'',''];
    h.vscode.window.showQuickPick = async items => items.find(item => item.kind === 'github');
    h.vscode.window.showInputBox = async () => inputs.shift();
    h.vscode.window.showInformationMessage = async () => action;
    h.vscode.workspace.openTextDocument = async value => value;
    h.vscode.window.showTextDocument = async () => {};
    await h.commands.get('praimate.importSkill')();
    assert.equal(inputs.length,2);
    const inspected = h.requests.find(req => req.method === 'skills.library' && req.params.action === 'inspect');
    assert.equal(inspected.params.source,root+'/tree/main/'+subpath);
    const installed = h.requests.find(req => req.method === 'skills.library' && req.params.action === 'install');
    if (action === null) { assert.equal(installed,undefined); return; }
    assert.ok(installed);
    assert.equal(installed.params.source,root);
    assert.equal(installed.params.subpath,subpath);
    assert.equal(installed.params.git_ref,revision);
    assert.equal(installed.params.review,'sha256:review');
    assert.equal(installed.params.revision,7);
    assert.equal(installed.params.approved,action === 'Import and enable');
    assert.deepEqual(h.errors,[]);
  } finally {h.close();}
});
}
test('bulk skill import deduplicates aliases and enables the reviewed selection together', async () => {
  const root = 'https://github.com/owner/repo', revision = 'a'.repeat(40);
  const h = extensionHarness('linux',true,null,req => {
    if (req.method === 'skills.rollout.get') return {enabled:true};
    if (req.method === 'skills.library' && req.params.action === 'list') return {view:{Revision:7}};
    if (req.method === 'skills.library' && req.params.action === 'inspect') return {source:root,subpath:'skills',git_ref:revision,review:req.params.selections ? 'sha256:selected' : 'sha256:catalogue',packages:[0,1].map(i => ({index:i,ref:'imported/example',manifest:{Name:'example'}}))};
  });
  try {
    h.vscode.window.showInputBox = async () => root+'/tree/main/skills';
    h.vscode.window.showQuickPick = async items => items.some(item => item.kind) ? items.find(item => item.kind === 'github') : items;
    h.vscode.window.showInformationMessage = async () => 'Import and enable';
    h.vscode.workspace.openTextDocument = async value => value;
    h.vscode.window.showTextDocument = async () => {};
    await h.commands.get('praimate.importSkill')();
    const installed = h.requests.find(req => req.method === 'skills.library' && req.params.action === 'install');
    assert.ok(installed);
    assert.deepEqual(installed.params.selections.map(s => s.ref),['imported/example','imported/example-2']);
    assert.equal(installed.params.review,'sha256:selected');
    assert.equal(installed.params.approved,true);
    assert.deepEqual(h.errors,[]);
  } finally {h.close();}
});
test('bulk session skill choices use on-demand loading instead of exceeding the pinned budget', async () => {
  const skills = Array.from({length:4},(_,i) => ({ref:'skill-'+i,digest:'digest-'+i,approved:true,name:'Skill '+i}));
  const h = extensionHarness('linux',true,null,req => req.method === 'skills.list' ? skills : undefined);
  try {
    h.vscode.window.showQuickPick = async items => items;
    await h.commands.get('praimate.selectSkills')();
    const update = h.requests.find(req => req.method === 'session.update' && req.params.skills);
    assert.ok(update);
    assert.equal(update.params.skills.length,4);
    assert.ok(update.params.skills.every(s => s.activation === 'auto'));
    assert.deepEqual(h.errors,[]);
  } finally {h.close();}
});
for (const platform of ['linux','win32']) {
  test(platform+': launches stdio with spaced paths and persists chat identity', async () => {
    const h = extensionHarness(platform);
    try {
      await new Promise(resolve => setImmediate(resolve));
      assert.equal(h.spawns.length,1); assert.equal(h.spawns[0].exe,h.executable);
      assert.equal(JSON.stringify(h.spawns[0].args),'["serve","--stdio"]');
      assert.equal(h.spawns[0].opts.shell,false); assert.equal(h.spawns[0].opts.windowsHide,true);
      assert.equal(h.spawns[0].opts.cwd,h.cwd);
      await h.provider().send('hello',false); await h.provider().send('again',false);
      assert.equal(h.requests.filter(r => r.method==='chats.create').length,1);
      const sends = h.requests.filter(r => r.method==='chats.send');
      assert.equal(sends.length,2); assert.equal(sends[1].params.chatId,'chat-1');
      await h.commands.get('praimate.openTerminal')();
      assert.equal(h.terminals[0].shellPath,platform === 'win32' ? 'C:\\CLI Tools\\praimate-code.exe' : '/CLI Tools/praimate-code');
      assert.equal(JSON.stringify(h.terminals[0].shellArgs),'[]');
      assert.deepEqual(h.errors,[]);
    } finally {h.close();}
  });
}
test('untrusted workspaces never spawn the backend', () => {
  const h = extensionHarness('win32',false);
  try{assert.equal(h.spawns.length,0);}finally{h.close();}
});
test('Workers command is registered and opens the Workers page', async () => {
  const h = extensionHarness('linux');
  try {
    await h.commands.get('praimate.focusWorkers')();
    assert.equal(h.provider().requestedPage,'workers');
    const manifest=JSON.parse(fs.readFileSync(path.join(__dirname,'package.json'),'utf8'));
    assert.ok(manifest.contributes.commands.some(command => command.command === 'praimate.focusWorkers'));
  } finally { h.close(); }
});
for (const platform of ['linux','win32']) {
  test(platform+': selected CLI and picker launch native terminals without changing chat settings',async () => {
    const cwd = platform === 'win32' ? 'C:\\Users\\Test User\\Project' : '/tmp/project with spaces';
    const h = extensionHarness(platform,true,{launchId:'terminal-test',config:{workspace:cwd,cli:'codex',model:'custom-model'}});
    try {
      await new Promise(resolve=>setImmediate(resolve));
      await h.commands.get('praimate.openTerminal')();
      assert.deepEqual(Array.from(h.terminals[0].shellArgs),['-m','custom-model']);
      assert.match(h.terminals[0].shellPath,/codex(?:\.exe)?$/);
      const before = h.requests.filter(r=>r.method==='session.update').length;
      for (const cli of ['claude','openclaude','opencode','praimate-code','copilot','antigravity']) {
        h.choose(cli); await h.commands.get('praimate.chooseTerminal')();
        const term = h.terminals.at(-1);
        assert.equal(term.name,'PrAImate · '+cli); assert.equal(term.cwd,cwd);
        assert.equal(term.shellArgs.length,0,'another CLI must not inherit Codex model');
        assert.equal(term.env.PRAIMATE_STUDIO_TOKEN,null);
      }
      assert.equal(h.requests.filter(r=>r.method==='session.update').length,before);
      h.choose(undefined); await h.commands.get('praimate.chooseTerminal')();
      assert.equal(h.terminals.length,7,'cancel must not launch');
      h.setAvailable([]); await h.commands.get('praimate.chooseTerminal')();
      assert.match(h.errors.at(-1),/No CLI executables/);
    } finally {h.close();}
  });
}
test('terminal commands fail closed in untrusted workspaces',async () => {
  const h=extensionHarness('win32',false);
  try {await h.commands.get('praimate.openTerminal')(); await h.commands.get('praimate.chooseTerminal')(); assert.equal(h.terminals.length,0); assert.equal(h.requests.length,0);}
  finally {h.close();}
});
test('Windows batch launch quotes spaced paths, disables AutoRun and rejects shell expansion',() => {
  const plan={cli:'claude',cwd:'C:\\Project Folder',command:'C:\\CLI Tools\\claude.cmd',args:['--model','model with spaces'],env:{PATH:'managed'}};
  const options=terminalOptions(plan,'win32',{SystemRoot:'C:\\Windows'});
  assert.equal(options.shellPath,'C:\\Windows\\System32\\cmd.exe');
  assert.equal(options.shellArgs,'/d /s /v:off /c ""C:\\CLI Tools\\claude.cmd" "--model" "model with spaces""');
  for(const value of ['a&whoami','%PATH%','!VAR!','a"b','a\nb','a|b','a>b','a^b']) {
    assert.throws(()=>terminalOptions({...plan,args:['--model',value]},'win32',{}),/cannot safely quote/);
  }
  const native=terminalOptions({...plan,command:'C:\\CLI Tools\\claude.exe',args:['--model','literal & value']},'win32',{});
  assert.deepEqual(native.shellArgs,['--model','literal & value']);
});
test('private launch descriptor overrides stale environment and preserves edits on reconnect', async () => {
  const descriptor = {backend:'/new backend/praimate',launchId:'first',config:{workspace:'/tmp/project with spaces',cli:'claude',model:'launch-model'}};
  const h = extensionHarness('linux',true,descriptor);
  try {
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(h.spawns[0].exe,descriptor.backend);
    assert.equal(h.requests.find(r => r.method==='session.update').params.model,'launch-model');
    h.stored.set('session',{cli:'claude',model:'user-edited',workspace:h.cwd});
    await h.commands.get('praimate.reconnect')();
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(h.requests.filter(r => r.method==='session.update').at(-1).params.model,'user-edited');
    h.changeDescriptor({...descriptor,backend:'/restarted/praimate',launchId:'second'});
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(h.spawns.at(-1).exe,'/restarted/praimate');
    assert.equal(h.requests.filter(r => r.method==='session.update').at(-1).params.model,'launch-model');
  } finally {h.close();}
});

function webviewHarness(withAssistant = false) {
  const html = fs.readFileSync(path.join(__dirname,'resources/chat.html'),'utf8');
  const script = /<script>([\s\S]*?)<\/script>/.exec(html)[1].replace('/*__ASSISTANT_SCRIPT__*/',withAssistant ? fs.readFileSync(path.join(__dirname,'resources/assistant.js'),'utf8') : '');
  const elements = new Map();
  const element = tag => ({tag,dataset:{},listeners:{},value:'',checked:true,disabled:false,children:[],innerHTML:'',textContent:'',className:'',
    style:{},append(...v){this.children.push(...v);},prepend(v){this.children.unshift(v);},classList:{add(){},remove(){}},get options(){return this.children;},
    focus(){},dispatchEvent(e){this.fire(e.type,e);},scrollHeight:0,scrollTop:0,clientHeight:0,setAttribute(name,value){this[name]=value;},
    addEventListener(name,fn){this.listeners[name]=fn;},appendChild(v){this.children.push(v);},replaceChildren(...v){this.children=v;this.innerHTML='';},
    fire(name,event={}){(this.listeners[name] || this['on'+name])?.(event);}});
  const document = {getElementById:id => {if(!elements.has(id)) elements.set(id,element());return elements.get(id);},createElement:element};
  const posted = [], listeners = {};
  const sandbox = {document,window:{addEventListener:(name,fn)=>(listeners[name] ||= []).push(fn),dispatchEvent:e=>(listeners[e.type] || []).forEach(fn=>fn(e))},Option:function(text,value){this.text=text;this.value=value;},
    acquireVsCodeApi:()=>({postMessage:m=>posted.push(m)}),navigator:{clipboard:{writeText:async()=>{}}},setInterval:()=>0,setTimeout:()=>0,clearTimeout:()=>{},Event:class{constructor(type){this.type=type;}}};
  vm.createContext(sandbox);vm.runInContext(script,sandbox);
  return {elements,posted,send:data=>(listeners.message || []).forEach(fn=>fn({data})),eval:code=>vm.runInContext(code,sandbox)};
}
const descendants = node => [node,...(node.children || []).flatMap(descendants)];
test('webview renders escaped prose and code using text nodes and clears old history', () => {
  const h = webviewHarness(); assert.equal(h.posted[0].type,'ready');
  h.send({type:'messages',messages:[{role:'assistant',content:'line one\nline two <img src=x>\n'+'```js\n<script>alert(1)</script>\n```'}]});
  const rendered = h.elements.get('messages').children[0];
  assert.match(rendered.children[1].innerHTML,/line one<br>line two &lt;img/);
  const box = rendered.children.find(e=>e.className==='code-box');
  assert.equal(box.children[1].tag,'pre'); assert.equal(box.children[1].textContent,'<script>alert(1)</script>\n');
  assert.equal(box.children[1].innerHTML,'');
  h.send({type:'messages',messages:[]});
  assert.equal(h.elements.get('messages').children.length,1);
  assert.equal(h.elements.get('messages').children[0].className,'welcome');
});
test('status updates preserve unsaved settings; errors preserve the draft and offer recovery', () => {
  const h = webviewHarness();
  const status = {connected:true,activeCLI:'claude',activeModel:'original',activeTools:'safe',availableCLIs:[{id:'claude',label:'Claude',available:true,capabilities:{toolLevels:['','ask']}}]};
  h.send({type:'status',status});
  h.elements.get('model-input').value='unsaved-model'; h.elements.get('session-form').fire('input');
  h.send({type:'status',status:{...status,models:['new-model']}});
  assert.equal(h.elements.get('model-input').value,'unsaved-model');
  h.elements.get('prompt-input').value='keep my prompt'; h.elements.get('send-btn').fire('click');
  const request = h.posted.at(-1); assert.equal(request.type,'send');
  h.send({type:'actionResult',id:request.requestId,ok:false,error:'backend disconnected'});
  assert.equal(h.elements.get('prompt-input').value,'keep my prompt');
  assert.equal(h.elements.get('notice').textContent,'backend disconnected');
  h.send({type:'status',status:{...status,connected:false,error:'Reopen Desktop'}});
  assert.equal(h.elements.get('connection').hidden,false); assert.equal(h.elements.get('send-btn').disabled,true);
  h.elements.get('reconnect-btn').fire('click'); assert.equal(h.posted.at(-1).type,'reconnect');
});
test('pending CLI detection preserves the persisted permission level on save', () => {
  const h = webviewHarness();
  h.send({type:'status',status:{connected:true,activeCLI:'codex',activeModel:'old',activeTools:'full',availableCLIs:[]}});
  assert.equal(h.elements.get('tools-select').value,'full');
  h.elements.get('model-input').value='new'; h.elements.get('session-form').fire('input');
  h.elements.get('session-form').fire('submit',{preventDefault(){}});
  const request = h.posted.at(-1);
  assert.equal(request.type,'updateConfig'); assert.equal(request.config.tools,'full');
});
test('native chat settings expose and save the context window', () => {
  const h = webviewHarness();
  h.send({type:'status',status:{connected:true,activeCLI:'praimate-cli',activeTools:'safe',localContextTokens:32768,localOutputTokens:2048,availableCLIs:[{id:'praimate-cli',label:'PrAImate CLI',available:true}]}});
  assert.equal(h.elements.get('native-budget').hidden,false);
  assert.equal(h.elements.get('local-context-tokens').value,32768);
  h.elements.get('local-context-tokens').value='65536';
  h.elements.get('session-form').fire('submit',{preventDefault(){}});
  const request=h.posted.at(-1);
  assert.equal(request.type,'updateConfig');
  assert.equal(request.config.localContextTokens,65536);
  assert.equal(request.config.localOutputTokens,2048);
});
test('Desktop window button reflects Core state and requests a toggle', () => {
  const h = webviewHarness();
  h.send({type:'status',status:{connected:true,activeCLI:'claude',activeTools:'safe',desktopWindow:{available:true,hidden:false}}});
  const button = h.elements.get('core-window-btn');
  assert.equal(button.hidden,false); assert.equal(button.textContent,'Hide PrAImate');
  button.fire('click'); assert.equal(h.posted.at(-1).type,'toggleDesktopWindow');
  h.send({type:'status',status:{connected:true,activeCLI:'claude',activeTools:'safe',desktopWindow:{available:true,hidden:true}}});
  assert.equal(button.textContent,'Show PrAImate');
  h.send({type:'status',status:{connected:false,desktopWindow:{available:true,hidden:true}}});
  assert.equal(button.hidden,true);
});
test('single navigation loads collections and exposes contextual actions', () => {
  const h = webviewHarness(); h.send({type:'status',status:{connected:true,activeCLI:'claude'}});
  h.eval("navigate('history')"); assert.equal(h.posted.at(-1).name,'history');
  h.send({type:'collection',name:'history',items:[{id:'chat-42',title:'Persisted chat',cli:'claude'}]});
  const actions = h.elements.get('catalog-items').children[0].children.at(-1).children;
  assert.deepEqual(actions.map(a=>a.textContent),['Resume','Rename','Delete']);
  actions[0].fire('click'); assert.equal(h.posted.at(-1).id,'chat-42');
  const req=h.posted.at(-1); h.send({type:'actionResult',id:req.requestId,ok:true});
  assert.equal(h.elements.get('chat-page').hidden,false);
  const manifest=JSON.parse(fs.readFileSync(path.join(__dirname,'package.json'),'utf8'));
  assert.equal(manifest.contributes.views['praimate-sidebar'].length,1);
});

test('Workers view keeps separate models for the same CLI and renders live events as text', () => {
  const h = webviewHarness();
  h.send({type:'status',status:{connected:true,activeWorkspace:'/project',availableCLIs:[{id:'codex',label:'Codex',available:true}]}});
  h.eval("navigate('workers')");
  assert.equal(h.posted.at(-1).type,'workerLoad');
  const profiles=['primary','middle','fast'].map((tier,index) => ({tier,runtime:'cli',cli:'codex',model:['astra','medium','small'][index],timeoutSeconds:120,maxInputBytes:65536,maxOutputTokens:0}));
  h.send({type:'workerState',config:{workspace:'/project',profiles},runs:[]});
  const sections=h.elements.get('worker-profiles').children;
  assert.equal(sections.filter(section=>section.className!=='worker-policy').length,3);
  const policy=sections.find(section=>section.className==='worker-policy');
  assert.ok(policy);
  descendants(policy).find(e=>e.tag==='select').value='full';
  descendants(policy).find(e=>e.tag==='select').fire('change');
  const controls=descendants(h.elements.get('worker-profiles').children[0]).filter(e=>e.tag==='input');
  controls[0].value='3';controls[0].fire('input');
  controls[1].value='5';controls[1].fire('input');
  h.elements.get('worker-new').fire('click');
  assert.equal(h.elements.get('worker-create').hidden,false);
  h.eval("$('worker-task').value='Fix a bug'");
  h.elements.get('worker-start').fire('click');
  const request=h.posted.at(-1);
  assert.equal(request.type,'workerStart');
  assert.deepEqual(request.config.profiles.map(p=>p.model),['astra','medium','small']);
  assert.equal(request.config.accessMode,'full');
  assert.equal(request.config.maxRetries,3);
  assert.equal(request.config.retryDelaySeconds,5);
  h.send({type:'workerStarted',id:'run-1'});
  assert.equal(h.elements.get('worker-create').hidden,true);
  h.send({type:'workerSnapshot',runs:[{id:'run-1',task:'Fix a bug',status:'running'}],snapshot:{id:'run-1',task:'Fix a bug',status:'running',profiles,events:[{tier:'fast',kind:'stream',text:'<script>unsafe</script>',timestamp:'2026-01-01T00:00:00Z'}]}});
  const output=descendants(h.elements.get('worker-lanes')).find(e=>e.tag==='pre' && e.textContent==='<script>unsafe</script>');
  assert.ok(output);assert.equal(output.innerHTML,'');
  assert.deepEqual(descendants(h.elements.get('worker-lanes')).filter(e=>e.tag==='h2').map(e=>e.textContent),['Needs you · 0','Working · 1','Done · 0','Queued · 0']);
});

test('worker console displays other CLI lifecycle, reasoning and provider errors for the selected attempt', () => {
  const h=webviewHarness();
  h.send({type:'status',status:{connected:true,activeWorkspace:'/project'}});
  h.send({type:'navigate',page:'workers',workerID:'run'});
  const profiles=[{tier:'primary',cli:'codex',model:'reasoner'},{tier:'middle',cli:'praimate-code',model:'router/medium'},{tier:'fast',cli:'openclaude',model:'small'}];
  const events=['backend_status','reasoning','error'].map((kind,index)=>({workerID:'middle-call',tier:'middle',cli:'praimate-code',model:'router/medium',kind,text:['praimate-code process started','Reported model activity','Provider unavailable (ref: err_fixture)'][index],timestamp:'2026-10-04T10:00:00Z'}));
  h.send({type:'workerSnapshot',selectedId:'run',runs:[{id:'run',status:'failed'}],snapshot:{id:'run',status:'failed',profiles,events}});
  const items=descendants(h.elements.get('worker-lanes')).filter(e=>e.className==='worker-event');
  assert.equal(items.length,events.length);
  for(let index=0;index<events.length;index++) {
    const item=items[index];
    assert.match(item.children[0].textContent,/praimate-code \/ router\/medium/);
    assert.equal(item.children[1].textContent,events[index].text);
  }
});

test('worker execution panels retain their selected run and settings save against that run', () => {
  const h=webviewHarness();
  h.send({type:'status',status:{connected:true,activeWorkspace:'/project',availableCLIs:[{id:'codex',label:'Codex',available:true}]}});
  h.send({type:'navigate',page:'workers',workerID:'saved-run'});
  assert.equal(h.eval('workerSelected'),'saved-run');
  const profiles=['primary','middle','fast'].map(tier=>({tier,runtime:'cli',cli:'codex',model:'original-'+tier,timeoutSeconds:120,maxInputBytes:65536,maxOutputTokens:0}));
  h.send({type:'workerSnapshot',selectedId:'saved-run',runs:[{id:'saved-run',status:'failed'}],snapshot:{id:'saved-run',title:'saved',workspace:'/project',status:'failed',profiles,events:[],dag:{tasks:[]}}});
  const header=h.elements.get('worker-result').children[0];
  header.children.find(child=>child.textContent==='Open execution panel').fire('click');
  assert.equal(h.posted.at(-1).type,'workerOpenExecution'); assert.equal(h.posted.at(-1).id,'saved-run');
  header.children.find(child=>child.textContent==='Run settings').fire('click');
  h.eval('workerConfig.profiles[0].timeoutSeconds=0');
  h.elements.get('worker-start').fire('click');
  assert.equal(h.posted.at(-1).type,'workerRunConfig'); assert.equal(h.posted.at(-1).id,'saved-run');
  assert.equal(h.posted.at(-1).config.profiles[0].timeoutSeconds,0);
});

test('planning timeout recovery saves only the reasoner timeout before retrying the original run', () => {
  const h=webviewHarness();
  h.send({type:'status',status:{connected:true,activeWorkspace:'/project'}});
  h.send({type:'navigate',page:'workers',workerID:'timed-out'});
  const profiles=['primary','middle','fast'].map(tier=>({tier,runtime:'cli',cli:'codex',model:tier,timeoutSeconds:120,maxInputBytes:65536,maxOutputTokens:0}));
  h.send({type:'workerSnapshot',selectedId:'timed-out',runs:[{id:'timed-out',status:'failed'}],snapshot:{id:'timed-out',workspace:'/project',status:'failed',error:'context deadline exceeded',profiles,events:[],dag:{tasks:[]}}});
  h.elements.get('worker-result').children.find(child=>child.textContent==='Retry with no reasoner timeout').fire('click');
  const save=h.posted.at(-1);
  assert.equal(save.type,'workerRunConfig');assert.equal(save.id,'timed-out');
  assert.deepEqual(save.config.profiles.map(p=>p.timeoutSeconds),[0,120,120]);
  assert.equal(profiles[0].timeoutSeconds,120);
  h.send({type:'actionResult',id:save.requestId,ok:true});
  assert.equal(h.posted.at(-1).type,'workerPlanRetry');assert.equal(h.posted.at(-1).id,'timed-out');
});

test('parallel worker drafts keep independent backend overrides and display Git diffs safely', () => {
  const h=webviewHarness();
  const profiles=['primary','middle','fast'].map(tier=>({tier,runtime:'cli',cli:'codex',model:'default-'+tier}));
  h.send({type:'status',status:{connected:true,availableCLIs:[{id:'codex',label:'Codex',available:true},{id:'claude',label:'Claude',available:true}]}});
  h.eval("navigate('workers')");
  const tasks=[{id:'a',description:'<img src=x>',dependencies:[],worker:{profile:'middle',cli:'claude',model:'sonnet'},status:'pending'}];
  const snapshot={id:'dag-run',task:'parallel objective',status:'draft',updatedAt:'one',profiles,events:[],dag:{tasks,maxParallel:2,targetBranch:'main',baseCommit:'1234567890abcdef'}};
  h.send({type:'workerSnapshot',runs:[{id:'dag-run',status:'draft'}],snapshot});
  const graph=descendants(h.elements.get('worker-result')).find(e=>e.className==='worker-graph');
  assert.ok(graph);
  graph.children.find(e=>e.tag==='button' && e.textContent==='Run task graph').fire('click');
  const saved=h.posted.at(-1);
  assert.equal(saved.type,'workerGraphSave');assert.equal(saved.tasks[0].worker.cli,'claude');assert.equal(saved.tasks[0].worker.model,'sonnet');
  h.send({type:'actionResult',id:saved.requestId,ok:true});assert.equal(h.posted.at(-1).type,'workerGraphExecute');
  const completed={...tasks[0],status:'completed',review:'pending',result:{baseCommit:'basecommit',resultCommit:'resultcommit',changedFiles:['file.js'],diff:'<script>malicious</script>'}};
  h.send({type:'workerSnapshot',runs:[{id:'dag-run',status:'review'}],snapshot:{...snapshot,status:'review',updatedAt:'two',dag:{...snapshot.dag,tasks:[completed]}}});
  const rendered=descendants(h.elements.get('worker-result')).find(e=>e.className==='worker-graph');
  const card=rendered.children.find(e=>e.tag==='article');
  const diff=card.children.find(e=>e.tag==='details' && e.children[0].textContent==='Git diff').children[1];
  assert.equal(diff.textContent,'<script>malicious</script>');assert.equal(diff.innerHTML,'');
});

test('completed worker review waits in Done while the run is still executing', () => {
  const h=webviewHarness();
  const profiles=['primary','middle','fast'].map(tier=>({tier,runtime:'cli',cli:'codex',model:'default-'+tier}));
  h.send({type:'status',status:{connected:true,availableCLIs:[{id:'codex',label:'Codex',available:true}]}});
  h.eval("navigate('workers')");
  const completed={id:'a',description:'A',dependencies:[],worker:{profile:'middle'},status:'completed',review:'pending',result:{baseCommit:'base',resultCommit:'result',changedFiles:[],diff:''}};
  const running={id:'b',description:'B',dependencies:[],worker:{profile:'fast'},status:'running'};
  const snapshot={id:'run',task:'objective',status:'running',updatedAt:'one',profiles,events:[],dag:{tasks:[completed,running],maxParallel:2,targetBranch:'main',baseCommit:'abc'}};
  const laneTitles=()=>descendants(h.elements.get('worker-lanes')).filter(e=>e.tag==='h2').map(e=>e.textContent);
  h.send({type:'workerSnapshot',runs:[{id:'run',status:'running'}],snapshot});
  assert.deepEqual(laneTitles(),['Needs you · 0','Working · 1','Done · 1','Queued · 0']);
  assert.ok(descendants(h.elements.get('worker-lanes')).some(e=>e.textContent.includes('Completed · review when execution ends')));
  h.send({type:'workerSnapshot',runs:[{id:'run',status:'review'}],snapshot:{...snapshot,status:'review',updatedAt:'two',dag:{...snapshot.dag,tasks:[completed]}}});
  assert.deepEqual(laneTitles(),['Needs you · 1','Working · 0','Done · 0','Queued · 0']);
  assert.ok(descendants(h.elements.get('worker-lanes')).some(e=>e.textContent.includes('Ready to review')));
});

test('failed worker tasks show next and previous routes, with an explicit profile reset', () => {
  const h=webviewHarness();
  const profiles=['primary','middle','fast'].map(tier=>({tier,runtime:'cli',cli:'codex',model:'replacement-'+tier}));
  h.send({type:'status',status:{connected:true}});h.eval("navigate('workers')");
  const failed={id:'retry',description:'Finish the task',dependencies:[],status:'failed',requestedWorker:{profile:'middle'},worker:{profile:'middle',runtime:'cli',cli:'praimate-code',model:'agy/broken',provider:'agy'}};
  const pending={id:'pending',description:'Wait',dependencies:['retry'],status:'pending',worker:{profile:'fast'}};
  const snapshot={id:'retry-run',task:'Retry objective',status:'failed',updatedAt:'one',profiles,events:[],dag:{tasks:[failed,pending],maxParallel:1,targetBranch:'main',baseCommit:'abcdef'}};
  const render=run=>h.send({type:'workerSnapshot',runs:[{id:run.id,status:run.status}],snapshot:run});
  const graph=()=>descendants(h.elements.get('worker-result')).find(e=>e.className==='worker-graph');
  const card=()=>graph().children.find(e=>e.tag==='article');
  render(snapshot);
  assert.ok(card().children.some(e=>e.textContent==='Next attempt: codex / replacement-middle'));
  assert.ok(card().children.some(e=>e.textContent==='Last attempt: praimate-code / agy/broken'));
  assert.ok(!card().children.some(e=>e.textContent==='Use current profile'));
  assert.equal(graph().children.find(e=>e.textContent==='Resume pending tasks').disabled,false);
  card().children.find(e=>e.textContent==='Continue with existing changes').fire('click');
  assert.equal(h.posted.at(-1).type,'workerGraphRetry');assert.equal(h.posted.at(-1).taskID,'retry');
  const pinned={...failed,requestedWorker:{profile:'middle',cli:'praimate-code',model:'agy/broken'}};
  render({...snapshot,updatedAt:'two',dag:{...snapshot.dag,tasks:[pinned,pending]}});
  assert.ok(card().children.some(e=>e.textContent==='Next attempt: praimate-code / agy/broken'));
  card().children.find(e=>e.textContent==='Use current profile').fire('click');
  assert.equal(h.posted.at(-1).type,'workerGraphProfile');assert.equal(h.posted.at(-1).id,'retry-run');assert.equal(h.posted.at(-1).taskID,'retry');
  render({...snapshot,updatedAt:'three',dag:{...snapshot.dag,tasks:[{...failed,status:'pending'},pending]}});
  assert.equal(graph().children.find(e=>e.textContent==='Resume pending tasks').disabled,false);
});

test('late action errors stay on the originating page and conversation', () => {
  const h = webviewHarness();
  h.send({type:'status',status:{connected:true,activeCLI:'claude',chatId:'first'}});
  h.eval("post('send',{text:'test'})"); const first=h.posted.at(-1);
  h.eval("navigate('overview')");
  h.send({type:'actionResult',id:first.requestId,ok:false,error:'old chat failure'});
  assert.equal(h.elements.get('notice').hidden,true);
  h.eval("navigate('chat'); post('send',{text:'test'})"); const second=h.posted.at(-1);
  h.send({type:'status',status:{connected:true,activeCLI:'claude',chatId:'second'}});
  h.send({type:'actionResult',id:second.requestId,ok:false,error:'first conversation failure'});
  assert.equal(h.elements.get('notice').hidden,true);
});

test('overview requests the selected month and renders usage labels as text', () => {
  const h=webviewHarness();
  h.send({type:'status',status:{connected:true,activeCLI:'claude'}});
  h.elements.get('usage-month').value='2026-09';h.eval("navigate('overview')");
  assert.equal(h.posted.at(-1).type,'usageDashboard');
  assert.equal(h.posted.at(-1).month,'2026-09');
  h.send({type:'usageDashboard',dashboard:{month:'2026-09',totals:{runs:1,reportedRuns:1,inputTokens:12,outputTokens:8,tokens:20,activeDays:1},clis:[{name:'<private-model>',runs:1}],models:[],months:[]}});
  const root=h.elements.get('usage-content');
  assert.equal(root.children[0].children[0].children[1].textContent,'20');
  assert.equal(root.children[1].children[0].children[1].children[0].textContent,'<private-model>');
  assert.equal(root.children[1].children[0].children[1].children[0].innerHTML,'');
});

 test('terminal closure and launch failure release usage receivers', async () => {
  const h=extensionHarness('linux');
  try {
    await new Promise(resolve=>setImmediate(resolve));
    const term=await h.commands.get('praimate.openTerminal')();
    h.closeTerminal(term);
    h.closeTerminal(term);
    await new Promise(resolve=>setImmediate(resolve));
    assert.equal(h.requests.filter(r=>r.method==='terminals.closed').length,1);
    assert.match(h.requests.find(r=>r.method==='terminals.closed').params.usageId,/^usage-/);
    h.failTerminal('cannot create terminal');
    await h.commands.get('praimate.openTerminal')();
    assert.equal(h.requests.filter(r=>r.method==='terminals.closed').length,2);
    assert.match(h.errors.at(-1),/cannot create terminal/);
  } finally {h.close();}
});


test('Application Assistant webview shares config, escapes replies and keeps errors scoped', async () => {
  const h=webviewHarness(true);
  h.eval("status.connected=true; navigate('assistant')");
  const reply=async(method,result)=>{const req=h.posted.findLast(m=>m.type==='assistantRPC'&&m.method===method);assert.ok(req,method);h.send({type:'assistantReply',id:req.requestId,result});await new Promise(r=>setImmediate(r));};
  await reply('assistant.available',true);
  await reply('assistant.config',{enabled:true,model_id:'existing',endpoint:'http://localhost:8080',model:'fixture',context:2048,output:512,permissions:{read:'allow'},voice:{enabled:false,model_id:'whisper-base'}});
  await reply('assistant.snapshot',{messages:[{role:'assistant',text:'<img src=x onerror=alert(1)>'}],task:{status:'completed',steps:[]}});
  const messages=h.elements.get('assistant-messages');
  assert.equal(messages.children[0].children[1].textContent,'<img src=x onerror=alert(1)>');
  assert.equal(messages.children[0].children[1].innerHTML,'');
  h.elements.get('assistant-prompt').value='Find my worker';
  h.elements.get('assistant-send').fire('click');
  const send=h.posted.findLast(m=>m.method==='assistant.send');assert.equal(send.params.message,'Find my worker');
  h.send({type:'assistantReply',id:send.requestId,error:'fixture failure'});
  await new Promise(r=>setImmediate(r));
  await reply('assistant.snapshot',{messages:[],task:{status:'failed',steps:[]}});
  assert.equal(h.elements.get('assistant-error').textContent,'fixture failure');
  h.eval("navigate('overview')");assert.equal(h.elements.get('assistant-page').hidden,true);
});

test('Application Assistant command has its own section',async()=>{
 const h=extensionHarness('linux');try{await h.commands.get('praimate.openAssistant')();assert.equal(h.provider().requestedPage,'assistant');}finally{h.close();}
});


test('worker console retains durable attempts and loads paginated activity without cross-run leakage', () => {
  const h=webviewHarness();h.send({type:'status',status:{connected:true}});
  const profiles=[{tier:'middle',cli:'codex',model:'new-model'}];
  const attempts=[{id:'old',taskID:'a',tier:'middle',cli:'praimate-code',model:'old-model',status:'failed',output:'partial'},{id:'new',taskID:'a',tier:'middle',cli:'codex',model:'new-model',status:'completed',output:'done'}];
  const run={id:'run',status:'review',updatedAt:'one',profiles,attempts,activityVersion:1,usage:{input:23,output:7,calls:2},events:[],dag:{targetBranch:'main',baseCommit:'abc',tasks:[{id:'a',description:'Fix it',status:'completed',worker:{profile:'middle'}}]}};
  h.send({type:'workerSnapshot',runs:[run],snapshot:run});
  assert.equal(h.elements.get('worker-followup-panel').hidden,true);
  assert.equal(h.posted.at(-1).type,'workerActivity');assert.equal(h.posted.at(-1).workerID,'new');
  const event=(sequence,text)=>({sequence,workerID:'new',kind:'tool',text,timestamp:'2026-10-07T12:00:00Z'});
  h.send({type:'workerActivity',runId:'run',workerID:'new',activity:{events:[event(2,'latest')],before:20,hasMore:true}});
  const root=h.elements.get('worker-lanes');descendants(root).find(e=>e.textContent==='Load earlier activity').fire('click');
  assert.equal(h.posted.at(-1).before,20);
  h.send({type:'workerActivity',runId:'run',workerID:'new',activity:{events:[event(1,'earlier'),event(2,'latest')],before:10,hasMore:false}});
  assert.deepEqual(descendants(root).filter(e=>e.tag==='pre').map(e=>e.textContent),['earlier','latest']);
  const select=descendants(root).find(e=>e['aria-label']==='Execution attempt');select.value='old';select.fire('change');
  assert.equal(h.posted.at(-1).workerID,'old');
  h.send({type:'workerActivity',runId:'run',workerID:'new',activity:{events:[event(3,'wrong attempt')],hasMore:false}});
  assert.ok(!descendants(root).some(e=>e.textContent==='wrong attempt'));
  const chat={...run,id:'chat',dag:null,status:'completed'};
  h.send({type:'workerSnapshot',runs:[chat],snapshot:chat});
  assert.equal(h.elements.get('worker-followup-panel').hidden,false);
  h.send({type:'workerSnapshot',runs:[],snapshot:null});
  assert.equal(h.elements.get('worker-followup-panel').hidden,true);
  h.send({type:'workerActivity',runId:'run',workerID:'old',activity:{events:[event(4,'wrong run')],hasMore:false}});
  assert.ok(!descendants(root).some(e=>e.textContent==='wrong run'));
});


test('worker recovery bridge preserves task identity and only executes after retry succeeds',async()=>{
  const h=extensionHarness('linux',true,null,req=>{
    if(req.method==='workers.activity')return {events:[],before:10,hasMore:true};
    if(req.method==='workers.task.preview')return {path:'/task',diff:'changes'};
  });
  try {
    await new Promise(resolve=>setImmediate(resolve));
    h.vscode.Uri={joinPath:(_, ...parts)=>parts.join('/')};
    let receive;const messages=[];
    const view={webview:{asWebviewUri:v=>v,cspSource:'test:',postMessage:m=>messages.push(m),onDidReceiveMessage:fn=>{receive=fn;}},onDidDispose(){}};
    h.provider().resolveWebviewView(view);
    receive({type:'workerGraphRetry',id:'run',taskID:'failed',requestId:1});
    await new Promise(resolve=>setImmediate(resolve));
    assert.deepEqual(h.requests.filter(r=>r.method.startsWith('workers.graph')).map(r=>r.method),['workers.graph.retry','workers.graph.execute']);
    assert.equal(h.requests.find(r=>r.method==='workers.graph.retry').params.taskID,'failed');
    receive({type:'workerGraphResolve',id:'run',taskID:'conflict',requestId:4});
    await new Promise(resolve=>setImmediate(resolve));
    assert.equal(h.requests.find(r=>r.method==='workers.graph.resolve').params.taskID,'conflict');
    assert.deepEqual(h.requests.filter(r=>r.method.startsWith('workers.graph')).slice(-2).map(r=>r.method),['workers.graph.resolve','workers.graph.execute']);
    receive({type:'workerActivity',id:'run',workerID:'attempt',before:20,requestId:2});
    receive({type:'workerTaskPreview',id:'run',taskID:'failed',requestId:3});
    await new Promise(resolve=>setImmediate(resolve));
    assert.equal(h.requests.find(r=>r.method==='workers.activity').params.before,20);
    assert.ok(messages.some(m=>m.type==='workerActivity' && m.runId==='run' && m.workerID==='attempt' && m.activity.before===10));
    assert.ok(messages.some(m=>m.type==='workerTaskPreview' && m.taskID==='failed' && m.preview.path==='/task'));
  } finally {h.close();}
});
