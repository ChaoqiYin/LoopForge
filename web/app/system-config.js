// 系统配置：外部工作事项连接、自检、字段/状态映射与立即同步。
// 复用 T2 已实现的 /api/work-management/* 接口；本页不创建第二套凭证体系，
// App Secret 仍由「通道管理」里的飞书机器人保存。
const WM_FIELD_LABELS = {
  business_key: '业务唯一键（必填）',
  title: '标题（必填）',
  status: '外部业务状态（必填）',
  description: '描述',
  priority: '优先级',
  assignee: '负责人',
  acceptance_criteria: '验收标准',
  repository: '关联仓库',
  external_url: '外部链接',
  project: '覆盖项目',
};
const WM_STATUS_ACTIONS = [
  ['start_sop', '启动 SOP'],
  ['ignore', '仅同步 / 忽略'],
  ['pause', '暂停当前执行'],
  ['cancel', '取消当前执行'],
  ['restart', '重启（先取消再派发）'],
  ['writeback', '回写外部状态'],
];
let wmPageState = { loaded: false, connection: null, fieldMappings: [], statusMappings: [], loading: false };

function wmIcon(name, size = 15) {
  return `<svg class="agent-icon harness-icon" data-icon="${escapeHTML(name)}" width="${size}" height="${size}" aria-hidden="true" focusable="false"></svg>`;
}

function wmReadiness(conn) {
  const state = String(conn?.readiness?.state || '').trim();
  const message = String(conn?.readiness?.message || '').trim();
  if (state === 'ready') return { tone: 'success', label: '连接正常', message };
  if (!state) return { tone: 'neutral', label: '未自检', message: '保存后执行一次连接自检' };
  return { tone: 'error', label: state === 'not_ready' ? '未就绪' : '需要检查', message: message || '连接自检未通过' };
}

function wmBadge(tone, label, icon = '') {
  const glyph = icon ? `<span class="hui-badge-glyph" aria-hidden="true">${wmIcon(icon, 12)}</span>` : '';
  return `<span class="hui-badge" data-size="sm" data-tone="${tone}">${glyph}<span class="hui-badge-label">${escapeHTML(label)}</span></span>`;
}

function wmLoading(text) {
  return `<div class="wm-loading" role="status">${wmIcon('arc-spinner', 16)}<span>${escapeHTML(text)}</span></div>`;
}

function wmNotice(title, body, isError = false) {
  return `<div class="wm-notice${isError ? ' is-error' : ''}">${wmIcon(isError ? 'alert' : 'info', 18)}<div><b>${escapeHTML(title)}</b><p>${escapeHTML(body)}</p></div></div>`;
}

async function loadSystemConfigPage() {
  const host = $('#systemConfigContent');
  if (!host) return;
  if (!isPlatformAdmin()) {
    host.innerHTML = wmNotice('仅平台管理员可配置', '外部工作事项连接、映射与同步配置对所有成员只读。');
    return;
  }
  if (wmPageState.loading) return;
  wmPageState.loading = true;
  host.innerHTML = wmLoading('正在加载外部工作事项连接…');
  try {
    const data = await api('/api/work-management/connections', { silent: true });
    const list = data?.connections || [];
    wmPageState.connection = list[0] || null;
    wmPageState.fieldMappings = [];
    wmPageState.statusMappings = [];
    if (wmPageState.connection?.id) {
      const detail = await api(`/api/work-management/connections/${encodeURIComponent(wmPageState.connection.id)}`, { silent: true });
      wmPageState.connection = detail?.connection || wmPageState.connection;
      wmPageState.fieldMappings = detail?.fieldMappings || [];
      wmPageState.statusMappings = detail?.statusMappings || [];
    }
    wmPageState.loaded = true;
    renderSystemConfigPage();
  } catch (error) {
    host.innerHTML = wmNotice('无法加载连接配置', error.message, true);
  } finally {
    wmPageState.loading = false;
  }
}

function wmProjectOptions(selected) {
  return (state.data.projects || []).map(project =>
    `<option value="${escapeHTML(project.id)}"${project.id === selected ? ' selected' : ''}>${escapeHTML(project.name || project.id)}</option>`).join('');
}

function wmBotOptions(selected) {
  return (state.data.bots || []).map(bot =>
    `<option value="${escapeHTML(bot.id)}"${bot.id === selected ? ' selected' : ''}>${escapeHTML(bot.name || bot.id)}</option>`).join('');
}

function wmWorkflowOptions(selected) {
  const workflows = typeof catalogWorkflows === 'function' ? catalogWorkflows() : (state.data.workflows || []);
  return `<option value="">选择 SOP</option>` + workflows.map(workflow =>
    `<option value="${escapeHTML(workflow.id)}"${workflow.id === selected ? ' selected' : ''}>${escapeHTML(workflow.name || workflow.id)}</option>`).join('');
}

