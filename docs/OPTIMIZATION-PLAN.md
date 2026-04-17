# agentsdk-go 优化实施方案（Skylark / 记忆分层 / MCP / 反思回退）
更新时间：2026-04-17

本方案把当前已识别的问题与优化方向，落到 **可逐步合并、可验证、可回退** 的工程计划中。重点解决：

- **Skylark progressive 模式“记不住上次结论”**（必须修）
- **多级持久化记忆**（短期 always-on + 长期可检索/可回滚）
- **MCP 工具提示/描述压缩与稳定性**（类似 mcp-compressor 的收益点）
- **反思、回退与鲁棒性**（把失败模式工程化）

> 约束：遵循仓库 v2 方向（见 `docs/refactor/PRD.md`、`docs/refactor/ARCHITECTURE-v2.md`），尽量保持改动局部、可测试、可渐进上线。

---

## 0. 现状与根因（针对“上次答过下次忘”）

### 0.1 根因总结（与代码对齐）

- **Skylark progressive 默认不注入 Memory/Rules**  
  仅 one-shot routing 会通过 `augmentSkylarkOneShotSystemPrompt()` 重新注入 `AGENTS.md` / rules；progressive 模式依赖模型自己调用 `retrieve_knowledge`，未调用则“忘记”。  
  相关：`pkg/api/runtime_internal.go`、`pkg/api/skylark_init.go`。

- **Skylark 索引语料不包含“对话结论沉淀”**  
  当前 `SkylarkEngine.Rebuild()` 的 docs 来自 memory/rules/skills/tools（`buildSkylarkDocuments`），不含“上次回答形成的结论”。

- **history 检索存在但不自动触发**  
  `pkg/skylark/history.go` 仅提供算法函数，是否调用取决于工具/流程；progressive 模式下并不会默认发生。

### 0.2 目标定义（可验收）

- **同一 session**：用户用“上次/之前/再问一次/继续”等表达复问时，模型在不调用检索工具的情况下也能稳定复用上次结论（或至少知道“需要检索并给出正确调用”）。
- **跨 session（同项目）**：关键结论能被持久化（可审计/可回退），并被检索命中。
- **token 成本**：新增 always-on 注入必须严格受控（默认 < 300~800 tokens），不能把 Skylark 的“按需检索省 token”优势抵消。

---

## 1. 总体架构：四层记忆（L0-L3）

> 参考 openclaw / Acontext 等常见做法：把“按需检索”前面补上一个极轻量的稳定记忆层，避免遗忘；长期记忆可检索、可回滚。

### 1.1 L0：瞬时工作记忆（per-run）

- **载体**：`middleware.State.Values` + `preparedRun` 局部结构体字段
- **内容**：本轮目标、约束、失败分型、重试/回退状态
- **写入**：只在运行期内存在，不落盘

### 1.2 L1：会话记忆（per-session）

- **载体**：`historyStore` + `pkg/message.History`
- **策略**：
  - 仍保留工具调用/结果以保证可追溯
  - 对进入 history 的“超长工具输出”做截断/摘要（否则 compaction 再强也被 tool I/O 淹没）
  - 在会话关键节点生成“会话总结”写入 L2（见后文）

### 1.3 L2：项目持久记忆（per-project，可版本化）

- **载体建议**：新增 `.agents/memory/`（可 git track 的知识/结论），格式建议 JSONL（便于追加与回退）
- **内容**：结论（facts/decisions）、复现步骤、约束、关键命令、环境差异
- **写入门控**：只写“高价值结论”（通过证据、通过测试、或重复出现）

### 1.4 L3：检索增强层（RAG/索引）

- **载体**：现有 `.agents/skylark/*`（Bleve + 可选向量）
- **内容来源**：memory/rules/skills/tools + L2 的结论文档 +（可选）会话总结
- **原则**：索引产物应 gitignore；可共享内容（L2）可版本化

---

## 2. 分期计划（P0/P1/P2）

### P0（优先级最高，快速止血）：Skylark 不再“忘”

#### P0.1 Progressive 模式也注入“超短记忆头”（Always-on Mini Memory）

**目标**：progressive 模式下，即使模型没调用 `retrieve_knowledge`，也能看到上次结论/关键约束的“钩子”。

**实现要点**
- 在 `pkg/api/runtime_internal.go` 组装 `systemPrompt` 时：
  - one-shot：保持现状（可注入完整 `AGENTS.md` / Rules）
  - progressive：追加一个 *mini memory*（严格长度限制）

