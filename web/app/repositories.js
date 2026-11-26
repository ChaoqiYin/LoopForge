// 代码仓库：当前项目的仓库、默认分支与仓库级 Git 服务凭证。
// 服务凭证由 T6 提供（AES-GCM 加密），执行时优先于用户级凭证。
let repositoryPageState = { credentials: {}, fixedHosts: {}, loading: false };

function repoConfigIcon(name, size = 15) {
  return `<svg class="agent-icon harness-icon" data-icon="${escapeHTML(name)}" width="${size}" height="${size}" aria-hidden="true" focusable="false"></svg>`;
}

function repoConfigBadge(label, tone = 'neutral', icon = '') {
  const glyph = icon ? `<span class="hui-badge-glyph" aria-hidden="true">${repoConfigIcon(icon, 12)}</span>` : '';
  return `<span class="hui-badge" data-size="sm" data-tone="${tone}">${glyph}<span class="hui-badge-label">${escapeHTML(label)}</span></span>`;
}

function repoConfigNotice(title, body, isError = false) {
  return `<div class="wm-notice${isError ? ' is-error' : ''}">${repoConfigIcon(isError ? 'alert' : 'info', 18)}<div><b>${escapeHTML(title)}</b><p>${escapeHTML(body)}</p></div></div>`;
}

function repoConfigProjectName(id) {
  return (state.data.projects || []).find(project => project.id === id)?.name || id || '—';
}

function repoConfigProjectOptions(selected) {
  return (state.data.projects || []).map(project =>
    `<option value="${escapeHTML(project.id)}"${project.id === selected ? ' selected' : ''}>${escapeHTML(project.name || project.id)}</option>`).join('');
}

async function loadRepositoriesPage(force = false) {
  const host = $('#repositoriesContent');
  if (!host) return;
  if (repositoryPageState.loading) return;
  repositoryPageState.loading = true;
  host.innerHTML = `<div class="wm-loading" role="status">${repoConfigIcon('arc-spinner', 16)}<span>正在加载代码仓库…</span></div>`;
  try {
    if (force || !Array.isArray(state.data.repositories) || !state.data.repositories.length) {
      const repositories = await api('/api/repositories', { silent: true });
      if (Array.isArray(repositories)) state.data.repositories = repositories;
    }
    const repositories = (state.data.repositories || []).filter(repo => !state.projectFilter || repo.projectId === state.projectFilter);
    const credentials = await Promise.all(repositories.map(async repo => {
      try {
        const list = await api(`/api/repositories/${encodeURIComponent(repo.id)}/git-credentials`, { silent: true });
        return [repo.id, Array.isArray(list) ? list : []];
      } catch {
        return [repo.id, []];
      }
    }));
    repositoryPageState.credentials = Object.fromEntries(credentials);
    renderRepositoriesPage();
  } catch (error) {
    host.innerHTML = repoConfigNotice('无法加载代码仓库', error.message, true);
  } finally {
    repositoryPageState.loading = false;
  }
}

function renderRepositoriesPage() {
  const host = $('#repositoriesContent');
  if (!host) return;
  const project = currentProject();
  const repositories = (state.data.repositories || []).filter(repo => !state.projectFilter || repo.projectId === state.projectFilter);
  const head = `<div class="repo-config-summary">${repoConfigIcon('git', 16)}<div><b>${escapeHTML(project?.name || '当前项目')}</b><span>${repositories.length} 个关联仓库</span></div></div>`;
  host.innerHTML = head + (repositories.length
    ? `<div class="repo-config-list">${repositories.map(repositoryConfigCard).join('')}</div>`
    : repoConfigNotice('当前项目还没有代码仓库', '在「项目配置」里添加仓库后，可在这里配置服务凭证与默认分支。'));
  bindRepositoryConfigActions();
}

