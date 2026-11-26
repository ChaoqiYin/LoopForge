import test from 'node:test';
import assert from 'node:assert/strict';
import { loadApp, settle } from '../lib/load-app.mjs';

test('stale local-requirement drafts are discarded instead of reopening the editor', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  const $ = (s) => app.document.querySelector(s);
  const draft = { open: true, projectId: 'project-1', input: 'Restored text', target: 'local', repoIds: ['repo-1'], baseBranch: 'feature/keep', workflowId: 'flow-1' };
  app.evaluate(`sessionStorage.setItem('agentflow.requirementComposer',${JSON.stringify(JSON.stringify(draft))})`);
  assert.equal(app.evaluate(`restoreRequirementComposer()`), false, '旧草稿不再恢复本地建单');
  assert.equal($('#requirementModal').classList.contains('open'), false);
  assert.equal(app.evaluate(`sessionStorage.getItem('agentflow.requirementComposer')`), null, '旧草稿被清除');
  assert.match($('#toastContainer').textContent, /本地建单已关闭/);
});

test('external Work Items remain observable without local requirement controls', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  const $ = (s) => app.document.querySelector(s);
  app.evaluate(`openRequirementWizard()`);
  assert.equal($('#requirementModal').classList.contains('open'), false, '本地建单弹窗不可达');
  assert.equal(app.evaluate(`issueCanPushTAPD(state.data.issues[0])`), false);
  assert.equal(app.document.querySelector('#newIssue'), null);
  assert.equal(app.document.querySelector('#dashNewRequirement'), null);
  await settle(1);
});

test('pushing an existing issue to TAPD from the detail page updates it and reports failures', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  const $ = (s) => app.document.querySelector(s);
  app.evaluate(`pushIssueToTAPDFromDetail('missing')`);
  await settle(2);
  assert.match($('#toastContainer').textContent, /未找到工作项/);
  await app.evaluate(`pushIssueToTAPDFromDetail('issue-1')`);
  assert.match($('#toastContainer').textContent, /未配置 TAPD/);
  app.evaluate(`Object.assign(state.data.projects[0],{tapdWorkspaceId:'ws',tapdIterationId:'it'});state.data.syncConfig={tapdMcpUrl:'https://tapd.example/mcp'}`);
  assert.equal(app.evaluate(`issueCanPushTAPD(state.data.issues[0])`), true);
  app.router.post('/api/sync/create', (call) => ({ status: 200, body: { ...app.evaluate(`state.data.issues[0]`), id: call.body.issueId, externalRef: { externalId: 'T-42' } } }));
  await app.evaluate(`pushIssueToTAPDFromDetail('issue-1')`);
  assert.match($('#toastContainer').textContent, /已在 TAPD 创建需求 T-42/);
  app.router.post('/api/sync/create', { error: 'tapd down' }, 502);
  await app.evaluate(`pushIssueToTAPDFromDetail('issue-1')`);
  assert.match($('#toastContainer').textContent, /tapd down/);
});
