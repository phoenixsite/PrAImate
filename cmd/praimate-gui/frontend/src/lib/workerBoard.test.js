import test from 'node:test'
import assert from 'node:assert/strict'
import { workerBoardItems, workerStatusLabel, mergeWorkerEvents } from './workerBoard.js'
import { workerExecutions, workerUsage } from './workerActivity.js'

test('attempts and usage remain visible after the event buffer is empty', () => {
  const run = { activityVersion: 1, events: [], usage: {input:23,output:7,calls:1}, attempts:[{id:'old',status:'failed',taskID:'a',model:'old-model'},{id:'new',status:'completed',taskID:'a',model:'new-model'}], profiles:[{tier:'middle',cli:'codex',model:'new-model'}], dag:{tasks:[{id:'a',status:'completed',worker:{profile:'middle'},description:'A'}]} }
  assert.equal(workerExecutions(run).length,2)
  assert.equal(workerUsage(run).input,23)
  const cards=workerBoardItems(run)
  assert.equal(cards[0].attemptID,'new')
  assert.equal(cards[0].attempts,2)
  assert.equal(cards[0].column,'done')
})

test('blocked tasks show their dependency and current route before an attempt exists', () => {
  const run={profiles:[{tier:'fast',cli:'claude',model:'small'}],dag:{tasks:[{id:'b',status:'blocked',worker:{profile:'fast'},dependencies:['a']}]} }
  const [card]=workerBoardItems(run)
  assert.equal(card.column,'attention')
  assert.deepEqual(card.dependencies,['a'])
  assert.equal(card.route.cli,'claude')
  assert.equal(card.attemptID,'')
})

test('journal pages merge with live updates by sequence without duplicate tool effects', () => {
  const events=mergeWorkerEvents([{sequence:1,kind:'tool',text:'done'},{sequence:2,kind:'stream',text:'hel'}],[{sequence:2,kind:'stream',text:'hello'},{sequence:3,kind:'completed'}])
  assert.equal(events.length,3)
  assert.equal(events[1].text,'hello')
})

test('completed changes awaiting review stay in Needs you once execution ends', () => {
  const [card]=workerBoardItems({status:'review',dag:{tasks:[{id:'review',status:'completed',review:'pending'}]}})
  assert.equal(card.column,'attention')
  assert.equal(workerStatusLabel(card),'Ready to review')
})

test('completed review waits in Done while the task graph is still running', () => {
  const [card]=workerBoardItems({status:'running',dag:{tasks:[{id:'review',status:'completed',review:'pending'}]}})
  assert.equal(card.column,'done')
  assert.equal(card.reviewLabel,'Completed · review when execution ends')
  assert.equal(workerStatusLabel(card),'Completed · review when execution ends')
})

test('retrying tasks stay in Working and old planning attempts do not clutter the task board', () => {
 const run={profiles:[],attempts:[{id:'plan',tier:'primary',phase:'completed',status:'completed'},{id:'failed',taskID:'fix',tier:'middle',status:'failed',cli:'codex',model:'model'}],dag:{tasks:[{id:'fix',description:'Fix navigation',status:'retrying',autoRetriesUsed:1,nextRetryAt:'2026-10-08T10:00:00Z',worker:{profile:'middle'},error:'temporary provider error'}]}};
 const items=workerBoardItems(run);
 assert.equal(items.length,1);
 assert.equal(items[0].column,'working');
 assert.equal(items[0].preview,'Fix navigation');
 assert.equal(items[0].autoRetriesUsed,1);
});
