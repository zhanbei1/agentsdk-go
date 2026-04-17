# agentsdk-go 优化能力使用指南（调用方）

更新时间：2026-04-17

本文面向 **agentsdk-go SDK 调用者**，汇总仓库内已实现的优化能力与推荐配置，帮助你在不同产品形态（CLI / HTTP / 内嵌服务）中稳定获得：

- **Skylark progressive 不再“复问遗忘”**
- **跨 session 的项目级持久记忆（L2）可检索**
- **MCP 工具描述/Schema 更省 token、更稳定**
- **工具输出不再淹没上下文（落盘 + 引用 + 摘要）**
- **结构化反思记录（便于上层做重试/降级/落盘）**

> 说明：仓库 v2 的“ground truth”在 `docs/refactor/PRD.md` 与 `docs/refactor/ARCHITECTURE-v2.md`。本文只解释“如何用”，不展开全部内部实现细节。

---

## 1. 一句话架构：四层记忆（L0-L3）

- **L0 瞬时工作记忆**：本次 run 的中间态（`middleware.State.Values` 等），不落盘。
- **L1 会话记忆**：`message.History`（单 session 多轮对话）。
- **L2 项目持久记忆**：写入 `.agents/memory/project_memory.jsonl`，可审计/可回滚/可被索引。
- **L3 检索增强层（Skylark）**：Bleve（可选向量），索引来自 Memory/Rules/Tools/Skills 以及 L2 文档。

你通常只需要：**启用 Skylark + 开启 L2 落盘 + 保持默认压缩阈值**，就能获得显著稳定性提升。

---

## 2. 快速开始（推荐默认）

### 2.1 推荐目录结构

项目内放置：

```text
.agents/
├── settings.json
├── rules/
├── skills/
└── memory/
    └── project_memory.jsonl   # 自动生成/追加
```

并确保 `.gitignore` 已忽略 Skylark 索引产物（仓库已内置相关规则）。

### 2.2 Go 侧最小配置（示例）

```go
rt, err := api.New(api.Options{
  ProjectRoot: "/path/to/project",
  // SystemPrompt / Model / Tools / MCPServers 等按你的应用注入

  Skylark: &api.SkylarkOptions{
    Enabled: true,

    // progressive 复问稳定性（建议保留默认即可）
    ProgressiveMiniMemoryMaxRunes: 800,
    HistoryPrefetchMaxHits:        5,
    HistoryPrefetchMaxRunes:       1800,

    // L2 项目记忆
    PersistProjectMemory: true,
    ProjectMemoryDir:     "/path/to/project/.agents/memory",
  },

  // 工具输出压缩（避免 history/token 爆炸）
  ToolOutputInlineMaxRunes:  4000,
  ToolOutputSnippetMaxRunes: 900,

  // 结构化反思（默认启用；显式写出便于阅读）
  ReflectionEnabled: ptr(true),
})
```

> 提示：若你不设置这些字段，SDK 会提供默认值；上面的代码只是把常用 knobs 显式化，便于团队讨论/调参。

---

## 3. Skylark progressive “复问不遗忘”

### 3.1 背景

Skylark progressive 的初衷是“按需检索”，但在真实使用中，用户经常用“上次/之前/继续/再问一次”复问；如果模型当轮没有主动触发 `retrieve_knowledge`，会出现“上次说过但这次忘了”的体验问题。

### 3.2 SDK 的解决方案（已实现）

- **Always-on mini memory（短注入）**  
  progressive 模式在 system prompt 末尾注入一个严格长度上限的“mini memory”，把最近结论/关键约束变成稳定的上下文钩子。

- **History prefetch（复问时自动取回 top-k）**  
  命中复问启发式时，SDK 会从最近 history 中检索相关 turn，直接注入到当轮用户输入里，降低模型“必须自觉调用检索”的依赖。

### 3.3 可调参数（SkylarkOptions）

- **`ProgressiveMiniMemoryMaxRunes`**：mini memory 的最大长度（runes）。
- **`HistoryPrefetchMaxHits`**：复问时最多注入多少条命中 turn。
- **`HistoryPrefetchMaxRunes`**：prefetch 注入块最大长度。
- **`HistoryPrefetchHints`**：复问关键词（默认包含中英常见说法）。

---

## 4. L2 项目记忆（跨 session 结论沉淀）

### 4.1 做什么

当一次 run 成功结束，SDK 会尝试从 history 中抽取“会话结论”（例如最终方案/关键约束/结论性答复），写入：

