# LoopForge 外部工作事项驱动改造需求文档

**项目名称：** LoopForge External Work Management Architecture  
**文档版本：** V1.1  
**改造方向：** 外部管理、内部执行、Git 仓库无关  
**首期管理平台：** 飞书多维表格

> V1.1 对齐外部工作事项驱动 spec 与实现：Work Item 复用 `issues` 作投影；业务状态、执行状态与业务审批分离；飞书长连接事件加对账兜底；仓库级加密服务凭证；存量清理与一次性切换。术语以根目录 `GLOSSARY.md` 为准，关键取舍见 `docs/adr/`。

---

# 1. 项目背景

## 1.1 项目现状

LoopForge 是一个面向 Agent 自动化研发的执行平台，提供 SOP 工作流、Agent 管理、Runtime、Runner、执行记录及 Trace 等能力。

目前系统以 GitHub 作为主要的研发协作管理平台，GitHub 不仅承担代码仓库管理，还承担了需求管理、Issue 管理、任务状态、审批及交付追踪等职责。

这种设计存在以下问题：

1. 系统对 GitHub 的依赖较重。
2. 需求管理与代码仓库管理耦合。
3. 更换管理平台需要修改核心业务逻辑。
4. 无法直接使用飞书多维表格作为统一的研发任务管理入口。
5. 系统内部与外部管理平台可能存在两套需求数据及状态，增加同步和维护成本。

本次改造需要重新划分系统职责，将研发协作管理与 Agent 执行能力彻底解耦。

## 1.2 改造目标

将 LoopForge 改造为一个由外部工作事项驱动的 Agent 自动化执行平台。

核心原则：

**外部管理，内部执行，代码仓库独立。**

具体目标：

1. 完全移除 LoopForge 内部独立的需求管理体系。
2. 由外部管理平台承担全部正式需求和研发任务管理。
3. 首期通过飞书多维表格接入需求、任务、状态、审批和交付管理。
4. 在 LoopForge 内部建立统一的 Work Item 领域模型。
5. 不同管理平台通过 Adapter 接入，不影响核心执行引擎。
6. Git 仓库操作与管理平台彻底解耦。
7. 支持任意符合标准 Git 协议的代码仓库。
8. 保留 LoopForge 原有 SOP、Agent、Runner、Runtime、Trace 等核心能力。

## 1.3 非目标

本次改造不计划：

- 在 LoopForge 内重新实现需求管理系统。
- 在 LoopForge 内重新实现项目管理系统。
- 在 LoopForge 内维护第二套正式需求列表。
- 强制要求 Git 仓库必须托管在 GitHub。
- 强制要求代码托管平台提供 Issue、PR、MR 等功能。
- 将所有执行日志和运行产物同步到飞书多维表格。
- 将飞书多维表格作为 LoopForge 的底层数据库。

---

# 2. 核心架构设计

## 2.1 总体架构

系统调整为五个相对独立的层次：

```text
┌──────────────────────────────────────────────┐
│              外部工作管理平台                 │
│                                              │
│       飞书多维表格 / GitHub / Jira / ...      │
└──────────────────────┬───────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────┐
│           Management Adapter Layer           │
│                                              │
│  Feishu Adapter / GitHub Adapter / Jira ...  │
│                                              │
│   字段映射 / 状态映射 / 事件订阅 / 对账      │
└──────────────────────┬───────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────┐
│             Work Item Domain                 │
│                                              │
│   Work Item (kind: requirement / issue)     │
│              + Delivery                      │
│                                              │
│  统一对象模型 / 生命周期 / 关联关系 / 事件    │
└──────────────────────┬───────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────┐
│             LoopForge Core                   │
│                                              │
│  SOP / Agent / Runtime / Runner / Trace      │
│                                              │
│  任务调度 / 工作流执行 / 运行监控 / 验收      │
└──────────────────────┬───────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────┐
│              Git Repository Layer            │
│                                              │
│  Git URL / Credentials / Branch / Commit     │
│  Clone / Fetch / Checkout / Push / Merge     │
│                                              │
│  Optional: GitHub PR / GitLab MR / Gitea PR  │
└──────────────────────────────────────────────┘
```

## 2.2 系统职责划分

### 外部管理平台

负责全部正式研发工作事项的管理。

包括：

- 需求创建及维护。
- 需求优先级。
- 任务拆分与任务依赖。
- 任务负责人。
- 业务状态。
- 人工审批。
- 人工验收。
- 交付追踪。
- 研发工作看板。

首期使用飞书多维表格实现。

### Management Adapter

负责屏蔽不同管理平台之间的数据和接口差异。

包括：

- 外部记录读取与分页列举。
- 外部状态与交付摘要回写（连接级默认关闭）。
- 外部字段与内部语义字段映射。
- 外部业务状态到状态动作的映射。
- 长连接事件接收与处理。
- 增量/全量对账。
- 连接自检与就绪状态。

