import '../../src/styles.css'
import WorkerGraph from '../../src/lib/WorkerGraph.svelte'
import WorkerMonitor from '../../src/lib/WorkerMonitor.svelte'
import Documents from '../../src/pages/Documents.svelte'
import Studio from '../../src/pages/Studio.svelte'

const calls=[]
const data={ListChats:[],ListAgents:[],ListCLIs:[{id:'codex',available:true}],LocalLLMModels:{configured:false},ListCLIModels:[],MCPServers:[{id:'docs-id',name:'Documentation endpoint',enabled:true},{id:'disabled',name:'Disabled endpoint',enabled:false}],PreflightExecution:{ok:true,issues:[]},PrepareStudioChat:'created-chat',PickFolder:'/project'}
Object.assign(data,{StudioGetStatus:{state:'installed',version:'1.3.6',binaryPath:'/editor/codium'},StudioListRecentProjects:['/project'],StudioGetEditorPreferences:{extensionsDirectory:'',managedDirectory:'/managed/extensions'}})
window.go={main:{App:new Proxy({},{get(_,method){return async(...args)=>{calls.push({method,args});if(method==='StudioSaveEditorPreferences'){data.StudioGetEditorPreferences.extensionsDirectory=args[0];return structuredClone(data.StudioGetEditorPreferences)}return method in data?structuredClone(data[method]):[]}}})}}
window.runtime={EventsOn:()=>()=>{},EventsOff(){}}
const snapshot={id:'review-fixture',task:'Review a saved task graph',status:'draft',profiles:[{tier:'middle',runtime:'cli',cli:'codex'}],events:[],dag:{targetBranch:'main',baseCommit:'abc',maxParallel:2,tasks:[{id:'one',description:'Apply the requested change',worker:{profile:'middle'},dependencies:[],status:'pending'}]}}
let component
window.issueReview={calls,show(mode,customSnapshot=snapshot){component?.$destroy();document.getElementById('app').replaceChildren();component=new ({graph:WorkerGraph,monitor:WorkerMonitor,documents:Documents,studio:Studio}[mode])({target:document.getElementById('app'),props:['documents','studio'].includes(mode)?{}:{snapshot:customSnapshot,clis:data.ListCLIs}})}}
window.issueReview.show('graph')