function renderSystemConfigPage() {
  const host = $('#systemConfigContent');
  if (!host) return;
  const conn = wmPageState.connection || {};
  const readiness = wmReadiness(conn);
  const where = conn.id ? `${wmBadge(readiness.tone, readiness.label, readiness.tone === 'success' ? 'check' : readiness.tone === 'error' ? 'alert' : 'clock')}${readiness.message && readiness.message !== readiness.label ? `<span class="wm-readiness-msg">${escapeHTML(readiness.message)}</span>` : ''}` : '';
  host.innerHTML = `<div class="wm-grid">
    <section class="wm-panel wm-connection-panel" aria-label="连接配置">
      <header class="wm-panel-head"><div><h2>连接配置${conn.name ? ` · ${escapeHTML(conn.name)}` : ''}</h2><p>首期单连接：一个飞书应用、一个多维表格 Base、一张主表。</p></div><div class="wm-panel-badge">${where}</div></header>
      <form id="wmConnectionForm" class="wm-form" autocomplete="off">
        <input type="hidden" name="id" value="${escapeHTML(conn.id || '')}">
        <label class="wm-field"><span>连接名称</span><input name="name" value="${escapeHTML(conn.name || '飞书需求主表')}" placeholder="例如：飞书需求主表"></label>
        <div class="wm-row">
          <label class="wm-field"><span>平台</span><input name="platform" value="feishu_bitable" readonly></label>
          <label class="wm-field"><span>飞书应用（通道管理）</span><select name="botId">${wmBotOptions(conn.botId)}</select></label>
        </div>
        <div class="wm-row">
          <label class="wm-field"><span>Base app_token</span><input name="baseAppToken" value="${escapeHTML(conn.baseAppToken || '')}" placeholder="bascn…"></label>
          <label class="wm-field"><span>数据表 table_id</span><input name="tableId" value="${escapeHTML(conn.tableId || '')}" placeholder="tbl…"></label>
        </div>
        <label class="wm-field"><span>默认项目</span><select name="defaultProjectId">${wmProjectOptions(conn.defaultProjectId)}</select></label>
        <div class="wm-switches">
          <label class="wm-switch"><input type="checkbox" name="enabled" role="switch"${conn.enabled === false ? '' : ' checked'}><span><b>启用连接</b><small>停用后不再触发执行，也不允许立即同步</small></span></label>
          <label class="wm-switch"><input type="checkbox" name="allowWriteback" role="switch"${conn.allowWriteback ? ' checked' : ''}><span><b>允许回写外部状态</b><small>默认关闭；只有状态映射显式声明回写时才写入外部平台</small></span></label>
        </div>
        <div class="wm-actions">
          <button type="button" class="button primary" data-wm-save>保存连接</button>
          <button type="button" class="button ghost" data-wm-verify${conn.id ? '' : ' disabled'}>连接自检</button>
          <button type="button" class="button ghost" data-wm-sync${conn.id ? '' : ' disabled'}>立即同步</button>
          <button type="button" class="button danger-outline" data-wm-delete${conn.id ? '' : ' hidden'}>删除连接</button>
        </div>
        <p class="wm-hint">App Secret 在「通道管理」的飞书机器人里维护；这里只引用已加密保存的凭证。</p>
      </form>
    </section>

    <section class="wm-panel" aria-label="字段映射">
      <header class="wm-panel-head"><div><h2>字段映射</h2><p>按外部字段名配置。业务唯一键、标题、状态为必填；字段 ID 由服务端缓存。</p></div></header>
      <form id="wmFieldMappings" class="wm-form">${wmFieldRows()}
        <div class="wm-actions"><button type="button" class="button primary" data-wm-save-fields${conn.id ? '' : ' disabled'}>保存字段映射</button></div>
      </form>
    </section>

    <section class="wm-panel" aria-label="状态映射">
      <header class="wm-panel-head"><div><h2>状态映射</h2><p>外部业务状态进入某个值时触发对应动作；未知状态只同步、不触发。</p></div><button type="button" class="button ghost small" data-wm-add-status${conn.id ? '' : ' disabled'}>${wmIcon('plus', 14)}新增状态</button></header>
      <form id="wmStatusMappings" class="wm-form">${wmStatusRows()}
        <div class="wm-actions"><button type="button" class="button primary" data-wm-save-statuses${conn.id ? '' : ' disabled'}>保存状态映射</button></div>
      </form>
    </section>
  </div>`;
  bindSystemConfigActions();
}