首期不包含从 LoopForge 创建外部记录，也不建立外部用户到 LoopForge 用户的身份映射；assignee / approver 只作展示与审计字段。

### Work Item Domain

负责定义 LoopForge 内部统一的工作事项投影与执行上下文。

它不是第二套需求管理系统：只保存执行所需的投影数据。核心执行引擎只依赖该领域模型，不直接依赖飞书、GitHub 或 Jira 的数据结构。

### LoopForge Core

负责：

- SOP 调度。
- Agent 执行。
- Runtime 管理。
- Runner 调用。
- 执行过程控制。
- 自动化测试。
- 代码审查。
- 执行日志。
- Trace 证据。
- 执行结果管理。

### Git Repository Layer

负责代码仓库相关操作。

不得依赖 GitHub Issue、GitHub OAuth 或 GitHub PR 才能完成基础代码操作。

---

# 3. Work Item 统一领域模型

## 3.1 设计目的

Work Item 是统一的工作事项抽象。

它不是新的需求管理系统，也不是将所有事项强制合并到一张数据库表。

它的主要作用是将外部管理平台的数据转换为 LoopForge 能够识别和处理的统一业务对象。

例如：

```text
飞书多维表格
       │
       ▼
Feishu Adapter
       │
       ▼
Work Item Domain
       │
       ├── Work Item（kind: requirement / issue）
       └── Delivery
       │
       ▼
SOP / Agent / Runner
```

## 3.2 核心对象

### Work Item

表示一条 External Record 在 LoopForge 内的投影与执行上下文，不是第二套需求管理。

内部复用 Issue 模型与 `issues` 表落库，通过 `kind` 区分 `requirement`（业务需求）与 `issue`（可执行研发任务），不新建 `work_items` 表。

主要字段：

| 字段 | 说明 |
|---|---|
| id | 内部唯一 ID |
| kind | `requirement` 或 `issue` |
| external_ref.business_key | 业务唯一键，正式 Work Item 必填 |
| external_ref.record_id | 外部记录当前引用，仅作展示与回读 |
| external_ref.external_url | 外部记录链接 |
| external_ref.external_state | 外部业务状态快照（只读权威值） |
| external_ref.orphaned | 外部记录已删除；停止自动触发，保留执行证据 |
| title / description / priority / acceptance_criteria | 外部字段快照，作为执行输入 |
| status | 本地执行生命周期，不承载外部业务状态 |
| repository_ids | 关联仓库 |
| latest_task_id | 最近执行实例 |
| external_pending_action | 运行中收到的待执行状态动作 |

Requirement / Issue 是 Work Item 的两种 kind，不是并列实体。

### Business Approval

业务审批完全由外部管理平台承载。LoopForge 不定义审批实体、不建 `approvals` 表，也不校验审批权限；审批结果以外部业务状态的观察形式进入投影。

### Execution Gate

SOP 内置的人工门禁，只控制执行，不承担业务审批。

### Delivery

表示一次研发交付。

主要字段：

| 字段 | 说明 |
|---|---|
| id | 内部唯一 ID |
| work_item_id | 关联 Work Item |
| project_id | 所属项目 |
| repository_id | 关联代码仓库 |
| branch | 交付分支 |
| commit_sha | Commit SHA |
| provider_url | 可选 PR/MR 地址，仅作展示 |
| checks | 测试与审查结果 |
| approval | 观察到的外部审批结果摘要 |
| state | draft / running / succeeded / failed / cancelled |

Delivery 不依赖 PR/MR；`mr_delivery_states` 只保留 provider 侧明细并关联 Delivery。交付摘要按配置回写外部管理平台。

## 3.3 领域模型设计要求

1. 所有对象使用 LoopForge 自身的唯一 ID。
2. 外部平台 ID 仅作为关联标识，不作为内部主键。
3. 不同管理平台可以拥有不同的字段结构。
4. 领域模型只保存执行所必需的数据。
5. 不复制外部管理平台的全部业务数据。
6. 对象之间通过明确的 ID 建立关联。
7. 外部对象删除、归档、权限变化必须有明确的处理策略。
8. 保留外部记录 URL，方便从 LoopForge 跳转回管理平台。
9. 正式 Work Item 必须绑定 External Record；只有 Formal Work Item 参与正式交付统计。
10. 外部记录删除后标记为 orphaned，停止自动触发，不级联删除 Run/Trace。

---

# 4. Management Adapter 管理平台适配层

## 4.1 设计要求

必须将管理平台接入逻辑从 LoopForge 核心执行引擎中抽离。首期实际边界如下：

```go
type Adapter interface {
    ValidateConnection(ctx context.Context, conn ManagementConnection) (ManagementConnectionReadiness, error)

    GetWorkItem(ctx context.Context, conn ManagementConnection, ref ExternalRef) (ExternalRecord, error)

    ListWorkItems(ctx context.Context, conn ManagementConnection, cursor string, limit int) (ExternalRecordPage, error)

    UpdateExternalStatus(ctx context.Context, conn ManagementConnection, ref ExternalRef, status string) error

    UpdateDeliverySummary(ctx context.Context, conn ManagementConnection, ref ExternalRef, summary ExternalDeliverySummary) error

    SubscribeEvents(ctx context.Context, conn ManagementConnection) error
}
```

