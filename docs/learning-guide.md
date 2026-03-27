# agentsdk-go 深入学习指南

> 本文档帮助开发者系统理解 agentsdk-go 的整体架构、核心逻辑与数据流，适合作为项目学习的首要参考。

## 目录

1. [项目概述](#一项目概述)
2. [整体架构](#二整体架构)
3. [目录结构](#三目录结构)
4. [请求生命周期与数据流](#四请求生命周期与数据流)
5. [核心层详解](#五核心层详解)
6. [功能层详解](#六功能层详解)
7. [配置系统](#七配置系统)
8. [安全与沙箱](#八安全与沙箱)
9. [入口点与示例](#九入口点与示例)
10. [学习路径建议](#十学习路径建议)
11. [关键文件索引](#十一关键文件索引)

---

## 一、项目概述

### 1.1 定位

**agentsdk-go** 是一个 Go 语言实现的 Agent SDK，实现 Claude Code 风格的运行时能力，具备可选的六阶段中间件拦截机制。目标场景包括：

- **CLI**：交互式命令行
- **CI/CD**：持续集成环境
- **Platform**：嵌入到企业应用中

### 1.2 设计原则

- **KISS**：单一职责，避免过度设计
- **YAGNI**：最小依赖，按需扩展
- **大道至简**：简单接口，精炼实现

### 1.3 核心能力

| 能力 | 说明 |
|------|------|
| 多模型支持 | 通过 `ModelFactory` / `ModelPool` 支持子 Agent 级模型绑定 |
| Token 统计 | 输入/输出/缓存 token 自动累计 |
| Auto Compact | 达到 token 阈值时自动上下文压缩 |
| Async Bash | 后台命令执行与任务管理 |
| Rules 配置 | `.claude/rules/` 目录支持，热重载 |
| OpenTelemetry | 分布式追踪（可选 build tag） |
| UUID 追踪 | 请求级 UUID 用于可观测性 |

---

## 二、整体架构

### 2.1 分层结构

```
┌─────────────────────────────────────────────────────────────────┐
│                        入口层 (Entry Points)                      │
│  CLI (cmd/cli)  │  HTTP (examples/03-http)  │  API (api.New)     │
└─────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────┐
│                    API 层 (pkg/api)                               │
│  Runtime: 统一 SDK 接口，聚合配置、模型、工具、沙箱、Hooks、历史等   │
└─────────────────────────────────────────────────────────────────┘
                                    │
        ┌───────────────────────────┼───────────────────────────┐
        ▼                           ▼                           ▼
┌───────────────┐         ┌───────────────┐         ┌───────────────┐
│  核心层 Core   │         │  功能层 Feature │         │  支撑层 Support │
├───────────────┤         ├───────────────┤         ├───────────────┤
│ agent/        │         │ config/       │         │ security/     │
│ middleware/   │         │ core/hooks/   │         │ sandbox/      │
│ model/        │         │ core/events/   │         │ mcp/          │
│ tool/         │         │ runtime/       │         │               │
│ message/      │         │   skills/      │         │               │
│               │         │   subagents/   │         │               │
│               │         │   commands/    │         │               │
│               │         │   tasks/       │         │               │
└───────────────┘         └───────────────┘         └───────────────┘
```

### 2.2 核心组件关系

```
                    ┌─────────────────────────────┐
                    │         api.Runtime          │
                    │  (prepare → runAgent → build) │
                    └──────────────┬──────────────┘
                                   │
         ┌─────────────────────────┼─────────────────────────┐
         ▼                         ▼                         ▼
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│ conversationModel│    │    agent.Agent   │    │runtimeToolExec  │
│ (model.Model →   │    │   (核心循环)     │    │ (tool.Executor  │
│  agent.Model)    │    │                 │    │  → agent.Tool   │
└────────┬────────┘    └────────┬────────┘    └────────┬────────┘
         │                      │                      │
         │    ┌─────────────────┴─────────────────┐   │
         │    │         middleware.Chain          │   │
         │    │  (6 阶段拦截: Before/After Agent/Model/Tool) │
         │    └───────────────────────────────────┘   │
         ▼                      ▼                      ▼
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│ model.Model     │    │ message.History │    │ tool.Executor   │
│ (Anthropic等)   │    │ (会话历史)       │    │ + sandbox       │
└─────────────────┘    └─────────────────┘    └─────────────────┘
```

---

## 三、目录结构

```
agentsdk-go/
├── cmd/cli/              # CLI 入口 (agentctl)
├── examples/              # 示例程序
│   ├── 01-basic/          # 最简 Run 调用
│   ├── 02-cli/            # 交互式 REPL
│   ├── 03-http/           # HTTP 服务 + SSE 流式
│   ├── 04-advanced/       # 完整管道 (middleware, hooks, MCP, sandbox)
│   ├── 05-custom-tools/   # 自定义工具
│   ├── 06-embed/          # 嵌入 FS 配置
│   ├── 07-multimodel/     # 多模型池
│   ├── 08-askuserquestion/# 用户确认
│   ├── 09-task-system/    # 任务系统
│   ├── 10-hooks/          # Hooks 生命周期
│   ├── 11-reasoning/      # 推理模型
│   └── 12-multimodal/     # 多模态输入
├── pkg/
│   ├── agent/             # Agent 核心循环
│   ├── api/               # 统一 SDK 接口 (Runtime)
│   ├── config/            # 配置加载 (.claude/settings.json)
│   ├── core/
│   │   ├── events/        # 事件总线
│   │   └── hooks/         # Hooks 执行器
│   ├── message/           # 消息历史
│   ├── middleware/        # 6 阶段拦截链
│   ├── model/             # 模型适配器 (Anthropic, OpenAI)
│   ├── mcp/               # MCP 客户端
│   ├── prompts/           # 提示词构建
│   ├── runtime/
│   │   ├── commands/      # Slash 命令解析
│   │   ├── skills/        # Skills 管理
│   │   ├── subagents/     # 子 Agent 管理
│   │   └── tasks/         # 任务依赖
│   ├── sandbox/           # 沙箱接口
│   ├── security/          # 安全校验
│   └── tool/              # 工具注册与执行
│       └── builtin/       # 内置工具 (bash, file_read, grep, glob 等)
├── test/integration/      # 集成测试
└── docs/                  # 文档
```

---

## 四、请求生命周期与数据流

### 4.1 完整请求流程

```
用户请求 (api.Request)
    │
    ▼
┌─────────────────────────────────────────────────────────────────┐
│ 1. Run() / RunStream() 入口                                       │
│    - sessionGate.Acquire(sessionID)  # 同 session 串行           │
│    - beginRun()  # 防止 Runtime 关闭时新请求                      │
└─────────────────────────────────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────────────────────────────┐
│ 2. prepare() 请求准备                                             │
│    - 标准化 Request (normalized)                                  │
│    - 获取/创建 History (historyStore.Get)                         │
│    - Auto Compact (maybeCompact)                                   │
│    - executeCommands()  # Slash 命令                              │
│    - executeSkills()    # Skills 展开                             │
│    - executeSubagent()  # 子 Agent 路由                           │
│    - 构建 toolWhitelist                                           │
└─────────────────────────────────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────────────────────────────┐
│ 3. runAgentWithMiddleware()                                       │
│    - selectModelForSubagent()  # 选模型 (请求/子Agent/默认)       │
│    - 构建 conversationModel (适配 model.Model → agent.Model)     │
│    - 构建 runtimeToolExecutor (适配 tool.Executor → agent.Tool)   │
│    - agent.New() + agent.Run()                                    │
└─────────────────────────────────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────────────────────────────┐
│ 4. Agent 循环 (pkg/agent/agent.go)                                 │
│                                                                   │
│    BeforeAgent ────────────────────────────────────────────────► │
│         │                                                         │
│         ▼                                                         │
│    ┌─────────────────────────────────────────────────────────┐   │
│    │ 循环 (直到 Done 或无 tool calls)                          │   │
│    │   BeforeModel → Model.Generate → AfterModel              │   │
│    │        │                                                 │   │
│    │        │ 若有 ToolCalls                                   │   │
│    │        ▼                                                 │   │
│    │   BeforeTool → tools.Execute → AfterTool → 下一轮        │   │
│    └─────────────────────────────────────────────────────────┘   │
│         │                                                         │
│         ▼                                                         │
│    AfterAgent ─────────────────────────────────────────────────► │
└─────────────────────────────────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────────────────────────────┐
│ 5. buildResponse() 组装 api.Response                              │
│    - 转换 runResult → Result                                      │
│    - 附加 CommandResults, SkillResults, SubagentResult           │
│    - persistHistory() 持久化历史                                  │
└─────────────────────────────────────────────────────────────────┘
```

### 4.2 工具执行链路

```
agent.ToolCall
    │
    ▼
runtimeToolExecutor.Execute()

    ├─ isAllowed()           # tool whitelist 检查
    │
    ├─ hooks.PreToolUse()    # 可能 deny / ask / modify

    ├─ permissionResolver   # 处理 ask 审批 (ApprovalQueue 等)

    └─ executor.Execute()
        ├─ sandbox.CheckToolPermission  # allow/ask/deny 规则
        ├─ sandbox.Enforce              # 路径/网络/资源校验
        ├─ registry.Get() + schema 校验
        ├─ tool.Execute() 或 StreamExecute
        └─ persister.MaybePersist      # 大输出落盘

    └─ appendToolResult → history.Append(tool message)
```

---

## 五、核心层详解

### 5.1 Agent

**位置**: `pkg/agent/agent.go`

**职责**: 驱动主循环，串联 middleware、model、tools。

**接口依赖**:

```go
// agent.Model - Agent 循环调用
type Model interface {
    Generate(ctx context.Context, c *Context) (*ModelOutput, error)
}

// agent.ToolExecutor - 执行模型发出的 tool call
type ToolExecutor interface {
    Execute(ctx context.Context, call ToolCall, c *Context) (ToolResult, error)
}
```

**核心数据结构**:

```go
type ModelOutput struct {
    Content   string
    ToolCalls []ToolCall
    Done      bool
}

type ToolCall struct {
    ID    string
    Name  string
    Input map[string]any
}
```

**循环逻辑**:
- 每次迭代: `BeforeModel` → `Model.Generate` → `AfterModel`
- 若有 `ToolCalls`: 对每个 call 执行 `BeforeTool` → `tools.Execute` → `AfterTool`
- 若 `Done` 或 `len(ToolCalls)==0`: 执行 `AfterAgent` 并返回
- 否则 `iteration++` 继续下一轮

**终止条件**:
- `ctx` 取消
- `MaxIterations` 达到
- 模型返回 `Done` 或空 `ToolCalls`

### 5.2 Model 双接口

SDK 存在两套 Model 接口，由 `pkg/api` 层桥接：

| 接口 | 定义位置 | 方法 | 用途 |
|------|----------|------|------|
| `agent.Model` | `pkg/agent/agent.go` | `Generate(ctx, *Context) (*ModelOutput, error)` | Agent 循环调用 |
| `model.Model` | `pkg/model/interface.go` | `Complete`, `CompleteStream` | 提供商实现 |

**适配器**: `conversationModel` (`pkg/api/agent.go`)

- 将 `model.Model` 包装为 `agent.Model`
- 负责: 历史管理、trim、rules 注入、流式调用
- 内部使用 `base.CompleteStream` 与底层 API 通信

### 5.3 Tool 系统

**位置**: `pkg/tool/`

**Tool 接口**:

```go
type Tool interface {
    Name() string
    Description() string
    Schema() *JSONSchema
    Execute(ctx context.Context, params map[string]any) (*ToolResult, error)
}
```

**Registry** (`registry.go`):
- 线程安全注册与查找
- 支持 MCP 远程工具动态注册

**Executor** (`executor.go`):
- 连接 Registry 与 Sandbox
- 执行前: `CheckToolPermission` → `Enforce`
- 支持 `StreamingTool` 流式输出
- 支持 `OutputPersister` 大输出落盘

**内置工具** (`pkg/tool/builtin/`):
- `bash` - 命令执行（含安全校验）
- `file_read` / `file_write` / `file_edit`
- `grep` - 正则搜索
- `glob` - 文件模式匹配
- `web_fetch` / `web_search`
- `bash_output` / `bash_status` / `kill_task` - 异步 bash
- `task_create` / `task_list` / `task_get` / `task_update`
- `ask_user_question`
- `skill` / `slash_command` / `task` (子 Agent)

### 5.4 Middleware

**位置**: `pkg/middleware/`

**6 个阶段** (`types.go`):

```go
const (
    StageBeforeAgent  // 请求边界
    StageBeforeModel  // 模型调用前
    StageAfterModel   // 模型调用后
    StageBeforeTool   // 工具执行前
    StageAfterTool    // 工具执行后
    StageAfterAgent   // 响应边界
)
```

**State** (`types.go`):

```go
type State struct {
    Iteration   int
    Agent       any   // *agent.Context
    ModelInput  any   // model.Request
    ModelOutput any   // *agent.ModelOutput
    ToolCall    any   // agent.ToolCall
    ToolResult  any   // agent.ToolResult
    Values      map[string]any  // 跨 middleware 共享
}
```

**Chain** (`chain.go`):
- 顺序执行各 middleware
- 任一返回错误则短路
- 支持 `WithTimeout` 每阶段超时

**使用方式**:

```go
mw := middleware.Funcs{
    Identifier: "logging",
    OnBeforeAgent: func(ctx context.Context, st *middleware.State) error {
        // ...
        return nil
    },
}
```

### 5.5 Message History

**位置**: `pkg/message/`

**History** (`history.go`):
- 线程安全
- `Append`, `Replace`, `All`, `Last`, `Len`, `Reset`
- 支持 `TokenCounter` 估算 token 数

**historyStore** (`pkg/api/runtime_helpers.go`):
- 按 session 的 LRU 缓存
- `MaxSessions` 控制最大会话数
- 可选 `diskHistoryPersister` 持久化

---

## 六、功能层详解

### 6.1 Config

**位置**: `pkg/config/`

**目录结构** (Claude Code 兼容):

```
.claude/
├── settings.json       # 项目配置
├── settings.local.json # 本地覆盖 (gitignored)
├── rules/              # 规则定义 (markdown)
├── skills/             # Skills 定义
├── commands/           # Slash 命令
└── agents/             # 子 Agent 定义
```

**配置优先级** (高 → 低):
1. 运行时覆盖 (CLI/API)
2. `.claude/settings.local.json`
3. `.claude/settings.json`
4. SDK 默认值

**Settings 主要字段** (`config/settings_types.go`):
- `Permissions` - allow/ask/deny 规则
- `Hooks` - PreToolUse, PostToolUse, SessionStart 等
- `Sandbox` - 沙箱开关、网络配置
- `MCP` - MCP 服务器
- `Model` - 默认模型
- `DisallowedTools` - 工具黑名单

### 6.2 Hooks

**位置**: `pkg/core/hooks/`

**执行方式**: Shell 命令，stdin 传入 JSON，stdout 解析 `HookOutput`。

**退出码语义**:

| 退出码 | 含义 | 行为 |
|--------|------|------|
| 0 | 成功 | 解析 stdout 为 JSON |
| 2 | 阻塞错误 | stderr 为错误信息，执行停止 |
| 其他 | 非阻塞 | 记录 stderr，继续 |

**支持事件**:
- PreToolUse, PostToolUse
- UserPromptSubmit, Stop
- PermissionRequest
- SessionStart, SessionEnd
- SubagentStart, SubagentStop
- ModelSelected

**runtimeHookAdapter** (`pkg/api/options.go`):
- 将 Hooks 与 Runtime 桥接
- `PreToolUse` 可返回 deny/ask/updatedInput

### 6.3 MCP

**位置**: `pkg/mcp/`

- stdio 传输 (本地进程)
- SSE 传输 (HTTP 服务)
- 从 MCP 服务器自动注册工具
- 通过 `.claude/settings.json` 或 `--mcp` 配置

### 6.4 Runtime 扩展

| 模块 | 位置 | 说明 |
|------|------|------|
| **Skills** | `pkg/runtime/skills/` | 脚本化加载，热重载 |
| **Commands** | `pkg/runtime/commands/` | Slash 命令解析与路由 |
| **Subagents** | `pkg/runtime/subagents/` | 多 Agent 编排 |
| **Tasks** | `pkg/runtime/tasks/` | 任务依赖与追踪 |

---

## 七、配置系统

### 7.1 settings.json 示例

```json
{
  "permissions": {
    "allow": ["Bash(ls:*)", "Bash(pwd:*)"],
    "deny": ["Read(.env)", "Read(secrets/**)"]
  },
  "disallowedTools": ["web_search", "web_fetch"],
  "env": {
    "MY_VAR": "value"
  },
  "sandbox": {
    "enabled": false
  },
  "mcp": {
    "servers": {
      "my-server": {
        "command": "node",
        "args": ["server.js"]
      }
    }
  }
}
```

### 7.2 API Options 主要字段

```go
api.Options{
    ProjectRoot:   ".",
    ModelFactory:  provider,
    EntryPoint:    api.EntryPointCLI,
    Middleware:    []middleware.Middleware{...},
    MaxIterations: 50,
    Timeout:       0,
    TokenLimit:    0,
    MaxSessions:   500,
    EnabledBuiltinTools: []string{"bash", "file_read"},
    CustomTools:   []tool.Tool{...},
}
```

---

## 八、安全与沙箱

### 8.1 三层防护

1. **Path 白名单** (`security.Sandbox`): `ValidatePath` 限制文件访问
2. **Symlink 解析** (`PathResolver`): 防止路径穿越
3. **命令校验** (`security.Validator`): 禁止危险命令，可配置 `AllowShellMetachars`

### 8.2 命令校验

**位置**: `pkg/security/validator.go`

- 禁止: `dd`, `mkfs`, `fdisk`, `shutdown`, `reboot`, `sudo`
- 危险模式: `rm -rf`, `rm -r`, `rmdir -p`
- Shell 元字符: `|;&><`$` (Platform 模式默认禁止)

### 8.3 Sandbox Manager

**位置**: `pkg/sandbox/`

- `FileSystemPolicy` - 路径校验
- `NetworkPolicy` - 出站域名白名单
- `ResourcePolicy` - CPU/内存/磁盘限制
- `CheckToolPermission` - 基于 settings 的 allow/ask/deny

---

## 九、入口点与示例

### 9.1 入口

| 入口 | 文件 | 说明 |
|------|------|------|
| **CLI** | `cmd/cli/main.go` | 交互式命令行，`--stream`、`--acp`、`--mcp` |
| **HTTP** | `examples/03-http/server.go` | `/health`, `POST /v1/run`, `POST /v1/run/stream` |
| **API** | `api.New()` + `runtime.Run()` | 程序化调用 |

### 9.2 示例

| 示例 | 说明 |
|------|------|
| 01-basic | 最简 Run 调用 |
| 02-cli | 模拟 CLI 交互 |
| 03-http | HTTP 服务 + SSE 流式 |
| 04-advanced | MCP、middleware、hooks |
| 05-custom-tools | 自定义工具 |
| 06-embed | 嵌入 FS |
| 07-multimodel | 多模型池 |
| 08-askuserquestion | 用户确认 |
| 09-task-system | 任务系统 |
| 10-hooks | Hooks 配置 |
| 11-reasoning | 推理模型 |
| 12-multimodal | 多模态输入 |

### 9.3 并发模型

- 不同 `SessionID` 的请求可并行
- 相同 `SessionID` 的请求互斥（串行），并发调用返回 `ErrConcurrentExecution`
- `Runtime.Close()` 等待所有进行中请求完成

---

## 十、学习路径建议

### 10.1 入门 (1–2 天)

1. 阅读 `README.md`、`CLAUDE.md`
2. 运行 `examples/01-basic`、`examples/02-cli`
3. 理解 `api.New()` → `runtime.Run()` 基本流程

### 10.2 核心 (3–5 天)

1. 精读 `pkg/agent/agent.go` 主循环
2. 理解 `conversationModel` 与 `runtimeToolExecutor` 的适配
3. 追踪一次完整请求从 `Run` 到 `buildResponse` 的路径
4. 阅读 `pkg/middleware/` 理解 6 阶段拦截

### 10.3 深入 (1–2 周)

1. 工具执行链路: `runtimeToolExecutor` → `hooks.PreToolUse` → `executor.Execute` → `sandbox`
2. 配置加载: `loadSettings` → `Settings` 合并逻辑
3. Hooks 与事件: `runtimeHookAdapter`、`core/events`
4. 安全: `sandbox.Manager`、`security.Validator`

### 10.4 扩展

1. 自定义 Middleware
2. 自定义 Tool
3. MCP 集成
4. 多模型配置

---

## 十一、关键文件索引

| 文件 | 作用 |
|------|------|
| `pkg/agent/agent.go` | Agent 主循环 |
| `pkg/api/agent.go` | Runtime、prepare、runAgent、conversationModel、runtimeToolExecutor |
| `pkg/model/interface.go` | Model 接口 |
| `pkg/model/anthropic.go` | Anthropic 实现 |
| `pkg/tool/registry.go` | 工具注册与 MCP |
| `pkg/tool/executor.go` | 工具执行与沙箱 |
| `pkg/middleware/chain.go` | 链式执行 |
| `pkg/middleware/types.go` | Stage、State、Middleware 接口 |
| `pkg/message/history.go` | 消息历史 |
| `pkg/config/settings_types.go` | 配置结构 |
| `pkg/security/sandbox.go` | 安全沙箱 |
| `pkg/sandbox/interface.go` | 沙箱策略接口 |
| `cmd/cli/main.go` | CLI 入口 |
| `examples/03-http/server.go` | HTTP 示例 |

---

## 附录：接口速查

### agent.Model

```go
type Model interface {
    Generate(ctx context.Context, c *Context) (*ModelOutput, error)
}
```

### agent.ToolExecutor

```go
type ToolExecutor interface {
    Execute(ctx context.Context, call ToolCall, c *Context) (ToolResult, error)
}
```

### model.Model

```go
type Model interface {
    Complete(ctx context.Context, req Request) (*Response, error)
    CompleteStream(ctx context.Context, req Request, cb StreamHandler) error
}
```

### tool.Tool

```go
type Tool interface {
    Name() string
    Description() string
    Schema() *JSONSchema
    Execute(ctx context.Context, params map[string]any) (*ToolResult, error)
}
```

### middleware.Middleware

```go
type Middleware interface {
    Name() string
    BeforeAgent(ctx context.Context, st *State) error
    BeforeModel(ctx context.Context, st *State) error
    AfterModel(ctx context.Context, st *State) error
    BeforeTool(ctx context.Context, st *State) error
    AfterTool(ctx context.Context, st *State) error
    AfterAgent(ctx context.Context, st *State) error
}
```