function wmFieldRows() {
  const rows = new Map((wmPageState.fieldMappings || []).map(row => [row.field, row]));
  return Object.entries(WM_FIELD_LABELS).map(([field, label]) => {
    const row = rows.get(field) || {};
    const required = ['business_key', 'title', 'status'].includes(field);
    return `<label class="wm-field wm-field-row" data-field="${field}"><span>${escapeHTML(label)}</span><input name="${field}" value="${escapeHTML(row.externalFieldName || '')}" placeholder="${required ? '必填' : '可选'}"${required ? ' required' : ''}></label>`;
  }).join('');
}

function wmStatusRows() {
  const rows = wmPageState.statusMappings || [];
  if (!rows.length) return '<p class="wm-empty">还没有状态映射。新增一条后，选择外部状态和对应动作。</p>';
  return rows.map((row, index) => wmStatusRow(row, index)).join('');
}

function wmStatusRow(row = {}, index = 0) {
  const action = row.action || 'ignore';
  return `<div class="wm-status-row" data-status-row>
    <label class="wm-field"><span>外部状态</span><input name="externalStatus" value="${escapeHTML(row.externalStatus || '')}" placeholder="例如：开发中"></label>
    <label class="wm-field"><span>动作</span><select name="action">${WM_STATUS_ACTIONS.map(([value, label]) => `<option value="${value}"${action === value ? ' selected' : ''}>${escapeHTML(label)}</option>`).join('')}</select></label>
    <label class="wm-field" data-status-workflow><span>启动 SOP</span><select name="workflowId">${wmWorkflowOptions(row.workflowId)}</select></label>
    <label class="wm-field" data-status-writeback><span>回写状态</span><input name="writebackStatus" value="${escapeHTML(row.writebackStatus || '')}" placeholder="例如：已排期"></label>
    <button type="button" class="button ghost small wm-remove" data-wm-remove-status="${index}" aria-label="删除状态映射">${wmIcon('trash', 14)}删除</button>
  </div>`;
}

function wmSyncStatusRowVisibility(row) {
  const action = row.querySelector('select[name="action"]')?.value || '';
  const workflow = row.querySelector('[data-status-workflow]');
  const writeback = row.querySelector('[data-status-writeback]');
  if (workflow) workflow.hidden = action !== 'start_sop' && action !== 'restart';
  if (writeback) writeback.hidden = action !== 'writeback';
}

function bindSystemConfigActions() {
  const form = $('#wmConnectionForm');
  if (!form) return;
  form.querySelector('[data-wm-save]').onclick = saveSystemConfigConnection;
  form.querySelector('[data-wm-verify]').onclick = verifySystemConfigConnection;
  form.querySelector('[data-wm-sync]').onclick = syncSystemConfigNow;
  form.querySelector('[data-wm-delete]').onclick = deleteSystemConfigConnection;
  $('#wmFieldMappings')?.querySelector('[data-wm-save-fields]')?.addEventListener('click', saveSystemConfigFields);
  $('#wmStatusMappings')?.querySelector('[data-wm-save-statuses]')?.addEventListener('click', saveSystemConfigStatuses);
  $('#systemConfigContent')?.querySelector('[data-wm-add-status]')?.addEventListener('click', () => {
    const host = $('#wmStatusMappings');
    const empty = host.querySelector('.wm-empty');
    if (empty) empty.remove();
    const wrap = document.createElement('div');
    wrap.innerHTML = wmStatusRow({}, host.querySelectorAll('[data-status-row]').length);
    const row = wrap.firstElementChild;
    host.insertBefore(row, host.querySelector('.wm-actions'));
    bindStatusRow(row);
    wmSyncStatusRowVisibility(row);
  });
  $$('#wmStatusMappings [data-status-row]').forEach(bindStatusRow);
}

function bindStatusRow(row) {
  row.querySelector('select[name="action"]')?.addEventListener('change', () => wmSyncStatusRowVisibility(row));
  row.querySelector('[data-wm-remove-status]')?.addEventListener('click', () => row.remove());
  wmSyncStatusRowVisibility(row);
}

function wmConnectionPayload() {
  const form = $('#wmConnectionForm');
  return {
    id: form.elements.id.value,
    name: form.elements.name.value.trim(),
    platform: 'feishu_bitable',
    botId: form.elements.botId.value,
    baseAppToken: form.elements.baseAppToken.value.trim(),
    tableId: form.elements.tableId.value.trim(),
    defaultProjectId: form.elements.defaultProjectId.value,
    allowWriteback: form.elements.allowWriteback.checked,
    enabled: form.elements.enabled.checked,
  };
}