该边界不包含创建外部记录，也不包含创建审批或查询审批结果。正式记录只在外部平台创建；审批结果作为外部业务状态被观察。

## 4.2 首期实现飞书适配器

实现：

```text
FeishuWorkManagement
```

通过飞书开放平台 API 访问多维表格。

需要支持：

- 复用现有通知 Bot 的加密应用凭证，不新建第二套飞书凭证体系。
- 单连接配置：base app_token、table_id、默认 Project、允许回写开关、同步游标。
- 字段映射与状态映射配置。
- 单条记录读取与分页列举。
- 外部状态回写（连接级默认关闭）与交付摘要回写。
- 长连接事件订阅：`drive.file.bitable_record_changed_v1` 与 `drive.file.bitable_field_changed_v1`。
- 事件先落 Event Inbox 再在 3 秒内 ack，异步处理。
- 增量对账、全量对账与「立即同步」。
- API 限流处理：读 20 次/秒、写 50 次/秒，429 按 `x-ogw-ratelimit-reset` 等待。
- 失败重试、字段缺失/类型变化告警、只读同步表拒绝写入。
- 首期不从 LoopForge 创建外部记录，也不接入飞书审批 OpenAPI。

## 4.3 字段映射

不能将飞书字段名称硬编码在 LoopForge 中。

应支持配置映射：

| 内部语义字段 | 飞书字段示例 | 必填 |
|---|---|---|
| business_key | 业务编号 | 是 |
| title | 任务名称 | 是 |
| status | 任务状态 | 是 |
| description | 任务描述 | 否 |
| priority | 优先级 | 否 |
| assignee | 负责人（仅展示） | 否 |
| acceptance_criteria | 验收标准 | 否 |
| repository | 关联仓库 | 否 |
| external_url | 外部链接 | 否 |

不同团队可能使用不同的多维表格结构，因此字段映射必须具备可配置性。映射以字段名编辑，内部维护 `field_id → 字段名` 缓存；字段变更事件触发缓存刷新。

`business_key` 是 Work Item 的稳定身份，`record_id` 只是外部记录的当前引用，不能替代业务唯一键。缺少业务键时降级为 `record_id` 并告警。

## 4.4 事件驱动机制

外部管理平台发生变化时，LoopForge 应能及时感知。

统一内部事件：

```text
WorkItemCreated
WorkItemUpdated
WorkItemAssigned
WorkItemStatusChanged
DeliveryCompleted
```

审批结果不单独建事件实体，以外部业务状态的观察形式进入。

处理流程：

```text
飞书记录发生变化
       │
       ▼
长连接事件（至少一次投递）
       │
       ▼
Event Inbox（event_id 唯一，先落库）
       │
       ▼
3 秒内 ack
       │
       ▼
异步串行处理（同一 record_id 串行，回读记录）
       │
       ▼
Management Adapter
       │
       ▼
Work Item 投影
       │
       ▼
状态映射动作
       │
       ▼
启动 SOP / 暂停 / 取消 / 重启 / 回写 / 观察
```

不能仅依赖事件保证一致性。对账策略：

- 事件实时处理。
- 每 15 分钟增量对账。
- 每日全量对账。
- 配置页「立即同步」。
- 增量对账按排序/分页拉取，并在本地按 `last_modified_time` 截断；飞书日期过滤只到天，不能依赖毫秒级过滤。
- `record_deleted` 事件或全量对账把 Work Item 标记为 orphaned，停止自动触发，不删除 Run/Trace。
- 公式与查找引用字段不触发事件、也不在事件体中；事件到达后回读记录。
- 回写必须带审计，并识别本系统回写产生的事件，避免回环。

事件处理必须满足：

- 幂等性，按 `event_id` 去重。
- 同一记录串行处理，避免乱序覆盖。
- 失败重试与告警。
- 事件处理记录与同步游标。
- 字段类型变化时暂停自动回写并告警。

---

# 5. 需求管理功能彻底外置

## 5.1 核心要求

LoopForge 不再提供独立的正式需求管理能力。

飞书多维表格是首期唯一正式需求和研发任务管理入口。

LoopForge 不允许通过自己的需求列表创建第二套正式需求。

## 5.2 原有功能调整