function repositoryConfigCard(repo) {
  const credentials = repositoryPageState.credentials[repo.id] || [];
  const provider = String(repo.provider || 'git');
  const address = repo.cloneUrl || repo.webUrl || repo.gongfengProjectId || repo.name || '';
  return `<section class="wm-panel repo-config-card" data-repo-card="${escapeHTML(repo.id)}" aria-label="仓库 ${escapeHTML(repo.name || repo.id)}">
    <header class="wm-panel-head">
      <div class="repo-config-title">${repoConfigIcon('git', 18)}<div><h2>${escapeHTML(repo.name || repo.id)}</h2><p>${escapeHTML(repoConfigProjectName(repo.projectId))} · ${escapeHTML(provider)}</p></div></div>
      <div class="repo-config-badges">${repo.serviceCredentialId ? repoConfigBadge('服务凭证已关联', 'success', 'check') : repoConfigBadge('未配置服务凭证', 'warn', 'key')}</div>
    </header>
    <div class="wm-form repo-config-form">
      <div class="wm-row">
        <label class="wm-field"><span>默认分支</span><input data-repo-default-branch="${escapeHTML(repo.id)}" value="${escapeHTML(repo.defaultBranch || 'main')}" placeholder="main"></label>
        <label class="wm-field"><span>集成分支（可选）</span><input data-repo-integrate-branch="${escapeHTML(repo.id)}" value="${escapeHTML(repo.integrateBranch || '')}" placeholder="例如 agentflow/integration"></label>
      </div>
      <label class="wm-field"><span>关联项目</span><select data-repo-project="${escapeHTML(repo.id)}">${repoConfigProjectOptions(repo.projectId)}</select></label>
      <p class="wm-hint repo-address" title="${escapeHTML(address)}">${escapeHTML(address || '未配置仓库地址')}</p>
      <div class="wm-actions">
        <button type="button" class="button primary" data-repo-save="${escapeHTML(repo.id)}">保存仓库设置</button>
        <button type="button" class="button ghost" data-repo-test="${escapeHTML(repo.id)}">${repoConfigIcon('check', 14)}测试连接</button>
      </div>
    </div>
    <div class="repo-credential-block">
      <div class="repo-credential-head"><b>仓库级服务凭证</b><span>外部触发没有登录用户时使用；用户级凭证仍可作为覆盖</span></div>
      <div class="repo-credential-list">${credentials.length ? credentials.map(credentialRow).join('') : '<p class="wm-empty">还没有服务凭证。</p>'}</div>
      <form class="wm-form repo-credential-form" data-repo-credential-form="${escapeHTML(repo.id)}" autocomplete="off">
        <div class="wm-row">
          <label class="wm-field"><span>类型</span><select name="kind"><option value="https">HTTPS</option><option value="ssh">SSH</option></select></label>
          <label class="wm-field"><span>主机 / 地址</span><input name="host" placeholder="https://git.example 或 git.example.com"></label>
        </div>
        <div class="wm-row">
          <label class="wm-field"><span>用户名（可选）</span><input name="username" placeholder="robot"></label>
          <label class="wm-field"><span>密码 / Token / 私钥</span><input name="secret" type="password" placeholder="保存后不再回显"></label>
        </div>
        <label class="wm-field"><span>凭证名称（可选）</span><input name="name" placeholder="例如：交付机器人"></label>
        <div class="wm-actions"><button type="button" class="button primary" data-repo-credential-save="${escapeHTML(repo.id)}">保存服务凭证</button></div>
      </form>
    </div>
  </section>`;
}

function credentialRow(credential) {
  return `<div class="repo-credential-row" data-credential-id="${escapeHTML(credential.id || '')}">
    <span class="repo-credential-kind">${repoConfigBadge(credential.kind === 'ssh' ? 'SSH' : 'HTTPS', 'neutral', 'key')}</span>
    <div><b>${escapeHTML(credential.name || credential.host || credential.id || '服务凭证')}</b><small>${escapeHTML(credential.host || '')}${credential.username ? ` · ${escapeHTML(credential.username)}` : ''}</small></div>
    <button type="button" class="button ghost small" data-repo-credential-delete="${escapeHTML(credential.id || '')}" data-repo-id="${escapeHTML(credential.repositoryId || '')}">删除</button>
  </div>`;
}

function bindRepositoryConfigActions() {
  const host = $('#repositoriesContent');
  if (!host) return;
  host.querySelectorAll('[data-repo-save]').forEach(button => button.onclick = () => saveRepositoryConfig(button.dataset.repoSave));
  host.querySelectorAll('[data-repo-test]').forEach(button => button.onclick = () => testRepositoryConfig(button.dataset.repoTest));
  host.querySelectorAll('[data-repo-credential-save]').forEach(button => button.onclick = () => saveRepositoryCredential(button.dataset.repoCredentialSave));
  host.querySelectorAll('[data-repo-credential-delete]').forEach(button => button.onclick = () => deleteRepositoryCredential(button.dataset.repoId, button.dataset.repoCredentialDelete));
  host.querySelectorAll('[data-repo-credential-form] select[name="kind"]').forEach(select => {
    const form = select.closest('form');
    const sync = () => {
      const ssh = select.value === 'ssh';
      form.querySelector('[name="host"]').placeholder = ssh ? 'git.example.com' : 'https://git.example';
      form.querySelector('[name="username"]').disabled = ssh;
    };
    select.addEventListener('change', sync);
    sync();
  });
}

