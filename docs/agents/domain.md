# Domain Docs

工程 skill 探索本仓库时如何消费领域文档。

## 探索前先读这些

- 根目录 **`GLOSSARY.md`**；或
- 根目录 **`GLOSSARY-MAP.md`**（若存在）：它指向每个上下文各自的 `GLOSSARY.md`，读取与当前主题相关的那些。
- **`docs/adr/`**：读与即将改动的区域相关的 ADR。多上下文仓库还要检查 `src/<context>/docs/adr/`。

若上述文件不存在，**静默继续**：不要提示缺失，也不要主动建议先创建。`/domain-modeling` skill（经由 `/grill-with-docs` 与 `/improve-codebase-architecture` 触达）会在术语或决策真正定稿时按需创建。

## 文件结构

本仓库为单上下文：

```
/
├── GLOSSARY.md
├── docs/adr/
│   ├── 0001-....md
│   └── 0002-....md
└── internal/          ← 主要代码
```

多上下文仓库的标志是根目录存在 `GLOSSARY-MAP.md`，本仓库不使用该布局。

## 使用词汇表里的术语

输出中提及领域概念时（issue 标题、重构提案、假设、测试名），使用 `GLOSSARY.md` 中的定义，不要漂移到词汇表明确排除的同义词。

如果需要的概念尚未进入词汇表，这本身是个信号：要么你在发明项目不使用的语言（重新考虑），要么存在真实的空白（记下来交给 `/domain-modeling`）。

## 标记 ADR 冲突

若你的输出与既有 ADR 冲突，显式指出，而不是静默覆盖：

> _与 ADR-0007（event-sourced orders）冲突，但值得重开，因为……_