| 原有功能 | 改造要求 |
|---|---|
| 需求列表 | 改为执行中心的 Work Item 只读视图，不再是需求管理入口 |
| 新建需求 | 移除；正式 Work Item 只在外部管理平台创建 |
| 编辑需求 | 移除；由外部管理平台负责 |
| 删除需求 | 移除；由外部管理平台负责 |
| 需求状态 | 外部业务状态由外部平台管理，与本地执行状态分列展示 |
| 需求详情 | 改为外部工作事项执行详情 |
| 需求派发 | 保留，改为外部事项状态触发或手工派发 |
| 需求统计 | 改为执行与交付统计，默认排除 Temporary Run |
| 外部 Tracker | 收敛到 Management Adapter；工蜂/TAPD 保留兼容同步，GitHub 退出正式流程 |
| 项目与仓库配置 | 保留，独立于需求管理 |
| rooms/spaces | 保留协作能力；新建需求改为选择已同步的 Work Item |

注意：

移除需求列表不意味着删除所有与 Requirement 相关的内部数据结构。

内部仍需要保存必要的外部事项投影、关联关系和执行上下文，但它只是投影，不是第二套权威来源。

## 5.3 正式需求的创建入口

正式工作事项必须来自外部管理平台。

例如：

```text
飞书多维表格创建需求
          │
          ▼
LoopForge 接收到需求
          │
          ▼
识别事项类型
          │
          ▼
匹配对应 SOP
          │
          ▼
执行任务
```

LoopForge 内部不得在没有外部事项关联的情况下创建正式研发任务。

首期不从 LoopForge 向外部创建新的正式记录（任务拆分、自动建单均不在范围内）。同步只负责读取外部权威记录、建立投影并按状态映射触发执行。

## 5.4 AI 工作台调整

保留 AI 工作台，但调整其职责。

允许：

- 自由对话。
- 代码探索。
- 技术方案分析。
- 临时测试。
- 查询执行记录。
- 查询 Agent 状态。
- 查询已有工作事项。

限制：

- 不允许直接创建正式需求，也不向助手暴露本地建单写工具。
- 不允许绕过外部管理平台创建正式研发任务。
- 正式开发任务必须关联外部 Work Item。
- 正式执行结果必须能够关联外部事项。
- 临时探索只能启动 Temporary Run，并显式标记 Origin（如 `assistant_scratch`）。

临时探索任务不落 Issue，不进入正式需求管理和交付统计。

---

# 6. 状态管理设计

## 6.1 外部业务状态与内部执行状态分离

必须区分两套状态。

### 外部业务状态

由飞书负责。

例如：

```text
待分析
待开发
开发中
待验收
已完成
已取消
```

具体状态由多维表格配置决定，不在 LoopForge 中硬编码。

### 内部执行状态

由 LoopForge 负责，分为两个层次：

1. Work Item 本地生命周期（`Issue.Status`）：`backlog`、`todo`、`in_progress`、`in_review`、`done`、`closed`、`blocked`、`cancelled`。
2. Run 状态：`queued`、`running`、`waiting`、`succeeded`、`failed`、`cancelled`。

内部执行状态是派生值，由 `Issue.Status` 加最近 Task/Run 状态实时计算；不新增 `execution_status` 字段，也不新增第二套状态机。

Run 没有 `paused` 状态。暂停请求表示为 `PauseRequested`，运行状态保持 `waiting`。`waiting_approval` 是节点级状态（Execution Gate 等待人工确认），不是 Run 状态。

两套状态不得直接共用同一个字段。外部业务状态只存在 `ExternalRef.ExternalState`，`Issue.Status` 不再被外部状态覆盖。

例如：

```text
飞书业务状态：开发中

LoopForge 投影：
    Issue.Status: in_progress
    Run.Status: running 或 waiting
    Node.Status: waiting_approval（仅执行门禁节点）
```

外部业务状态不应因为内部某个 Agent 节点执行成功就自动变为已完成。

必须根据工作流和业务规则决定何时更新外部状态。

## 6.2 状态映射

允许按 `(connection, table, external_status)` 配置映射动作。动作枚举：

| 动作 | 行为 |
|---|---|
| `start_sop` | 启动映射指定的 SOP（映射必须配置工作流） |
| `ignore` | 只更新投影，不触发执行 |
| `pause` | 请求当前 Run 暂停（`PauseRequested` + `waiting`） |
| `cancel` | 取消当前 Run |
| `restart` | 先取消旧 Run，再重新派发 |
| `writeback` | 把派生状态回写外部，不触发执行 |

触发规则：

- 只在状态进入某个值的变更时触发，不在每次轮询/同步时重复触发。
- 未知状态只同步入库并记录观测，不触发。
- 同一 Work Item 同时最多一个 active Run；运行中的状态变化只更新投影与待执行动作，不重复派发。
- 状态回退只记录事件与告警，不自动回滚代码。
- 外部业务状态默认不写回，仅动作显式声明时才写。

---

# 7. Git Repository 通用化

## 7.1 改造目标

彻底取消 GitHub 作为必需代码托管平台的前提。

代码仓库只需要满足标准 Git 操作要求。

首期支持任何满足标准 Git 协议的主机：

- GitHub / GitHub Enterprise。
- GitLab。
- Gitea / Gitee。
- 自建 Git Server。
- 其他支持 HTTPS 或 SSH 的标准 Git 服务。