function repositoryConfigPayload(repoId) {
  const repo = (state.data.repositories || []).find(item => item.id === repoId);
  if (!repo) return null;
  const branch = document.querySelector(`[data-repo-default-branch="${CSS.escape(repoId)}"]`)?.value.trim() || 'main';
  const integrate = document.querySelector(`[data-repo-integrate-branch="${CSS.escape(repoId)}"]`)?.value.trim() || '';
  const projectId = document.querySelector(`[data-repo-project="${CSS.escape(repoId)}"]`)?.value || repo.projectId;
  return { ...repo, defaultBranch: branch, integrateBranch: integrate, projectId };
}

async function saveRepositoryConfig(repoId) {
  const body = repositoryConfigPayload(repoId);
  if (!body) return;
  try {
    const saved = await api('/api/repositories', { method: 'PUT', body: JSON.stringify(body) });
    const index = (state.data.repositories || []).findIndex(item => item.id === repoId);
    if (index >= 0) state.data.repositories[index] = { ...state.data.repositories[index], ...body, ...(saved || {}) };
    toast('仓库设置已保存', 'ok');
    renderRepositoriesPage();
  } catch (error) {
    toast(error.message, 'err');
  }
}

async function testRepositoryConfig(repoId) {
  const repo = (state.data.repositories || []).find(item => item.id === repoId);
  if (!repo) return;
  const credentials = repositoryPageState.credentials[repoId] || [];
  const address = String(repo.cloneUrl || repo.webUrl || '').trim();
  if (!address) return toast('仓库缺少地址，无法测试连接', 'err');
  if (!/^(https?:|git@|ssh:)/.test(address)) return toast('仓库地址格式无法识别', 'err');
  if (!String(repo.defaultBranch || '').trim()) return toast('请先填写默认分支', 'err');
  toast(credentials.length ? '连接配置完整：执行时将使用仓库级服务凭证' : '连接配置完整：未配置服务凭证，将回退到用户级凭证', credentials.length ? 'ok' : 'warn');
}

async function saveRepositoryCredential(repoId) {
  const form = document.querySelector(`[data-repo-credential-form="${CSS.escape(repoId)}"]`);
  if (!form) return;
  const body = {
    name: form.elements.name.value.trim(),
    kind: form.elements.kind.value,
    host: form.elements.host.value.trim(),
    username: form.elements.username.value.trim(),
    secret: form.elements.secret.value,
  };
  if (!body.host) return toast('请填写仓库主机或地址', 'err');
  if (!body.secret) return toast('请填写密码、Token 或私钥', 'err');
  try {
    const result = await api(`/api/repositories/${encodeURIComponent(repoId)}/git-credentials`, { method: 'PUT', body: JSON.stringify(body) });
    if (Array.isArray(result)) repositoryPageState.credentials[repoId] = result;
    else repositoryPageState.credentials[repoId] = [...(repositoryPageState.credentials[repoId] || []), { ...body, id: result?.id || 'saved' }];
    form.reset();
    const kind = form.querySelector('select[name="kind"]');
    if (kind) kind.dispatchEvent(new Event('change'));
    toast('服务凭证已保存', 'ok');
    renderRepositoriesPage();
  } catch (error) {
    toast(error.message, 'err');
  }
}

document.getElementById('refreshRepositories')?.addEventListener('click', () => loadRepositoriesPage(true));

async function deleteRepositoryCredential(repoId, credentialId) {
  if (!repoId || !credentialId) return;
  if (!await afConfirm({ title: '删除服务凭证', message: '删除后，没有用户级凭证的执行可能无法访问该仓库。继续？', confirmText: '删除', isDanger: true })) return;
  try {
    const result = await api(`/api/repositories/${encodeURIComponent(repoId)}/git-credentials/${encodeURIComponent(credentialId)}`, { method: 'DELETE' });
    repositoryPageState.credentials[repoId] = Array.isArray(result)
      ? result
      : (repositoryPageState.credentials[repoId] || []).filter(item => item.id !== credentialId);
    toast('服务凭证已删除', 'ok');
    renderRepositoriesPage();
  } catch (error) {
    toast(error.message, 'err');
  }
}
