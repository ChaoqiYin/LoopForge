import test from 'node:test';
import assert from 'node:assert/strict';
import { loadApp, settle } from '../lib/load-app.mjs';

const formalIssue = {
  id: 'wi-1', projectId: 'project-1', number: 7, title: '结算页优惠券展示',
  description: '外部记录描述', status: 'in_progress', priority: 'high', kind: 'requirement',
  source: 'feishu_bitable', workflowId: 'flow-1',
  externalRef: {
    source: 'feishu_bitable', externalId: 'rec-001', externalIid: 0,
    externalUrl: 'https://feishu.example/base/rec-001', externalState: '开发中',
    tableId: 'tbl-1', businessKey: 'BIZ-7', recordId: 'rec-001',
  },
  acceptanceCriteria: '优惠券金额正确', externalAssignee: '张敏',
  createdAt: '2026-11-20T10:00:00Z', updatedAt: '2026-11-21T10:00:00Z',
};

const formalRun = {
  id: 'run-formal-1', projectId: 'project-1', issueId: 'wi-1', workflowId: 'flow-1',
  workflowName: 'Delivery Flow', status: 'running', currentNodeId: 'node-1', progress: 30,
  origin: '', createdAt: '2026-11-21T09:00:00Z',
  executionContext: { projectId: 'project-1' },
};

const tempRun = {
  id: 'run-temp-1', projectId: 'project-1', workflowId: 'conversation-workspace',
  workflowName: '自由探索', workflowSnapshot: { id: 'conversation-workspace', name: '自由探索' },
  kind: 'workspace', status: 'succeeded', origin: 'assistant_scratch',
  createdAt: '2026-11-21T08:00:00Z', executionContext: { projectId: 'project-1' },
};

test('navigation matches the external-work spec and removes local requirement entries', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  const rail = app.document.querySelector('#platformRail');
  const labels = [...rail.querySelectorAll('[data-view] .rail-label')].map((el) => el.textContent.trim());
  assert.deepEqual(labels, ['工作总览', '执行中心', '审批中心', '统计分析', '设置']);
  assert.equal(rail.querySelector('[data-view="issues"]'), null, '旧需求列表入口必须移除');
  assert.equal(rail.querySelector('[data-view="executions"] .rail-label').textContent, '执行中心');
  assert.equal(rail.querySelector('[data-view="approvals"] .rail-label').textContent, '审批中心');
  assert.equal(rail.querySelector('[data-view="observability"] .rail-label').textContent, '统计分析');

  const settings = app.document.querySelector('#settingsSidebar');
  for (const [view, label] of [['systemConfig', '系统配置'], ['repositories', '代码仓库'], ['sops', 'SOP'], ['agents', 'Agent']]) {
    const entry = settings.querySelector(`[data-view="${view}"]`);
    assert.ok(entry, `settings navigation is missing ${view}`);
    assert.equal(entry.querySelector('.nav-label').textContent, label);
  }
  assert.equal(app.document.querySelector('#newIssue'), null, '新建需求入口必须移除');
  assert.equal(app.document.querySelector('#issueHeadingMore'), null, '外部同步/导入入口必须移出执行中心');
  assert.equal(app.document.querySelector('[data-issue-view]'), null, '执行中心不提供本地拖拽看板');
  assert.equal(app.document.body.textContent.includes('需求列表'), false);
});

test('execution center reads formal Work Items and shows external and internal status side by side', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  app.router.get('/api/issues?formal=1', [formalIssue]);
  app.router.get('/api/tasks', [{ id: 'task-1', issueId: 'wi-1', runId: 'run-formal-1', workflowId: 'flow-1', status: 'running' }]);
  app.router.get('/api/runs', [formalRun, tempRun]);
  app.router.get('/api/issues/wi-1', { issue: formalIssue, comments: [] });

  app.evaluate(`
    state.data.projects=[{id:'project-1',name:'Atlas',key:'ATL'}];
    state.data.workflows=[{id:'flow-1',projectId:'project-1',name:'Delivery Flow',nodes:[
      {id:'node-1',type:'agent',name:'实现',agentId:'agent-1'}]}];
    state.data.agents=[{id:'agent-1',projectId:'project-1',name:'Developer'}];
    state.data.repositories=[];state.projectFilter='project-1';
    switchView('executions');
  `);
  await settle(6);

  const formalCall = app.router.calls.find((call) => call.method === 'GET' && call.search === '?formal=1');
  assert.ok(formalCall, '执行中心必须只拉取正式 Work Item（formal=1）');
  const table = app.document.querySelector('#issueBoard');
  assert.match(table.textContent, /结算页优惠券展示/);
  assert.match(table.textContent, /BIZ-7/);
  assert.match(table.textContent, /飞书多维表格/);
  assert.match(table.textContent, /开发中/);
  assert.match(table.textContent, /Delivery Flow/);
  assert.match(table.textContent, /Developer/);
  assert.ok(table.querySelector('a[href="https://feishu.example/base/rec-001"]'), '外部链接必须可点');
  assert.match(table.textContent, /formal-1|正在执行/);
  assert.doesNotMatch(table.textContent, /本地新建需求|新建需求/);

  table.querySelector('.issue-table-row[data-id="wi-1"]').click();
  await settle(4);
  assert.equal(app.document.querySelector('#issueDetailDrawer').classList.contains('open'), true);
  assert.equal(app.router.calls.some((call) => call.method === 'PUT' || call.method === 'POST'), false,
    '打开执行详情不能触发本地建单/编辑请求');
});