基础 Git 操作不依赖代码托管平台的 Issue、PR/MR 或 OAuth。首期不为 Gitea/Gitee 新增 PR/MR 写适配器。

## 7.2 仓库配置

每个代码仓库应独立配置：

| 字段 | 说明 |
|---|---|
| repository_id | 内部仓库 ID |
| project_id | 关联项目 |
| name | 仓库名称 |
| provider | 可选平台类型，仅用于展示与可选 API 路由 |
| clone_url | Git Clone 地址 |
| web_url | 仓库网页地址 |
| default_branch | 默认分支 |
| service_credential_id | 仓库级加密服务凭据引用 |
| gongfeng_project_id | 工蜂兼容身份字段 |

`provider` 不应成为执行 Git 基础操作的必要条件。

仓库级服务凭证存放在 `repository_git_credentials`：

| 字段 | 说明 |
|---|---|
| id | 凭据 ID（即 credential_id） |
| repository_id | 所属仓库 |
| name | 凭据名称，仓库内唯一 |
| kind | SSH 或 HTTPS |
| host | 目标 Git 主机 |
| username | 可选用户名 |
| secret | AES-256-GCM 加密后的密钥或令牌，永不返回明文 |
| created_at / updated_at | 审计时间 |

执行时优先使用仓库级服务凭证，使用户级凭证（`user_git_http_credentials` / `user_git_ssh_keys`）保留为可选覆盖，不强制迁移现有配置。

## 7.3 Git 基础能力

统一抽象：

```go
type GitRepository interface {
    Clone(ctx context.Context, url string, target string) error

    Fetch(ctx context.Context) error

    Checkout(ctx context.Context, branch string) error

    CreateBranch(ctx context.Context, branch string) error

    Commit(ctx context.Context, message string) error

    Push(ctx context.Context, branch string) error

    GetCommit(ctx context.Context) (string, error)
}
```

实际实现可优先复用现有 Git 操作模块。

需要支持：

- HTTPS 认证。
- SSH Key 认证。
- 私有仓库。
- 分支管理。
- Commit 管理。
- Push / Fetch。
- 仓库凭证安全存储。
- 仓库访问权限检查。

## 7.4 PR/MR 独立为可选能力

PR/MR 不是 Git 协议的一部分。

因此必须单独设计：

```go
type CodeReviewProvider interface {
    CreatePullRequest(ctx context.Context, req PullRequest) error

    GetPullRequestStatus(
        ctx context.Context,
        id string,
    ) (*PullRequestStatus, error)

    MergePullRequest(ctx context.Context, id string) error
}
```

支持的情况下可对接：

- GitHub / GitHub Enterprise Pull Request（读写）。
- GitLab Merge Request（只读）。
- 首期不新增 Gitea/Gitee PR 适配器。

没有 PR/MR API 的仓库，仍然必须能够完成：

```text
Clone
  ↓
Create Branch
  ↓
Agent Development
  ↓
Commit
  ↓
Push
  ↓
飞书审批
  ↓
交付记录
```

不得因为没有 PR/MR 功能而阻断整个研发流程。

## 7.5 Git 与飞书审批的关系

飞书承担业务审批。LoopForge 不定义审批权限，也不通过审批 API 发起审批。

例如：

1. Agent 完成代码开发。
2. LoopForge 执行自动化测试并生成 Delivery。
3. 按配置将 Commit、测试结果和代码变更摘要回写飞书。
4. 外部业务状态流转到待审批；审批在飞书完成。
5. LoopForge 观察到审批通过的外部状态后，按状态映射继续执行。
6. SOP 的执行门禁（Execution Gate）只控制执行，不代替业务审批。
7. 执行合并或推送目标分支，更新 Delivery 并回写交付摘要。

如果使用 GitHub/GitLab 等平台，也可以根据配置创建 PR/MR，但不能把它作为必须步骤。

---

# 8. Delivery 交付管理

## 8.1 设计要求

交付管理不再依赖 GitHub PR 或 Issue。

LoopForge 应建立独立的 Delivery 记录。

一条交付记录至少关联：

- 外部 Work Item。
- 所属项目。
- 内部执行实例。
- 代码仓库。
- 代码分支。
- Commit SHA。
- 测试结果。
- 审查结果。
- 观察到的外部审批结果。
- 最终交付状态。

Delivery 落在 `deliveries` 表；`mr_delivery_states` 只保留 provider 侧明细并关联 Delivery。`issues.payload` 中的旧交付字段逐步弃用。

## 8.2 交付流程

```text
Agent 开发完成
      │
      ▼
自动化测试
      │
      ▼
代码审查
      │
      ▼
生成 Delivery
      │
      ▼
回写交付摘要到飞书（按配置）
      │
      ▼
外部业务状态进入待审批
      │
      ▼
飞书完成业务审批
      │
      ▼
LoopForge 观察审批结果状态
      │
      ▼
执行合并或目标分支推送
      │
      ▼
更新 Delivery 状态
      │
      ▼
回写交付摘要（按配置）
```