**Mini Memory 内容建议（默认）**
- 最近一次成功 run 的“结论摘要”（3-5 条，短句）
- 会话级偏好/约束（若存在 tags/metadata）
- 提醒：如需更多细节请调用 `retrieve_knowledge`

**改动点**
- `pkg/api/runtime_internal.go`：在 system prompt 构建处增加 `augmentSkylarkProgressiveMiniMemory(...)`
- 新增 helper 文件：`pkg/api/skylark_memory_head.go`（或类似）

**验证**
- 单测：给定 progressive 模式 + 历史里有结论摘要，确保 system prompt 包含 mini memory 且不超过上限。

#### P0.2 自动触发一次 history 检索并注入 top-k（不靠模型自觉）

**目标**：用户复问时，稳定命中上次相关 turn。

**实现要点**
- 在 `prepare()` 或 `runLoop()` 第一次迭代前：
  - 对 prompt 做 cheap heuristic（是否包含“上次/之前/继续/再问/你刚才”等）
  - 若命中：用 `skylark.SearchHistory(prompt, turns, limit=3~5)` 得到 hits
  - 把 hits 以引用块追加进本轮 user message（或 system prompt 末尾）

**改动点**
- 新增：`pkg/api/skylark_history_prefetch.go`
- 可能需要把 `message.Message` 转换为 `skylark.HistoryTurn` 的轻量转换函数（注意：避免循环依赖，skylark/history.go 已刻意不 import `pkg/message`）

**验证**
- 单测：构造 history，复问 prompt，断言注入内容包含上次回答片段。

#### P0.3 `.agents/skylark/*` 索引产物与可版本化记忆分离

**目标**：避免把 Bleve store 等运行产物误提交；把可共享记忆放到 `.agents/memory/`。

**实现要点**
- 更新 `.gitignore`：忽略 `.agents/skylark/bleve/`、`vectors.json` 等（保留可选 `corpus.json` 视需要）
- 新增目录约定文档（本文件已覆盖，可再补充到 `AGENTS.md` 或 `docs/skylark.md`）

---

### P1（中期）：多级持久化记忆 + MCP 工具压缩/稳定性

#### P1.1 L2：项目记忆落盘（JSONL/WAL）与写入门控（Write Barrier）

**目标**：跨 session 记忆不再丢；可审计、可回滚。

**数据格式（建议）**：`.agents/memory/project_memory.jsonl`

每行示例（逻辑形状）：
- `id`：稳定 ID（hash）
- `kind`：`decision|fact|recipe|pitfall|preference`
- `title`：一句话标题
- `content`：短内容（可被索引）
- `evidence`：文件路径/命令/测试名/链接（可选）
- `confidence`：0-1
- `created_at`/`last_seen_at`
- `source`：`session_id`、`request_id`、`span`（便于回滚/清理）

**写入时机**
- `SessionEnd`（见 `hooks.SessionEnd`）且本次 run 成功
- 或出现“明确用户确认”的指令（后续可加 AskQuestion/显式确认，本仓库默认 YOLO）

**改动点**
- 新增：`pkg/api/memory_store.go`（仅写 JSONL；读取可按需）
- 在 `Runtime.Run` / `RunStream` 的 session end defer 处调用 `maybePersistMemory(...)`

**验证**
- 单测：成功 run 触发写入；失败不写；写入内容可被解析；带 source 字段。

#### P1.2 Skylark corpus 纳入 L2（让索引能召回“结论”）

**目标**：`retrieve_knowledge` 不仅能搜 rules/tools，也能搜项目结论。

**实现要点**
- `buildSkylarkDocuments(...)` 里加入对 `.agents/memory/*.jsonl` 的读取与转换为 `skylark.Document`（kind=memory）
- 控制每条 doc 的长度（避免索引/召回时过长）

**改动点**
- `pkg/api/skylark_documents.go`
- （可选）`pkg/skylark/kinds.go` 增加 Kind 常量（如 `KindMemory`）

**验证**
- 单测：给定 memory jsonl，rebuild 后 SearchIndex 命中并返回。

#### P1.3 MCP 工具“描述压缩 + 缓存”（mcp-compressor 类收益点）

**目标**：减少 tool schema/description 占用 token，提升模型工具选择稳定性。

**实现要点**
- 在 MCP 工具 wrapper 构建时（`pkg/tool/registry.go` 的 `buildRemoteToolWrappers`）增加 transform：
  - 规范化 schema（排序、删除冗余字段）
  - 截断/摘要 description（保留关键参数名）
  - 对“很少用的工具”可配置为强压缩
