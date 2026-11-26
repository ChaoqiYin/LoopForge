import test from 'node:test';
import assert from 'node:assert/strict';
import { loadApp, settle } from '../lib/load-app.mjs';

function describe(app, text) {
  const input = app.document.querySelector('#chatInput');
  input.value = text;
  input.dispatchEvent(new app.window.Event('input', { bubbles: true }));
}

test('local requirement composition is closed and never calls creation APIs', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  const $ = (s) => app.document.querySelector(s);

  app.evaluate('openRequirementWizard()');
  assert.equal($('#requirementModal').classList.contains('open'), false, '本地建单弹窗不再打开');
  assert.match($('#toastContainer').textContent, /外部管理工作台创建 Work Item/);

  describe(app, 'Ship audit log\nKeep 90 days');
  const before = app.router.calls.length;
  await app.evaluate(`createIssueFromChat('local')`);
  await app.evaluate(`createIssueFromChat(undefined,'chat')`);
  assert.equal(app.router.calls.length, before, '任何提交路径都不能再调用本地建单 API');
  assert.equal(app.evaluate(`state.reqCreating`), '');
  assert.match($('#toastContainer').textContent, /本地建单已关闭/);
  assert.equal($('#requirementModal').classList.contains('open'), false);
  assert.equal($('#chatInput').value, 'Ship audit log\nKeep 90 days');
});

test('synced Work Items can still be handed to the AI workbench', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  const $ = (s) => app.document.querySelector(s);
  const frame = $('#chatNextFrame');
  const posted = [];
  frame.contentWindow.postMessage = (message) => posted.push(JSON.parse(JSON.stringify(message)));

  app.evaluate(`switchView('executions');openIssueInChat({id:'issue-next',projectId:'project-1'})`);
  assert.equal(app.evaluate(`activeViewName()`), 'collaboration');
  assert.equal(app.evaluate(`activeViewName()`), 'collaboration');
  assert.deepEqual(JSON.parse(JSON.stringify(app.evaluate(`pendingChatIssueMention`))), { issueId: 'issue-next', projectId: 'project-1' });

  app.window.dispatchEvent(new app.window.MessageEvent('message', {
    data: { type: 'agentflow-chat-ready' }, origin: app.window.location.origin, source: frame.contentWindow,
  }));
  assert.ok(posted.some(message => message.type === 'agentflow-chat-mention-issue' && message.issueId === 'issue-next' && message.projectId === 'project-1'));
  assert.ok(posted.some(message => message.type === 'agentflow-project-context'), '项目上下文随 mention 下发');
  assert.equal(app.evaluate(`pendingChatIssueMention`), null);

  posted.length = 0;
  const src = frame.src;
  app.evaluate(`switchView('executions');openIssueInChat({id:'issue-already',projectId:'project-1'})`);
  assert.ok(posted.some(message => message.type === 'agentflow-chat-mention-issue' && message.issueId === 'issue-already'));
  assert.equal(frame.src, src, '已就绪的聊天不重新加载');
  await settle(1);
});