具体节点应由 SOP 决定，而不是写死在 Git Provider 中。没有 PR/MR 的仓库同样必须完成完整交付流程。

---

# 9. 数据一致性和同步机制

## 9.1 数据所有权

必须遵守以下原则：

| 数据 | 权威数据源 |
|---|---|
| 正式需求 | 外部管理平台 |
| 正式研发任务 | 外部管理平台 |
| 负责人及业务优先级 | 外部管理平台 |
| 业务状态 | 外部管理平台 |
| 审批结论 | 外部管理平台 |
| 执行状态 | LoopForge |
| SOP 定义 | LoopForge |
| Agent 配置 | LoopForge |
| Run / Trace | LoopForge |
| 代码内容 | Git 仓库 |
| Commit | Git 仓库 |
| 交付过程记录 | LoopForge |
| 最终交付摘要 | 外部管理平台同步副本 |

## 9.2 本地数据投影

LoopForge 允许保存外部 Work Item 的必要数据副本。

但必须明确：

- 本地副本不具有独立需求管理权。
- 外部记录是正式需求的权威来源。
- 本地数据用于执行上下文、关联查询和运行恢复。
- 不应在本地副本中直接修改外部业务字段。
- 外部修改需要通过 Adapter 同步。
- Work Item 身份以 `(connection, table, business_key)` 为准，`record_id` 只作当前记录引用。
- Formal Work Item 由「是否绑定 External Record」派生；只有 Formal Work Item 进入正式派发、交付统计与默认执行视图。

## 9.3 同步异常处理

需要考虑：

- 飞书 API 不可用。
- 事件丢失或乱序；Event Inbox 按 `event_id` 去重，同一记录串行处理。
- 事件重复投递（至少一次语义）。
- 外部记录被删除（标记 orphaned，停止自动触发）。
- 字段缺失、重命名或类型变化（暂停自动回写并告警）。
- 外部状态与内部执行状态不一致（分列展示，不互相覆盖）。
- 未知外部状态（只同步，不触发）。
- 回写产生的事件形成回环（识别本系统写入）。
- 同步表只读（直接返回可读错误）。
- API 调用限流（429 按 `x-ogw-ratelimit-reset` 等待）。
- 网络中断。
- 执行过程中外部事项被取消。

系统必须能够识别异常、记录原因、重试和恢复。

执行过程不能因为一次飞书 API 调用失败就丢失运行状态。回写失败进入 outbox 重试，不阻断 Run。

---

# 10. 前端功能调整

## 10.1 导航调整

移除原有需求列表导航。

建议调整为：

```text
LoopForge
├── 工作总览
├── 执行中心
├── SOP 工作流
├── Agent 管理
├── 代码仓库
├── 审批中心
├── 统计分析
└── 系统配置
```

### 工作总览

展示：

- 当前执行中的任务。
- 等待执行的任务。
- 执行失败的任务。
- 等待外部业务审批的事项与内部执行门禁。
- 最近完成的交付。
- Agent 运行情况。

### 执行中心

作为正式工作事项的执行视图。

展示：

- 外部事项标题。
- 外部事项 ID（业务唯一键）。
- 来源管理平台。
- 外部事项链接。
- 外部业务状态。
- 内部执行状态（派生值）。
- 当前 SOP。
- 当前 Agent。
- 当前 Run。
- 最近执行时间。

Work Item 为只读视图，不提供需求编辑。点击事项进入执行详情，而不是进入需求编辑页面。

### 代码仓库

负责仓库注册和管理。

支持：

- 添加 Git 仓库。
- 配置访问凭证。
- 测试连接。
- 设置默认分支。
- 关联项目。
- 查看最近执行记录。

### 审批中心

只读视图，展示：

- 外部待审批事项（Business Approval）。
- 内部执行门禁（Execution Gate）。

不提供审批操作。业务审批由外部管理平台承接，LoopForge 只观察外部状态，不发起审批请求。

### 系统配置

新增：

- 管理平台连接（单连接）。
- 飞书应用凭证引用。
- 多维表格 base app_token 与 table_id。
- 字段映射（必填 business_key）。
- 状态映射。
- 连接自检与「立即同步」。
- 允许回写开关。
- 仓库级 Git 服务凭证。
- Git Provider 可选配置。

---

# 11. 原有功能改造清单

开发阶段必须逐项检查现有代码。