test('system configuration saves connection, self-check, sync, and mappings', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  const connection = {
    id: 'wm-1', name: '飞书需求主表', platform: 'feishu_bitable', botId: 'bot-1',
    baseAppToken: 'bascn', tableId: 'tbl-1', defaultProjectId: 'project-1',
    allowWriteback: false, enabled: true, readiness: { state: 'ready', message: '连接正常' },
  };
  const fieldMappings = [
    { field: 'business_key', externalFieldName: '业务编号' },
    { field: 'title', externalFieldName: '标题' },
    { field: 'status', externalFieldName: '状态' },
  ];
  const statusMappings = [
    { externalStatus: '开发中', action: 'start_sop', workflowId: 'flow-1', awaitingApproval: true },
    { externalStatus: '已上线', action: 'ignore' },
  ];
  app.router.get('/api/work-management/connections', { connections: [connection] });
  app.router.get('/api/work-management/connections/wm-1', { connection, fieldMappings, statusMappings });
  app.router.put('/api/work-management/connections', (call) => ({ connection: { ...connection, ...call.body } }));
  app.router.post('/api/work-management/connections/wm-1/verify', { ok: true, readiness: { state: 'ready', message: '自检通过' } });
  app.router.post('/api/work-management/connections/wm-1/sync', { ok: true, accepted: true, sync: { scanned: 3 } });
  app.router.put('/api/work-management/connections/wm-1/field-mappings', (call) => ({ ok: true, fieldMappings: call.body.fieldMappings }));
  app.router.put('/api/work-management/connections/wm-1/status-mappings', (call) => ({ ok: true, statusMappings: call.body.statusMappings }));
  app.router.get('/api/notification-bots', [{ id: 'bot-1', name: '飞书机器人', type: 'feishu', enabled: true }]);

  app.evaluate(`
    state.data.projects=[{id:'project-1',name:'Atlas',key:'ATL'}];
    state.data.workflows=[{id:'flow-1',projectId:'project-1',name:'Delivery Flow',nodes:[]}];
    state.data.bots=[{id:'bot-1',name:'飞书机器人',type:'feishu',enabled:true}];
    switchView('systemConfig');
  `);
  await settle(6);

  const view = app.document.querySelector('#systemConfigView');
  assert.match(view.textContent, /飞书需求主表/);
  assert.match(view.textContent, /连接正常/);
  const form = app.document.querySelector('#wmConnectionForm');
  assert.equal(form.elements.name.value, '飞书需求主表');
  assert.equal(form.elements.tableId.value, 'tbl-1');
  assert.equal(form.elements.allowWriteback.checked, false);
  assert.match(form.elements.allowWriteback.className, /\bhui-switch\b/,
    '开关必须复用 Harness Switch 视觉语言');

  form.elements.allowWriteback.checked = true;
  form.querySelector('[data-wm-save]').click();
  await settle(4);
  const save = app.router.calls.find((call) => call.method === 'PUT' && call.path === '/api/work-management/connections');
  assert.equal(save.body.allowWriteback, true);
  assert.equal(save.body.baseAppToken, 'bascn');

  app.document.querySelector('[data-wm-verify]').click();
  await settle(4);
  assert.ok(app.router.calls.some((call) => call.method === 'POST' && call.path === '/api/work-management/connections/wm-1/verify'));

  app.document.querySelector('[data-wm-sync]').click();
  await settle(4);
  assert.ok(app.router.calls.some((call) => call.method === 'POST' && call.path === '/api/work-management/connections/wm-1/sync'));

  const fieldForm = app.document.querySelector('#wmFieldMappings');
  assert.ok(fieldForm.querySelector('[data-field="business_key"] input').value, '必填字段映射必须回填');
  fieldForm.querySelector('[data-field="business_key"] input').value = '业务编号';
  fieldForm.querySelector('[data-wm-save-fields]').click();
  await settle(4);
  const fieldSave = app.router.calls.find((call) => call.method === 'PUT' && call.path === '/api/work-management/connections/wm-1/field-mappings');
  assert.ok(fieldSave.body.fieldMappings.some((row) => row.field === 'business_key' && row.externalFieldName === '业务编号'));

  const statusForm = app.document.querySelector('#wmStatusMappings');
  assert.ok(statusForm.querySelector('[data-status-row]'), '状态映射必须回填');
  const awaiting = statusForm.querySelector('[data-status-row] input[name="awaitingApproval"]');
  assert.ok(awaiting, '状态映射必须提供显式的等待业务审批开关');
  assert.equal(awaiting.checked, true);
  assert.match(awaiting.className, /\bhui-switch\b/);
  statusForm.querySelector('[data-wm-save-statuses]').click();
  await settle(4);
  const statusSave = app.router.calls.find((call) => call.method === 'PUT' && call.path === '/api/work-management/connections/wm-1/status-mappings');
  assert.equal(statusSave.body.statusMappings[0].externalStatus, '开发中');
  assert.equal(statusSave.body.statusMappings[0].action, 'start_sop');
  assert.equal(statusSave.body.statusMappings[0].awaitingApproval, true);
});