- `ProjectMemoryDir/project_memory.jsonl`

随后 Skylark rebuild 时会把这些条目纳入 corpus，使得 `retrieve_knowledge` 能直接命中“历史结论”。

### 4.2 如何开启

- **`SkylarkOptions.PersistProjectMemory = true`**
- （可选）设置 **`SkylarkOptions.ProjectMemoryDir`** 指向项目内 `.agents/memory`

### 4.3 运维建议

- **版本控制**：建议把 `.agents/memory/` 纳入 git（作为项目知识库），但保留清理机制（例如只保留高价值结论）。
- **审计/清理**：JSONL 适合追加与回滚；若你要做“遗忘/合并”，建议在应用侧加一个定期整理流程。

---

## 5. MCP 工具优化（省 token + 更稳定）

SDK 针对 MCP tool 的常见痛点做了两类优化：

- **描述与 schema 压缩（tool registry 层）**：裁剪冗余字段、缩短 description，降低提示词体积。
- **刷新稳定性**：对 `ToolListChanged` 做 debounce / singleflight / backoff，避免 refresh 风暴。

调用方通常只需要按业务启用 MCP servers；压缩策略与刷新稳定性对你是“透明收益”。

---

## 6. 工具输出压缩（落盘 + 引用 + 摘要）

### 6.1 解决什么问题

很多工具（bash、搜索、抓取）会产出长输出；把它们原样塞进 history 会：

- 迅速消耗 token
- 干扰模型注意力（大量无关文本）
- 让 compaction 变得困难（I/O 淹没上下文）

### 6.2 SDK 的策略（已实现）

当某次工具输出超过阈值（`ToolOutputInlineMaxRunes`）：

- **全文落盘**到本机 spool（示例：`/tmp/agentsdk/tool-output/...`）
- history 中只保留：
  - **引用指针**（保存路径）
  - **高信息密度摘要**

摘要规则（优先级）：

- **JSON**：输出 object keys / array len
- **日志/多行文本**：输出 head + tail
- 其他：裁剪到 snippet 上限

### 6.3 调参建议（api.Options）

- **`ToolOutputInlineMaxRunes`**：越小越“省 token”，但可能让模型更频繁依赖引用取回。
- **`ToolOutputSnippetMaxRunes`**：越大越“可读”，但会增加 history 成本。

---

## 7. 结构化反思（Reflection）

SDK 默认启用结构化反思中间件，用于把“失败”变成机器可处理的记录，写入 `middleware.State.Values`（不会阻塞主流程）。

覆盖场景包括：

- 工具调用失败：timeout / validation / safety_denied / MCP 问题等
- 模型因 token 限制等原因提前停止（可用于上层降级/重试）

调用方建议：

- 在你自己的日志/观测系统里，把 `reflection.records`（若存在）打到结构化日志，方便分析“为什么失败、下一步该怎么做”。

---

## 8. 安全与兼容性注意点

- **bash 工具更严格**：默认不允许 shell 元字符/多行命令（除非显式允许）。这属于安全收益，但可能影响你之前依赖的复杂命令拼接。
- **OpenAI provider 更严格**：`api key` 为空会直接报错（避免测试/环境变量混乱）。
- **Skylark 索引产物**：应保持在 gitignore 内；可共享记忆应放 `.agents/memory/`。

---

## 9. 常见问题（FAQ）

### 9.1 为什么我启用 Skylark 仍然会“忘”？

优先检查：

- 是否是 **progressive** 模式（该模式依赖 mini memory + prefetch 才更稳）
- 是否把 `SkylarkOptions.ProgressiveMiniMemoryMaxRunes` 设得过小
- 是否把会话 history 在应用侧裁剪得过狠（导致 prefetch 无法命中）

### 9.2 工具输出落盘路径是否可控？

当前默认落盘在临时目录下，适合本机/短生命周期进程。若你在容器或多机环境运行，建议后续按 P2.3-C 路线把策略下沉到 `tool.OutputPersister` 并支持自定义 URI（例如对象存储、可观测平台、内部文件服务）。

---

## 10. 后续路线（可选）

如果你希望进一步“统一输出压缩策略”，建议按 `docs/OPTIMIZATION-PLAN.md` 的 **P2.3-C** 推进：

- 下沉到 `tool.Executor` 的 `OutputPersister`（让所有运行时路径共享同一策略）
- 把 API 层压缩逻辑变为兼容层，逐步移除重复落盘