| 模块 | 改造要求 |
|---|---|
| Tracker | 兼容同步收敛到 Management Adapter；GitHub 退出正式流程 |
| GitHub Issue | 从正式需求管理流程中移除 |
| GitHub OAuth | 不再作为系统运行前提 |
| GitHub PR | 改为可选 Code Review Provider |
| Work Item | 复用 Issue 模型作投影，`kind` 区分 requirement / issue |
| Business Approval | 外部平台承载，不建实体、不建表 |
| Execution Gate | 保留为 SOP 内执行门禁 |
| SOP Engine | 保留，消费状态映射动作与统一内部事件 |
| Agent | 保留 |
| Runner | 保留 |
| Runtime | 保留 |
| Trace | 保留 |
| Run | 保留；Temporary Run 标记 Origin |
| Delivery | 独立 `deliveries` 记录；PR/MR 可选 |
| AI 工作台 | 保留自由对话，正式任务必须关联外部事项，不再提供本地建单写工具 |
| 需求列表 UI | 改为执行中心 Work Item 只读视图 |
| 需求创建 UI | 移除 |
| 需求编辑 UI | 移除 |
| rooms/spaces | 保留协作，改为选择已同步 Work Item |
| 项目管理 | 保留必要的执行环境及仓库配置能力 |

注意：以上是目标架构要求，不代表可以不经源码分析就直接删除同名目录或数据库表。

开发前必须识别现有模块之间的调用关系，避免破坏原有 SOP、Run、Trace 和执行恢复能力。存量需求数据只通过 `migrations/043_external_work_cleanup.sql` 手动清理，不在启动路径自动迁移。

---

# 12. 开发实施阶段

## Phase 1：源码分析与架构解耦

目标：摸清现有 GitHub 依赖，明确改造边界。

工作内容：

1. 梳理 Tracker 接口和实现。
2. 梳理 GitHub Issue 的调用位置。
3. 梳理 GitHub PR 的调用位置。
4. 梳理 Requirement、Issue 等内部数据模型。
5. 梳理需求列表前后端调用链。
6. 梳理 AI 工作台创建需求的逻辑。
7. 梳理 SOP 的触发机制。
8. 抽象 Work Item Domain。
9. 抽象 Work Management Interface。
10. 抽象 Git Repository Interface。

交付物：

- 源码依赖分析。
- 新架构设计。
- 数据模型调整方案。
- 接口定义。

## Phase 2：Work Item Domain 实现

目标：建立统一内部工作事项模型。

工作内容：

1. 实现 Work Item 投影（复用 Issue 模型，`kind` 区分 requirement / issue）。
2. 实现 ExternalRef 身份：business key、record id、外部状态、orphaned。
3. 实现 Delivery。
4. 不实现 Approval 实体；业务审批保持外部承载。
5. 实现内部事件模型。
6. 实现外部业务状态与本地执行状态隔离，执行状态按需派生。
7. 实现状态映射动作与派发集成。

## Phase 3：飞书 Adapter 实现

目标：完成飞书多维表格接入。

工作内容：

1. 复用通知 Bot 的加密飞书应用凭证。
2. 多维表格连接配置与自检。
3. 字段映射与状态映射。
4. 单条记录读取与分页列举。
5. 外部状态回写与交付摘要回写（默认关闭）。
6. 长连接事件订阅。
7. Event Inbox、3 秒内 ack、按 event_id 去重、同一记录串行。
8. 增量/全量对账与「立即同步」。
9. 429 限流、失败重试、只读同步表与字段类型变化处理。
10. 事件与 SOP 触发集成。

## Phase 4：Git 通用化

目标：解除 GitHub 代码托管依赖。

工作内容：

1. 统一仓库配置。
2. 实现通用 Git 操作。
3. 支持 SSH Key。
4. 支持 HTTPS 凭证。
5. 支持私有仓库。
6. 将 PR/MR 能力独立抽象。
7. 适配可选 Git Provider。
8. 完成非 GitHub 仓库测试。

## Phase 5：前端改造

目标：移除内部需求管理功能。

工作内容：

1. 需求列表改为执行中心 Work Item 只读视图。
2. 移除内部正式需求创建和编辑。
3. 改造执行中心（外部业务状态 + 内部执行状态）。
4. 改造工作总览。
5. 新增管理平台配置与立即同步。
6. 新增飞书字段映射与状态映射配置。
7. 审批中心改为只读视图。
8. 改造代码仓库配置与仓库级服务凭证。
9. 调整 AI 工作台的正式任务入口，临时探索走 Temporary Run。

## Phase 6：集成测试与交付

目标：验证整体架构。

工作内容：

1. fake 飞书边界下的记录同步测试。
2. 外部事项投影、业务键冲突与删除测试。
3. SOP 自动触发与状态映射测试。
4. Agent 自动执行与并发触发测试。
5. 重复/乱序/丢失事件与对账恢复测试。
6. 外部审批状态观察与 Execution Gate 测试。
7. Git 通用操作与仓库级凭证测试。
8. 交付结果回写与防回环测试。
9. 飞书 429/5xx、只读表、字段类型变化测试。
10. 存量清理 SQL 的 MySQL 测试（dry-run、备份、幂等、保留执行证据）。
11. 完整端到端测试。

---

# 13. 验收标准

## 13.1 外部管理验收