test('approval center is read-only and lists external business approvals plus internal execution gates', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  const waitingRun = {
    id: 'run-gate-1', projectId: 'project-1', status: 'waiting', workflowId: 'flow-1',
    createdAt: '2026-11-20T10:00:00Z', executionContext: { projectId: 'project-1', responsibleUserId: 7 },
    workflowSnapshot: { id: 'flow-1', name: '交付 SOP', version: 1, revision: 1,
      nodes: [{ id: 'review', name: '验收', type: 'approval', taskPrompt: '确认结果' }], edges: [] },
    nodes: { review: { status: 'waiting_approval' } },
  };
  const pendingExternal = { ...formalIssue, id: 'wi-2', title: '待审批的报销流程',
    externalRef: { ...formalIssue.externalRef, externalId: 'rec-002', externalState: '待审批', awaitingApproval: true } };
  // A status whose text looks like an approval but has no explicit mapping
  // marker must not be guessed into the approval center.
  const legacyLookalike = { ...formalIssue, id: 'wi-3', title: '只是文案像审批',
    externalRef: { ...formalIssue.externalRef, externalId: 'rec-003', externalState: '待审批（旧状态）' } };
  app.router.get('/api/runs', [waitingRun]);
  app.router.get('/api/tasks', [{ id: 'task-1', issueId: 'wi-1', runId: 'run-gate-1', workflowId: 'flow-1' }]);
  app.router.get('/api/issues', [pendingExternal, legacyLookalike]);

  app.evaluate(`
    state.data.projects=[{id:'project-1',name:'Atlas',key:'ATL'}];
    state.data.runs=[${JSON.stringify(waitingRun)}];
    state.data.tasks=[{id:'task-1',issueId:'wi-1',runId:'run-gate-1',workflowId:'flow-1'}];
    state.data.issues=[${JSON.stringify(pendingExternal)},${JSON.stringify(legacyLookalike)}];
    state.currentUser={id:7,loginName:'reviewer',isPlatformAdmin:true};
    switchView('approvals');
  `);
  await settle(6);

  const view = app.document.querySelector('#approvalsView');
  assert.match(view.textContent, /待审批的报销流程/);
  assert.doesNotMatch(view.textContent, /只是文案像审批/,
    '审批中心只能依据状态映射的显式标记，不能用状态文案猜测');
  assert.ok(view.querySelector('#externalApprovalList'), '审批中心需要独立的只读外部审批区');
  assert.ok(view.querySelector('a[href="https://feishu.example/base/rec-001"]') || view.querySelector('[data-external-link]'),
    '外部待审批必须能跳回外部平台');
  assert.match(view.textContent, /交付 SOP/);
  assert.equal(view.querySelector('[data-approve]'), null, '审批中心不提供通过操作');
  assert.equal(view.querySelector('[data-retry-node]'), null);
  assert.equal(view.querySelector('[data-reject]'), null);
  assert.equal(view.querySelector('[data-ap-batch-approve]'), null);
  assert.equal(app.router.calls.some((call) => call.method === 'POST' && /approve|reject|retry/.test(call.path)), false);
  assert.match(view.textContent, /去外部平台处理|只读/);
});

