import test from 'node:test';
import assert from 'node:assert/strict';
import { loadApp, settle } from '../lib/load-app.mjs';

test('revision history diffs and restores workflows and imports MCP JSON', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  const revision = {
    id: 'rev-1', version: 1, revision: 2, summary: 'Before review',
    createdAt: '2026-10-20T10:00:00Z',
    payload: JSON.stringify({ ...app.evaluate(`structuredClone(state.workflow)`), name: 'Previous flow' }),
  };
  app.router.get(`/api/workflows/${app.evaluate(`state.workflow.id`)}/revisions`, [revision]);
  app.router.post(new RegExp('/api/workflows/.+/revisions/rev-1/restore'), {
    ...app.evaluate(`structuredClone(state.workflow)`), name: 'Previous flow',
  });
  await app.evaluate(`openRevisionHistory('workflow',state.workflow.id,state.workflow.name)`);
  app.document.querySelector('[data-rev="rev-1"]').click();
  assert.match(app.document.querySelector('#revisionDiff').textContent, /Previous flow/);
  const restore = app.evaluate(`restoreSelectedRevision()`);
  await settle();
  app.document.querySelector('#confirmOkBtn').click();
  await restore;
  assert.equal(app.evaluate(`state.workflow.name`), 'Previous flow');
  app.evaluate(`closeRevisionHistory()`);

  app.router.post('/api/mcp-servers/import', {
    imported: 1, servers: [{ id: 'mcp-new', name: 'Imported MCP', projectId: 'project-1' }],
  });
  app.evaluate(`state.data.mcpServers=[]`);
  await app.evaluate(`importMcpJsonFile({target:{files:[{text:async()=>'{\"mcpServers\":{}}'}],value:'x'}})`);
  assert.ok(app.router.calls.some((call) => call.path === '/api/mcp-servers/import'));
});

test('flow history merges edits and supports undo, redo, clear, and workflow rebinding', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  const result = app.evaluate(`
    (() => {
      state.workflow={id:'history-a',nodes:[],edges:[]};flowHistory.clear();
      flowHistory.record({merge:'typing'});state.workflow.nodes.push({id:'a',position:{x:0,y:0}});
      flowHistory.record({merge:'typing'});state.workflow.nodes[0].name='A';
      const canUndo=flowHistory.canUndo(),undone=flowHistory.undo(),empty=state.workflow.nodes.length;
      const redone=flowHistory.redo(),restored=state.workflow.nodes.length;
      flowHistory.clear();
      state.workflow={id:'history-b',nodes:[],edges:[],preview:{enabled:true}};
      return {canUndo,undone,empty,redone,restored,afterClear:flowHistory.canUndo(),version:flowHistory.current()};
    })()
  `);
  assert.deepEqual({ canUndo: result.canUndo, undone: result.undone, empty: result.empty }, { canUndo: true, undone: true, empty: 0 });
  assert.equal(result.restored, 1);
  assert.equal(result.afterClear, false);
});

test('execution center removes local bulk selection entries', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  const issues = [
    {id:'bulk-1',title:'Work item one',projectId:'project-1',status:'backlog',source:'feishu_bitable',externalRef:{source:'feishu_bitable',externalId:'rec-1',externalState:'开发中'}},
    {id:'bulk-2',title:'Work item two',projectId:'project-1',status:'todo',source:'feishu_bitable',externalRef:{source:'feishu_bitable',externalId:'rec-2',externalState:'待排期'}}
  ];
  app.router.get('/api/issues?formal=1', issues);
  app.evaluate(`
    state.data.issues=${JSON.stringify(issues)};
    state.issueSelection=new Set();state.issueSelectionMode=true;
    switchView('executions');
  `);
  await settle(4);
  assert.equal(app.document.querySelector('#startIssueSelection'), null, '批量操作入口已移除');
  assert.equal(app.document.querySelector('#issueBulkActions'), null);
  assert.equal(app.document.querySelectorAll('[data-issue-select]').length, 0);
  assert.equal(app.document.querySelectorAll('[data-issue-select-page]').length, 0);
  assert.equal(app.document.querySelectorAll('.kanban-card').length, 0, '执行中心不提供本地拖拽看板');
  assert.match(app.document.querySelector('#issueBoard').textContent, /Work item one/);
  assert.equal(app.router.calls.some((c) => c.method === 'DELETE' || (c.method === 'PUT' && c.path === '/api/issues')), false);
});

test('notification editors load bots, add bindings, choose presets, insert variables, preview, and remove', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  app.router.get('/api/notification-bots?projectId=project-1', [{
    id: 'bot-1', name: 'Delivery Bot', enabled: true, target: { type: 'group', name: 'Dev group' },
  }]);
  app.router.post('/api/notification-preview', { content: '### Preview\nGenerated successfully' });
  const host = app.document.createElement('div');
  app.document.body.append(host);
  const bindings = [];
  app.window.__bindings = bindings;
  app.window.__notifyHost = host;
  app.evaluate(`mountNotificationEditor(__notifyHost,__bindings,{projectId:'project-1',sourceType:'run',kind:'workflow'},()=>{})`);
  await settle(8);
  host.querySelector('[data-notification-add]').click();
  await settle(2);
  host.querySelector('[data-notification-preset="complete"]').click();
  await settle(2);
  host.querySelector('[data-notification-variable="project"]')?.click();
  const mode = host.querySelector('[data-notification-mode]');
  mode.value = 'ai';
  mode.dispatchEvent(new app.window.Event('change', { bubbles: true }));
  await settle(2);
  host.querySelector('[data-notification-remove]').click();
  assert.equal(bindings.length, 0);

  const sopBindings = [{ botId: 'bot-1', events: ['succeeded'], scope: 'run', contentMode: 'ai', template: '' }];
  app.window.__sopBindings = sopBindings;
  app.evaluate(`mountSOPNotificationEditor(__notifyHost,__sopBindings,{projectId:'project-1'},()=>{})`);
  const details = host.querySelector('[data-sop-notification-custom]');
  details.open = true;
  details.dispatchEvent(new app.window.Event('toggle'));
  host.querySelector('[data-sop-notification-recommended]').click();
  host.querySelector('[data-sop-notification-field="project"]')?.click();
  await new Promise((resolve) => setTimeout(resolve, 300));
  await settle(4);
  assert.match(host.textContent, /Generated successfully|完成汇报/);
});

test('local bulk deletion is unreachable without a selection entry', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  app.router.delete('/api/issues/bulk-1', { ok: true });
  app.evaluate(`
    state.data.issues=[{id:'bulk-1',title:'Synced item',projectId:state.projectFilter,status:'backlog',source:'feishu_bitable',externalRef:{source:'feishu_bitable',externalId:'rec-1'}}];
    state.issueSelection=new Set(['bulk-1']);state.issueSelectionMode=true;
    switchView('executions');
  `);
  await settle(4);
  assert.equal(app.evaluate('state.issueSelectionMode'), true);
  assert.equal(app.document.querySelector('[data-issue-clear-selection]'), null);
  assert.equal(app.document.querySelector('[data-issue-cancel-selection]'), null);
  await app.evaluate('deleteSelectedIssues()');
  await settle(2);
  assert.equal(app.router.calls.filter((c) => c.method === 'DELETE').length, 0, '执行中心不触发本地删除');
});