- [ ] LoopForge 不再提供独立正式需求列表。
- [ ] LoopForge 不再允许创建独立正式需求。
- [ ] 飞书可以作为正式 Work Item 入口。
- [ ] 飞书事项可以被 LoopForge 识别为投影。
- [ ] 外部事项可以关联内部执行实例。
- [ ] 外部业务状态与内部执行状态分离，执行状态为派生值。
- [ ] 业务审批由外部承载，LoopForge 不建审批实体、不提供审批操作。
- [ ] 执行结果可以按配置回写飞书。
- [ ] 外部事项取消后可以按规则停止执行。
- [ ] 同步失败可以恢复。
- [ ] 不存在未经外部关联的正式研发任务。

## 13.2 架构解耦验收

- [ ] 核心执行引擎不直接调用飞书 API。
- [ ] 核心执行引擎不直接依赖 GitHub Issue API。
- [ ] 管理平台通过 Adapter 接入。
- [ ] Work Item 领域模型独立于外部平台。
- [ ] 新增管理平台不需要修改 SOP 核心执行逻辑。
- [ ] 状态映射可配置。
- [ ] 事件处理支持幂等。

## 13.3 Git 通用化验收

- [ ] 不使用 GitHub 也可以正常运行。
- [ ] 支持标准 Git Clone。
- [ ] 支持 Fetch。
- [ ] 支持 Checkout。
- [ ] 支持创建分支。
- [ ] 支持 Commit。
- [ ] 支持 Push。
- [ ] 支持私有仓库。
- [ ] 支持 SSH 和 HTTPS 认证。
- [ ] 没有 PR/MR 的仓库也能完成交付流程。
- [ ] PR/MR 能力为可选扩展。

## 13.4 核心能力回归验收

- [ ] 原有 SOP 正常运行。
- [ ] 原有 Agent 正常运行。
- [ ] 原有 Runner 正常运行。
- [ ] 原有 Runtime 正常运行。
- [ ] 原有 Trace 正常记录。
- [ ] 原有执行恢复能力不受影响。
- [ ] AI 工作台自由对话能力保留。

## 13.5 切换与清理验收

- [ ] 清理 SQL 的 dry-run 输出与预期行数一致。
- [ ] 清理脚本先备份、后删除，可重复执行。
- [ ] 清理后 tasks / runs / run_events / Trace 仍可查看，允许 issue_id 悬空。
- [ ] 不删除业务表结构。
- [ ] 飞书同步产生的 Work Item 投影在重复执行清理脚本时被保留。
- [ ] 发布/部署文档包含备份、执行顺序与回滚窗口说明。

---

# 14. 关键架构约束

以下约束属于本次改造的强制要求。

**约束一：外部管理平台是正式需求的唯一权威来源。**

LoopForge 不得保留第二套可独立维护的正式需求管理体系。

**约束二：Work Item 是内部领域模型，不是第二套管理平台。**

内部模型只负责统一对象表达、执行关联及生命周期，不承担完整的项目管理功能。

**约束三：核心执行引擎不得依赖具体管理平台。**

不得在 SOP、Agent、Runner 等核心模块中直接出现飞书、GitHub、Jira 等平台专属 API 调用。

**约束四：Git 仓库与管理平台独立。**

不得要求代码仓库与需求管理平台属于同一家服务商。

**约束五：PR/MR 是可选能力。**

没有 PR/MR API 的 Git 仓库也必须可以完成完整的代码开发和交付。

**约束六：执行状态与业务状态分离。**

外部业务状态和内部执行状态分别管理，通过可配置的状态映射在变更时触发动作；两者不是同一个状态机，也不互相覆盖。

**约束七：执行证据留在 LoopForge。**

Trace、Run、日志、执行快照和详细产物不应全部复制到飞书。

**约束八：所有外部同步必须可恢复。**

任何外部 API 故障都不能导致内部执行数据丢失。

---

# 15. 最终产品定位

本次改造完成后，LoopForge 的产品定位为：

**一个由外部工作事项驱动的、可治理、可追踪、可扩展的 Agent 自动化执行平台。**

其核心职责是：

- 接收外部管理平台的工作事项。
- 将工作事项转换为统一内部模型。
- 根据规则选择对应 SOP。
- 调度 Agent 执行任务。
- 管理执行环境和代码仓库。
- 自动完成开发、测试及交付。
- 在必要节点观察外部业务审批或等待内部执行门禁。
- 记录完整执行证据。
- 按配置将执行结果回写外部管理平台。

最终架构遵循：

```text
External Work Management
          │
          ▼
Management Adapter
          │
          ▼
Work Item Domain
          │
          ▼
LoopForge Execution Engine
          │
          ├── SOP
          ├── Agent
          ├── Runtime
          ├── Runner
          └── Trace
          │
          ▼
Git Repository
          │
          ▼
Delivery
          │
          ▼
External Work Management
```

**核心原则：需求在哪里管理，就在哪里维护；代码在哪里托管，就在哪里操作；LoopForge 只负责把工作事项转化为可执行、可验证、可追踪的 Agent 工作流。**