test('repositories view configures service credentials, default branch, and associated project', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  const repository = {
    id: 'repo-1', projectId: 'project-1', name: 'atlas-web', provider: 'https',
    cloneUrl: 'https://git.example/atlas/web.git', webUrl: 'https://git.example/atlas/web',
    defaultBranch: 'main', integrateBranch: 'develop', serviceCredentialId: '',
  };
  app.router.get('/api/repositories/repo-1/git-credentials', []);
  app.router.put('/api/repositories', (call) => ({ ...repository, ...call.body, defaultBranch: 'release' }));
  app.router.put('/api/repositories/repo-1/git-credentials', (call) => call.body);

  app.evaluate(`
    state.data.projects=[{id:'project-1',name:'Atlas',key:'ATL'}];
    state.data.repositories=[${JSON.stringify(repository)}];
    state.projectFilter='project-1';
    switchView('repositories');
  `);
  await settle(6);

  const view = app.document.querySelector('#repositoriesView');
  assert.match(view.textContent, /atlas-web/);
  assert.match(view.textContent, /Atlas/);
  const branch = view.querySelector('[data-repo-default-branch="repo-1"]');
  assert.equal(branch.value, 'main');
  branch.value = 'release';
  view.querySelector('[data-repo-save="repo-1"]').click();
  await settle(4);
  const save = app.router.calls.find((call) => call.method === 'PUT' && call.path === '/api/repositories');
  assert.equal(save.body.defaultBranch, 'release');
  assert.equal(save.body.projectId, 'project-1');

  view.querySelector('[data-repo-credential-form="repo-1"] [name="kind"]').value = 'https';
  view.querySelector('[data-repo-credential-form="repo-1"] [name="host"]').value = 'https://git.example';
  view.querySelector('[data-repo-credential-form="repo-1"] [name="username"]').value = 'robot';
  view.querySelector('[data-repo-credential-form="repo-1"] [name="secret"]').value = 'token';
  view.querySelector('[data-repo-credential-save="repo-1"]').click();
  await settle(4);
  const credential = app.router.calls.find((call) => call.method === 'PUT' && call.path === '/api/repositories/repo-1/git-credentials');
  assert.equal(credential.body.kind, 'https');
  assert.equal(credential.body.username, 'robot');
  assert.equal(view.textContent.includes('测试连接'), true);
});

test('runs view keeps temporary runs visible and can filter them from formal runs', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  app.router.get('/api/runs', [formalRun, tempRun]);
  app.router.get('/api/tasks', []);
  app.router.get('/api/issues', [formalIssue]);
  app.evaluate(`
    state.data.projects=[{id:'project-1',name:'Atlas',key:'ATL'}];
    state.data.runs=[${JSON.stringify(formalRun)},${JSON.stringify(tempRun)}];
    state.data.tasks=[];state.data.issues=[${JSON.stringify(formalIssue)}];
    state.projectFilter='project-1';state.runFilter='all';
    switchView('runs');
  `);
  await settle(6);
  const view = app.document.querySelector('#runsView');
  assert.match(view.textContent, /临时|Temporary/);
  const filter = app.document.querySelector('#runOriginFilter');
  assert.ok(filter, '运行记录需要正式/临时 Run 筛选');
  filter.value = 'temporary';
  filter.dispatchEvent(new app.window.Event('change', { bubbles: true }));
  await settle(3);
  assert.match(view.querySelector('#runList').textContent, /自由探索/);
  assert.doesNotMatch(view.querySelector('#runList').textContent, /Delivery Flow/);
  filter.value = 'formal';
  filter.dispatchEvent(new app.window.Event('change', { bubbles: true }));
  await settle(3);
  assert.match(view.querySelector('#runList').textContent, /Delivery Flow/);
  assert.doesNotMatch(view.querySelector('#runList').textContent, /自由探索/);
});

test('execution status reuses the Run status mapping and has no paused Run status', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  const result = app.evaluate(`(() => ({
    running: executionStatusLabel({status:'running'}),
    shared: runStatus('running'),
    pausedLegacy: executionStatusLabel({status:'paused'}),
    pausedLabel: runStatus('paused'),
    pausedFlag: executionStatusLabel({pauseRequested:true,status:'waiting'}),
    displayPaused: runDisplayStatus({pauseRequested:true,status:'waiting'})
  }))()`);
  assert.equal(result.running, result.shared, '执行中心必须复用 runs.js 的 Run 状态文案');
  assert.notEqual(result.pausedLegacy, '已暂停', 'paused 不是合法的 Run 状态');
  assert.equal(result.pausedLegacy, result.pausedLabel);
  assert.equal(result.pausedFlag, '已暂停');
  assert.equal(result.displayPaused, '已暂停');
});