- 按 `serverID + toolName + schemaHash` 缓存压缩结果（避免每次 refresh 重算）

**配置建议**：在 `.agents/settings.json` 的 `mcp` 段增加（或 runtime Options）：
- `toolDescriptionMaxRunes`
- `schemaPruneLevel`
- `toolCompressionLevel`（A/B/C）

**验证**
- 单测：同一 schema 多次 refresh 命中缓存；压缩后仍能通过 validator（如有）。

#### P1.4 MCP ToolListChanged refresh 防抖/单航班/退避（鲁棒性）

**目标**：避免 tool list 频繁变更导致 refresh 风暴与不稳定。

**实现要点**
- 对 `mcpToolsChangedHandler` 触发的 refresh：
  - debounce（200-500ms）
  - singleflight（同 serverID/sessionID 同时只跑一个）
  - backoff（失败指数退避）

**改动点**
- `pkg/tool/registry.go`：`mcpToolsChangedHandler` / `refreshMCPTools` 周边

**验证**
- 单测/竞态测试：多次通知只触发 1 次 refresh；失败后退避。

---

### P2（高级）：反思、回退与端到端鲁棒性

#### P2.1 结构化反思（Reflection）落在 middleware.State（而不是长文本）

**目标**：失败时生成可机器处理的“决策记录”，而非 prompt 作文。

**实现要点**
- 在 `AfterTool` / `AfterAgent` middleware 中：
  - 失败分型：timeout/validation/safety_denied/mcp_disconnect/not_found/output_too_large
  - 记录：`state.Values["reflection"] = {hypotheses,next_action,rollback_needed,evidence}`

**改动点**
- 新增官方 middleware：`pkg/middleware/reflection/` 或简单放在 `pkg/api` 内建（取决于你们对 package 数量的约束）

**验证**
- 单测：不同错误类型写出不同分型；并能被上层消费（例如写入 L2）。

#### P2.2 回退（Rollback）最小闭环：先“可人工回退”，再“自动回退”

**阶段 A：回退信息完备（P2.2-A）**
- 文件写入工具在执行前记录：
  - 旧内容 hash / patch
  - 写入范围
  - session/request id
- bash 工具记录：
  - cwd/env/command/stdout/stderr（已有 spool 机制可复用）

**阶段 B：自动回滚（P2.2-B）**
- 仅对确定可回滚的操作（例如 edit/write）提供 `rollback_last_step` 工具或 API

**改动点**
- `pkg/tool/builtin/*`（write/edit）
- 或在 `tool.Executor` 层增加 “sidecar journal”

**验证**
- 单测：写入后可按 journal 回滚到上一个版本。

#### P2.3 工具输出压缩策略统一化（防止 tool I/O 淹没上下文）

**目标**：大输出不直接污染 history；保留指针以便按需取回。

**实现要点**
- 在 `runtimeToolExecutor.finalizeToolCall` 处（或更早）：
  - 超过阈值则截断
  - 生成摘要（优先规则式：JSON keys/len、日志 head+tail；必要时再用模型压缩）
  - 把全文写入 spool，仅把“摘要+指针”写入 history
  - 阈值可配置（`Options.ToolOutputInlineMaxRunes` / `Options.ToolOutputSnippetMaxRunes`）

**C：让 `tool.OutputPersister` 覆盖更多工具输出（统一由 persister 管）**
- **动机**：当前在 API 层做“落盘 + 指针 + 摘要”虽有效，但属于“后置修剪”。把策略下沉到工具执行层，可以：
  - 让所有调用路径（并发工具调用、subagent、未来新 runtime）共享同一输出策略
  - 避免在多处重复做“是否持久化/如何摘要/引用格式”的逻辑
  - 更容易针对不同工具类型定制（JSON/日志/二进制/文件列表等）
- **设计落点**
  - 在 `tool.Executor`（或其 result pipeline）中引入统一的 `OutputPersister` 策略：**执行后、回填 history 前**，把过大输出变成 `OutputRef + Summary`。
  - API 层只负责“把 `OutputRef` 以稳定格式展示给模型”，并且**不重复持久化**。
- **建议接口形状（示例）**
  - `type OutputPersister interface { Persist(ctx, meta, output) (ref *tool.OutputRef, summary string, err error) }`
  - `type OutputRef struct { URI string; Bytes int64; ContentType string; Digest string }`
  - `meta` 至少包含：`sessionID`、`toolName`、`toolUseID`、`timestamp`、`contentTypeHint`
