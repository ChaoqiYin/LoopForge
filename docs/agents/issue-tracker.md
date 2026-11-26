# Issue tracker: GitHub

本仓库的 issue 与 spec 存放在 GitHub Issues 中，所有操作统一使用 `gh` CLI。

## 约定

- **创建 issue**：`gh issue create --title "..." --body "..."`。多行正文用 heredoc。
- **读取 issue**：`gh issue view <number> --json number,title,body,labels,comments`。
- **列出 issue**：`gh issue list --state open --json number,title,body,labels,comments --jq '[.[] | {number, title, body, labels: [.labels[].name], comments: [.comments[].body]}]'`，按需加 `--label` 与 `--state` 过滤。
- **设为父 issue 的子 issue**：`gh issue create --parent <parent> ...`，或事后 `gh issue edit <parent> --add-sub-issue <child>`（`gh` 2.94+）。旧版本：`gh api --method POST repos/<owner>/<repo>/issues/<parent>/sub_issues -F sub_issue_id=<child-db-id>`（数量为 database id）。不支持子 issue 时，在子 issue 正文顶部写 `Part of #<parent>`。
- **阻塞关系**：使用 GitHub 原生 issue dependencies（UI 可见）。`gh issue create --blocked-by <n>,<n> ...` 或 `gh issue edit <child> --add-blocked-by <blocker>`（`gh` 2.94+）。旧版本：`gh api --method POST repos/<owner>/<repo>/issues/<child>/dependencies/blocked_by -F issue_id=<blocker-db-id>`，其中 `<blocker-db-id>` 为阻塞 issue 的数字 **database id**（`gh api repos/<owner>/<repo>/issues/<n> --jq .id`，不是 `#number` 或 `node_id`）。dependencies 不可用时退化为正文顶部 `Blocked by: #<n>, #<n>`。所有阻塞项关闭后该工单才算可开工。
- **统计未关闭阻塞项**：`gh api repos/<owner>/<repo>/issues/<n> --jq .issue_dependencies_summary.blocked_by`（仅统计未关闭项，0 表示可开工）。`gh issue view <n> --json blockedBy` 会包含已关闭项，需过滤：`--jq '[.blockedBy.nodes[] | select(.state == "OPEN")] | length'`。
- **评论**：`gh issue comment <number> --body "..."`。
- **加 / 去标签**：`gh issue edit <number> --add-label "..."` / `--remove-label "..."`。
- **关闭**：先用 `gh issue comment <number> --body "..."` 说明原因，再 `gh issue close <number>`。对已关闭 issue 使用 `gh issue close --comment` 会丢弃评论但仍以 0 退出。

仓库由 `git remote -v` 推断，在克隆目录内 `gh` 会自动识别。

只向用户自己的仓库提交 issue。不要向 `mattpocock/skills`（这些 skill 的出处）提 issue，除非用户拥有该仓库。fork 场景下 `gh` 可能默认指向 upstream 父仓库，因此每个会话首次 `gh issue create` 前用 `gh repo view --json nameWithOwner` 确认目标仓库。

## PR 作为 triage 入口

**PR 作为请求入口：否。** _（若本仓库把外部 PR 视作 feature request，改为 `yes`；`/triage` 会读这个开关。）_

设为 `yes` 时，PR 与 issue 走同一套标签和状态，使用对应的 `gh pr` 命令：

- **读取 PR**：`gh pr view <number>`，再 `gh pr view <number> --comments`（非 TTY 下 `--comments` 只输出评论），diff 用 `gh pr diff <number>`。
- **列出待 triage 的外部 PR**：`gh api --paginate 'repos/{owner}/{repo}/pulls?state=open' --jq '.[] | select(.author_association | IN("OWNER","MEMBER","COLLABORATOR") | not) | {number, title, author: .user.login, author_association, labels: [.labels[].name]}'`。
- **评论 / 打标签 / 关闭**：`gh pr comment`、`gh pr edit --add-label`/`--remove-label`、`gh pr close`。

GitHub 的 issue 与 PR 共用一个编号空间，裸写 `#42` 可能是 PR：先用 `gh pr view 42` 解析，失败再退回 `gh issue view 42`。

## 当 skill 要求「发布到 issue tracker」

创建 GitHub issue。

## 当 skill 要求「取出相关工单」

按上面 **读取 issue** 执行。

## Wayfinding 操作

供 `/wayfinder` 使用。**地图**是一个 issue，**工单**是它的子 issue。

- **地图**：单个 issue，标签 `wayfinder:map`，正文保存 Notes / Decisions-so-far / Fog。`gh issue create --label wayfinder:map`。
- **子工单**：以 GitHub 子 issue 关联到地图（见上文）。不支持子 issue 时，把子工单放进地图正文的任务列表，并在子工单正文顶部写 `Part of #<map>`。标签为 `wayfinder:<type>`（`research`/`prototype`/`grilling`/`task`）。被认领后指派给执行的开发者。
- **阻塞**：见 **约定** 中的 **阻塞关系**。
- **前沿查询**：列出地图下未关闭的子 issue，剔除有未关闭阻塞项（见 **统计未关闭阻塞项**）或已有 assignee 的，按地图顺序取第一个。
- **认领**：`gh issue edit <n> --add-assignee @me`，作为会话的第一次写操作。
- **解决**：`gh issue comment <n> --body "<答案>"`，然后 `gh issue close <n>`，最后把上下文指针（要点 + 链接）追加到地图的 Decisions-so-far。