async function saveSystemConfigConnection() {
  const button = $('#wmConnectionForm [data-wm-save]');
  if (button) button.disabled = true;
  try {
    const body = wmConnectionPayload();
    const data = await api('/api/work-management/connections', { method: 'PUT', body: JSON.stringify(body) });
    wmPageState.connection = data?.connection || { ...(wmPageState.connection || {}), ...body };
    toast('连接已保存', 'ok');
    await loadSystemConfigPage();
  } catch (error) {
    toast(error.message, 'err');
  } finally {
    if (button) button.disabled = false;
  }
}

async function verifySystemConfigConnection() {
  const id = wmPageState.connection?.id;
  if (!id) return;
  try {
    const data = await api(`/api/work-management/connections/${encodeURIComponent(id)}/verify`, { method: 'POST' });
    wmPageState.connection = { ...(wmPageState.connection || {}), readiness: data?.readiness || {} };
    renderSystemConfigPage();
    toast(data?.readiness?.message || '连接自检通过', data?.ok ? 'ok' : 'warn');
  } catch (error) {
    toast(error.message, 'err');
  }
}

async function syncSystemConfigNow() {
  const id = wmPageState.connection?.id;
  if (!id) return;
  const button = $('[data-wm-sync]');
  if (button) button.disabled = true;
  try {
    const data = await api(`/api/work-management/connections/${encodeURIComponent(id)}/sync`, { method: 'POST' });
    const scanned = data?.sync?.scanned ?? data?.sync?.created ?? null;
    toast(scanned == null ? (data?.message || '同步完成') : `同步完成：${scanned} 条记录`, 'ok');
  } catch (error) {
    toast(error.message, 'err');
  } finally {
    if (button) button.disabled = false;
  }
}

async function deleteSystemConfigConnection() {
  const id = wmPageState.connection?.id;
  if (!id) return;
  if (!await afConfirm({ title: '删除连接', message: '删除后停止外部同步与触发，已同步的 Work Item 与执行证据保留。继续？', confirmText: '删除连接', isDanger: true })) return;
  try {
    await api(`/api/work-management/connections/${encodeURIComponent(id)}`, { method: 'DELETE' });
    wmPageState = { loaded: false, connection: null, fieldMappings: [], statusMappings: [], loading: false };
    toast('连接已删除', 'ok');
    renderSystemConfigPage();
  } catch (error) {
    toast(error.message, 'err');
  }
}

function wmCollectFields() {
  const connection = wmPageState.connection || {};
  return $$('#wmFieldMappings [data-field]').map(row => ({
    connectionId: connection.id || '',
    tableId: connection.tableId || '',
    field: row.dataset.field,
    externalFieldName: row.querySelector('input')?.value.trim() || '',
  })).filter(row => row.externalFieldName);
}

async function saveSystemConfigFields() {
  const id = wmPageState.connection?.id;
  if (!id) return;
  try {
    const data = await api(`/api/work-management/connections/${encodeURIComponent(id)}/field-mappings`, {
      method: 'PUT', body: JSON.stringify({ fieldMappings: wmCollectFields() }),
    });
    wmPageState.fieldMappings = data?.fieldMappings || wmPageState.fieldMappings;
    renderSystemConfigPage();
    toast(data?.warning || '字段映射已保存', data?.warning ? 'warn' : 'ok');
  } catch (error) {
    toast(error.message, 'err');
  }
}

function wmCollectStatuses() {
  const connection = wmPageState.connection || {};
  return $$('#wmStatusMappings [data-status-row]').map(row => {
    const rowData = {
      connectionId: connection.id || '',
      tableId: connection.tableId || '',
      externalStatus: row.querySelector('[name="externalStatus"]')?.value.trim() || '',
      action: row.querySelector('[name="action"]')?.value || 'ignore',
    };
    const workflow = row.querySelector('[name="workflowId"]')?.value;
    if (workflow && (rowData.action === 'start_sop' || rowData.action === 'restart')) rowData.workflowId = workflow;
    const writeback = row.querySelector('[name="writebackStatus"]')?.value.trim();
    if (writeback && rowData.action === 'writeback') rowData.writebackStatus = writeback;
    return rowData;
  }).filter(row => row.externalStatus);
}

async function saveSystemConfigStatuses() {
  const id = wmPageState.connection?.id;
  if (!id) return;
  try {
    const data = await api(`/api/work-management/connections/${encodeURIComponent(id)}/status-mappings`, {
      method: 'PUT', body: JSON.stringify({ statusMappings: wmCollectStatuses() }),
    });
    wmPageState.statusMappings = data?.statusMappings || wmPageState.statusMappings;
    renderSystemConfigPage();
    toast('状态映射已保存', 'ok');
  } catch (error) {
    toast(error.message, 'err');
  }
}