- **迁移步骤**
  - Step C1：在 `tool.Executor` 里把“输出太大 → Persist → 回填 OutputRef/Summary”打通（默认实现落盘到 spool）
  - Step C2：为常见内容类型提供内置摘要器（JSON keys/len、日志 head+tail）
  - Step C3：让 `runtimeToolExecutor` 优先使用 `CallResult.Result.OutputRef`/`Summary`，并逐步把 API 层的落盘逻辑降级为兼容层（最终可移除）
- **验收标准**
  - 任意工具只要输出超过阈值，都会得到一致的 `OutputRef + Summary`（不依赖 API 层）
  - 重复调用/重试不会重复落盘（基于 digest 或幂等 key）
  - 单测覆盖：JSON、日志、超长纯文本三类输出的 persister 行为一致

**验证**
- 单测：超长输出进入 history 前被截断；指针可用于后续读取。
  - 单测：JSON 输出优先生成 keys/len 摘要；日志输出优先 head+tail。

---

## 3. 具体实现步骤清单（按提交粒度建议）

### Step 1（P0.1）：Progressive mini memory 注入
- 新增 helper + 调整 system prompt 组装
- 单测覆盖（长度上限、内容形状）

### Step 2（P0.2）：复问触发的 history prefetch
- 新增 heuristic + `SearchHistory` 注入
- 单测覆盖（复问命中、非复问不注入）

### Step 3（P0.3）：产物与可版本化内容分离
- 更新 `.gitignore`
- 可选：补 `docs/skylark.md` 或在 `AGENTS.md` 追加约定

### Step 4（P1.1）：L2 项目记忆 JSONL store（写入门控）
- 增加 store + 写入逻辑（SessionEnd）
- 单测覆盖（成功写、失败不写、字段齐全）

### Step 5（P1.2）：Skylark 索引纳入 L2
- `buildSkylarkDocuments` 读取 memory jsonl → 文档
- rebuild/search 单测

### Step 6（P1.3）：MCP tool descriptor 压缩 + 缓存
- 在 wrapper 构建处加 transformer
- 缓存结构 + 单测

### Step 7（P1.4）：MCP refresh 稳定性（debounce/singleflight/backoff）
- 并发/竞态测试（至少覆盖“多次通知只 refresh 一次”）

### Step 8（P2）：反思/回退/输出压缩
- 先上“结构化反思”+“回退信息完备”
- 最后再考虑“自动回滚工具”

---

## 4. 验收标准（建议）

- **Skylark 复问不再遗忘**：progressive 模式下，复问场景能稳定引用上次结论或相关片段（无需模型主动调用检索工具）。
- **跨 session 结论可召回**：写入 `.agents/memory/*.jsonl` 的结论，能被 `retrieve_knowledge` 命中。
- **token 成本可控**：mini memory 默认 < 300~800 tokens；history prefetch 注入条数默认 <=5。
- **MCP 稳定**：ToolListChanged 高频触发不再导致 refresh 风暴；压缩后工具调用仍可用。
- **可回退**：至少达到“回退信息完备”（人工可按日志/patch 回滚）。

---

## 5. TODO（可直接勾选）

### P0
- [x] **P0.1**：progressive 模式加入 mini memory 注入（system prompt 末尾，长度上限）
- [x] **P0.2**：复问触发 history prefetch（SearchHistory top-k 注入）
- [x] **P0.3**：更新 `.gitignore`，分离 Skylark 索引产物与可版本化记忆目录

### P1
- [x] **P1.1**：新增 `.agents/memory/` JSONL store（写入门控 + source 字段）
- [x] **P1.2**：Skylark corpus 纳入 L2 记忆文档（Rebuild + SearchIndex 命中）
- [x] **P1.3**：MCP tool descriptor 压缩与缓存（schema/description）
- [x] **P1.4**：MCP refresh 防抖 + singleflight + backoff

### P2
- [x] **P2.1**：结构化反思（AfterTool/AfterAgent 写入 state.Values）
- [x] **P2.2-A**：回退信息完备（write/edit 记录 patch；bash 记录环境/输出）
- [x] **P2.2-B**：自动回滚能力（仅对可回滚操作）
- [x] **P2.3**：统一工具输出压缩策略（摘要+指针入 history，全文进 spool）
  - [x] **P2.3-C1**：在 `tool.Executor` 下沉 `OutputPersister`（输出太大 → Persist → OutputRef+Summary）
  - [x] **P2.3-C2**：内置摘要器（JSON keys/len、日志 head+tail、纯文本裁剪）
  - [x] **P2.3-C3**：API 层仅展示引用（不再自行落盘），并提供兼容/迁移开关

