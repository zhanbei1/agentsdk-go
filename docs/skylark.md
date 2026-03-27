# Skylark：渐进式检索（Progressive Retrieval）

## 目标

在启用 **Skylark** 后，运行时不再把完整 Memory / Rules / 技能列表 / 工具列表一次性塞进系统提示与工具 schema，而是：

1. **默认只暴露** `retrieve_knowledge` 与 `retrieve_capabilities` 两个工具（若未被 `disallowedTools` 禁用）。
2. 模型通过检索按需拉取 **知识**（memory / context / document / history）与 **能力**（skill / tool / mcp）。
3. 通过 `retrieve_capabilities` 的 `unlock` / `unlock_names` / `unlock_top_n` 将会话内 **可执行工具集合** 逐步扩大（与 `Request.ToolWhitelist` 求交）。

底层索引：**Bleve** 全文检索 + 可选 **向量语义**（langchaingo `embeddings` + OpenAI 兼容 HTTP），持久化在 `.agents/skylark/`。

## 架构位置

- `pkg/skylark`：Bleve 索引、`corpus.json` 正文、`vectors.json` 向量、历史会话的轻量打分检索。
- `pkg/api`：`SkylarkOptions`、`skylark_route.go`（One-shot 路由）、`skylark_allow.go`（会话内解锁集合）、`skylark_tools.go`（两个检索工具）、`skylark_init.go`（引擎构建与注册）、`runtime_internal.go`（每轮重算 `Tools`、并行 tool 批次）、`runtime_tool_executor.go`（白名单 + prepare/invoke/finalize）。

依赖方向保持：`api` → `skylark`（`skylark` 不依赖 `api`）。

## 策略与算法（摘要）

1. **索引构建（启动时）**  
   从 `AGENTS.md` 文本、Rules 聚合内容、`skills.Registry` 定义、`tool.Registry.List()`（含 MCP 包装名）生成 `Document`，写入 Bleve；若配置了嵌入模型，则对「标题+正文」批量 `EmbedDocuments` 并写入 `vectors.json`。

2. **知识检索 `retrieve_knowledge`**  
   - 未指定 `kinds`：Bleve 全库检索 + 会话 **history**（对 `message.History` 做分词重叠打分，不经 Bleve）。  
   - 指定 `kinds`：仅检索对应 `kind` 字段；仅 `history` 时不走 Bleve。  
   - 排序：Bleve `_score` 归一化后与查询向量余弦相似度按 `TextWeight`/`VecWeight` 混合（无向量时退化为全文）。

3. **能力检索 `retrieve_capabilities`**  
   在 `kind ∈ {skill,tool,mcp}` 子集上检索；可选 **解锁**：  
   - `unlock_names`：显式解锁工具名；  
   - `unlock=true`：`unlock_top_n`（默认见 `SkylarkOptions.DefaultUnlockTopN`，未配置时为 2）对命中中的 tool/mcp 解锁；若存在 skill 命中则解锁一次 `skill` 工具。

4. **自动技能注入**  
   默认在 Skylark 下 **关闭** 旧的自动 skill 激活（`executeSkills` 短路），避免在用户提示前注入大段 skill 输出；可通过 `SkylarkOptions.KeepAutoSkills=true` 恢复。

5. **系统提示**  
   启用 Skylark 时不再把完整 Memory / Rules 拼进 system prompt（仍进入索引）；并追加简短 Skylark 使用说明（`skylarkSystemPromptAppend`）。

## 配置

### `api.Options`

```go
Skylark: &api.SkylarkOptions{
    Enabled:          true,
    DataDir:          "", // 默认 <ProjectRoot>/.agents/skylark
    DisableEmbedding: false,
    Embedder:         nil,  // 可选；nil 且未 DisableEmbedding 时用环境变量创建嵌入客户端
    KeepAutoSkills:   false,
    // 短问句路由：EnableOneShotRouting 缺省为开启（nil 或与 *true）；传 *false 可关闭。
    // 短于 SimplePromptMaxRunes（withDefaults 后默认 10 汉字/字符，未配置时 0 也会变成 10）
    // 且不命中 ComplexityHints 时，本请求跳过渐进解锁并全量工具 + 注入 Memory/Rules。
    EnableOneShotRouting:     nil, // 或 boolPtr(true) / boolPtr(false)
    SimplePromptMaxRunes:   0,   // ≤0 时在 withDefaults 中为 10；也可显式设为更大
    ComplexityHints:        nil, // 子串匹配（不区分大小写）则仍走渐进 Skylark
    // 模型省略 limit / unlock_top_n 时的默认 top-k（0 表示 knowledge=3、capabilities=2、unlock_top_n=2）
    DefaultKnowledgeLimit:    0,
    DefaultCapabilitiesLimit: 0,
    DefaultUnlockTopN:        0,
},
// 同一轮模型返回多个 tool_use 时，默认并行执行（RunnableParallel 语义）。禁止见：
DisableParallelToolCalls: false,
```

### 环境变量（语义向量，可选）

| 变量 | 说明 |
|------|------|
| `SKYLARK_EMBEDDING_API_KEY` / `OPENAI_API_KEY` | 未设置则仅使用 Bleve，不调用嵌入 API |
| `SKYLARK_EMBEDDING_BASE_URL` / `OPENAI_BASE_URL` | OpenAI 兼容 API 根路径，默认 `https://api.openai.com/v1` |
| `SKYLARK_EMBEDDING_MODEL` | 默认 `text-embedding-3-small` |

### `settings.json`

- `disallowedTools` 若包含 `retrieve_knowledge` 或 `retrieve_capabilities`，对应工具不会注册。

## 磁盘布局（默认 `DataDir`）

```
.agents/skylark/
├── bleve/          # Bleve 索引目录
├── corpus.json     # 文档快照（供展示与向量键）
└── vectors.json      # 文档 ID → 向量（启用嵌入时）
```

## 运维说明

- 修改技能、工具、MCP、AGENTS.md 或 Rules 后，当前实现会在 **进程启动** 时 `Rebuild` 索引；长跑进程需重启或后续可加显式 `Rebuild` API（未在本次范围）。
- 无 API Key 时行为为 **纯 Bleve**，仍可进行关键词检索。

## 功能说明（面向产品/集成）

- **适用场景**：工具与技能很多、上下文昂贵、希望模型先检索再行动。  
- **不适用**：需要「开箱即用」列出全部工具名的极简 demo（可关闭 `Skylark.Enabled`）。
