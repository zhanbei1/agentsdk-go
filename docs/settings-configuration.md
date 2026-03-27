# .claude/settings.json 配置说明文档

## 概述

`.claude/settings.json` 是 agentsdk-go 的项目级配置文件，用于定义工具权限、沙箱设置、钩子、MCP 服务器等。该文件位于项目根目录的 `.claude/` 文件夹下。

从 0.5+ 版本开始，**所有配置都通过同一条加载管道** 进入 `api.Runtime`：

- 文件层：`.claude/settings.json` + `.claude/settings.local.json`
- 运行时层：`api.Options.SettingsOverrides`
- API 运行参数：`api.Options` 本身（例如 `Sandbox`、`ApprovalQueue` 等）

### 配置加载优先级（从低到高）

**1）SDK 默认值**

- 由 `config.GetDefaultSettings()` 和 `api.Options.withDefaults()` 提供：
  - 默认 `cleanupPeriodDays`、`bashOutput` 阈值
  - 默认 `respectGitignore`
  - 默认 `MaxSessions`、`Sandbox.NetworkAllow` 等

**2）项目级配置 `.claude/settings.json`**

- 该文件随仓库提交，作为团队共享的基础配置。

**3）本地覆盖 `.claude/settings.local.json`**

- gitignored，仅在本机生效。
- 用于开发者个人偏好、临时测试配置等。

**4）运行时覆盖 `api.Options.SettingsOverrides` / `SettingsPath`**

- `SettingsOverrides`：以结构体方式按字段“高优先级 merge”到前面的所有文件上。
- `SettingsPath`：允许加载一份额外 JSON 文件，并以高优先级 merge。

> 以上四层的合并由 `config.SettingsLoader` + `api.loadSettings` 完成，最终得到一份 `*config.Settings`，供整个 runtime 使用。

**5）API 选项 `api.Options`**

- `api.New(ctx, api.Options{...})` 中的字段在很多场景下拥有 **最高优先级**，用于按入口（CLI / HTTP / 嵌入式）定制行为。
- 例如：
  - 模型池：`ModelPool` / `SubagentModelMapping`
  - Sandbox 根目录 / 网络白名单：`SandboxOptions`
  - Token 统计：`TokenTracking` / `TokenCallback`
  - 权限交互：`PermissionRequestHandler` / `ApprovalQueue` / `ApprovalWait` 等

> 一句话：**文件描述“项目默认行为”，`SettingsOverrides` 做 batch 覆盖，`api.Options` 再进行入口级/调用级定制。**

---

## 运行时配置总览（api.New + settings）

### Runtime 创建流程（简版）

当你调用：

```go
rt, err := api.New(ctx, api.Options{
    ProjectRoot: ".",
    ModelFactory: api.ModelFactoryFunc(...),
    SettingsOverrides: &config.Settings{ /* 可选覆盖 */ },
    // 其它选项...
})
```

`api.New` 会按如下顺序 wiring：

1. **文件系统抽象层**：`config.NewFS(ProjectRoot, EmbedFS)`
2. **加载 .claude/hooks**：`materializeEmbeddedClaudeHooks`
3. **加载 CLAUDE.md 作为 System Prompt 记忆**
4. **加载 settings**（关键）：
   - 调 `loadSettings(opts)` → `config.SettingsLoader.Load()`：
     - 默认值 → `.claude/settings.json` → `.claude/settings.local.json` → `SettingsOverrides`
   - 若设置了 `SettingsPath`，再读一份 JSON overlay 并 merge。
   - 再次应用 `SettingsOverrides`，确保运行时覆盖权重最高。
5. **解析模型**：`resolveModel` / `ModelFactory`
6. **构建 SandboxManager**：`buildSandboxManager(opts, settings)`
7. **加载 commands/skills/subagents**（来自 `.claude/commands`、`.claude/skills`、`.claude/agents`）
8. **注册内置 & 自定义 tools**：`registerTools(registry, opts, settings, ...)`
9. **收集 MCP 服务器**：`collectMCPServers(settings, opts.MCPServers)`
10. **创建 Executor / Hooks / Compactor / Tracer / 历史存储** 等

Runtime 内部所有需要配置的信息，**都应从这一步得到的 `settings` 和 `api.Options` 来获取**，避免重复读取磁盘文件。

---

## 完整配置结构

```json
{
  "apiKeyHelper": "",
  "cleanupPeriodDays": 30,
  "companyAnnouncements": [],
  "env": {},
  "includeCoAuthoredBy": true,
  "permissions": {
    "allow": [],
    "ask": [],
    "deny": [],
    "additionalDirectories": [],
    "defaultMode": "askBeforeRunningTools",
    "disableBypassPermissionsMode": ""
  },
  "disallowedTools": [],
  "hooks": {
    "PreToolUse": [],
    "PostToolUse": [],
    "PermissionRequest": [],
    "SessionStart": [],
    "SessionEnd": []
  },
  "disableAllHooks": false,
  "model": "",
  "statusLine": {
    "type": "",
    "command": "",
    "template": "",
    "intervalSeconds": 0,
    "timeoutSeconds": 0
  },
  "outputStyle": "",
  "mcp": {
    "servers": []
  },
  "sandbox": {
    "enabled": false,
    "autoAllowBashIfSandboxed": true,
    "excludedCommands": [],
    "allowUnsandboxedCommands": true,
    "enableWeakerNestedSandbox": false,
    "network": {
      "allowUnixSockets": [],
      "allowLocalBinding": false,
      "httpProxyPort": 0,
      "socksProxyPort": 0
    }
  },
  "bashOutput": {
    "syncThresholdBytes": 30000,
    "asyncThresholdBytes": 1048576
  },
  "toolOutput": {
    "defaultThresholdBytes": 0,
    "perToolThresholdBytes": {}
  },
  "respectGitignore": true
}
```

---

## Permissions 配置（权限控制）

### 字段说明

| 字段 | 类型 | 必需 | 说明 |
|------|------|------|------|
| `allow` | `[]string` | 否 | 自动允许的工具规则列表 |
| `ask` | `[]string` | 否 | 需要确认的工具规则列表（触发 Approval Queue） |
| `deny` | `[]string` | 否 | 禁止的工具规则列表 |
| `additionalDirectories` | `[]string` | 否 | Sandbox 额外允许访问的目录 |
| `defaultMode` | `string` | 否 | 默认权限模式（仅验证，未在运行时使用） |
| `disableBypassPermissionsMode` | `string` | 否 | 设置为 `"disable"` 禁用 bypass 模式（仅验证） |

### 规则格式

#### 1. 工具名匹配（简单格式）

匹配整个工具，不区分大小写。

```json
{
  "permissions": {
    "ask": ["bash", "Bash", "Read", "Write"],
    "deny": ["rm", "sudo"]
  }
}
```

支持 glob 通配符：
- `"ba*"` - 匹配 `bash`、`base` 等
- `"*read*"` - 匹配包含 "read" 的工具名

#### 2. 工具名 + 目标模式（完整格式）

格式：`ToolName(pattern)`

```json
{
  "permissions": {
    "allow": [
      "Bash(ls:*)",
      "Bash(pwd:*)",
      "Bash(echo:*)",
      "Read(**/*.md)",
      "Read(**/*.txt)",
      "Glob(**/*.go)"
    ],
    "ask": [
      "Bash(rm:*)",
      "Bash(mv:*)",
      "Bash(cp:*)",
      "Bash(curl:*)",
      "Bash(wget:*)",
      "Write(**/*)",
      "Edit(**/*)",
      "Read(**/secret.md)"
    ],
    "deny": [
      "Bash(sudo:*)",
      "Bash(su:*)",
      "Bash(regex:^rm.*/$)",
      "Read(**/.env)",
      "Read(**/.gitconfig)",
      "Read(**/secrets/**)",
      "Read(**/*.key)",
      "Read(**/*.pem)"
    ]
  }
}
```

**Bash 工具的目标格式：**

Bash 命令的目标格式为 `命令名:参数`。

| 规则 | 说明 | 示例匹配 |
|------|------|----------|
| `"Bash(ls:*)"` | 允许 `ls` 命令及任意参数 | `ls -la`、`ls /tmp` |
| `"Bash(ls:-la)"` | 仅允许 `ls -la` | `ls -la` |
| `"Bash(rm:*)"` | 匹配 `rm` 命令及任意参数 | `rm file.txt`、`rm -rf /` |
| `"Bash(regex:^ls:.*$)"` | 使用正则表达式 | 所有 `ls` 命令 |

**文件操作工具的目标格式：**

| 规则 | 说明 | 匹配示例 |
|------|------|----------|
| `"**/*.md"` | 所有 Markdown 文件 | `/readme.md`、`/docs/guide.md` |
| `"**/secret.md"` | 所有名为 secret.md 的文件 | `/config/secret.md` |
| `"**/.env"` | 所有 .env 文件 | `/.env`、`/config/.env` |
| `"regex:.*\\.env$"` | 正则匹配 .env 结尾 | 同上 |

#### 3. 纯路径规则

不包含 `()` 的规则会被视为路径规则，匹配所有工具的目标路径。

```json
{
  "permissions": {
    "deny": [
      "**/secret.txt",
      "**/.env",
      "**/credentials/**"
    ],
    "allow": [
      "**/*.md",
      "**/*.txt",
      "**/README*"
    ]
  }
}
```

#### 4. 正则表达式规则

使用 `regex:` 或 `regexp:` 前缀。

```json
{
  "permissions": {
    "allow": [
      "Bash(regex:^ls:.*$)",
      "Bash(regex:^grep:.*\\.go$)"
    ],
    "deny": [
      "Read(regex:.*secret.*)",
      "Bash(regex:^rm.*-rf.*$)"
    ]
  }
}
```

#### 5. MCP 工具规则

MCP 工具名格式为 `mcp__serverName__toolName`。

```json
{
  "permissions": {
    "allow": [
      "mcp__demo__*",
      "mcp__github__list_repos"
    ],
    "deny": [
      "mcp__demo__danger",
      "mcp__admin__*"
    ]
  }
}
```

### 权限优先级

权限检查按以下优先级（从高到低）：

1. **Deny** - 拒绝规则优先
2. **Ask** - 询问规则次之
3. **Allow** - 允许规则最后
4. **Unknown** - 没有匹配规则时返回未知（默认为允许）

### AdditionalDirectories（额外目录）

定义 Sandbox 允许访问的额外目录。

```json
{
  "permissions": {
    "additionalDirectories": [
      "/tmp",
      "/var/log",
      "/data/project",
      "../shared"
    ]
  }
}
```

**注意：**
- 路径可以是相对路径或绝对路径
- 相对路径会解析为绝对路径
- 空路径会被忽略

---

## Sandbox 配置（沙箱设置）

### 字段说明

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | `*bool` | `false` | 启用文件系统/网络沙箱 |
| `autoAllowBashIfSandboxed` | `*bool` | `true` | 沙箱启用时自动允许 Bash 命令 |
| `excludedCommands` | `[]string` | `[]` | 必须在沙箱外运行的命令 |
| `allowUnsandboxedCommands` | `*bool` | `true` | 允许 dangerouslyDisableSandbox 转义 |
| `enableWeakerNestedSandbox` | `*bool` | `false` | 为非特权 Docker 启用较弱沙箱 |
| `network` | `object` | - | 网络沙箱配置 |

### Network 配置

| 字段 | 类型 | 说明 |
|------|------|------|
| `allowUnixSockets` | `[]string` | 允许访问的 Unix socket（SSH agent、docker socket） |
| `allowLocalBinding` | `*bool` | 允许绑定到 localhost 端口（macOS） |
| `httpProxyPort` | `*int` | 自定义 HTTP 代理端口 |
| `socksProxyPort` | `*int` | 自定义 SOCKS5 代理端口 |

### 配置示例

```json
{
  "sandbox": {
    "enabled": true,
    "autoAllowBashIfSandboxed": false,
    "excludedCommands": ["docker", "kubectl"],
    "allowUnsandboxedCommands": false,
    "network": {
      "allowUnixSockets": ["/var/run/docker.sock"],
      "allowLocalBinding": true
    }
  }
}
```

---

## Hooks 配置（钩子）

### 事件类型

| 事件 | 触发时机 |
|------|----------|
| `PreToolUse` | 工具执行前 |
| `PostToolUse` | 工具执行后 |
| `PostToolUseFailure` | 工具执行失败后 |
| `PermissionRequest` | 权限请求时 |
| `SessionStart` | 会话开始时 |
| `SessionEnd` | 会话结束时 |
| `SubagentStart` | 子代理启动时 |
| `SubagentStop` | 子代理停止时 |
| `Stop` | 停止事件 |
| `Notification` | 通知事件 |
| `UserPromptSubmit` | 用户提交提示时 |
| `PreCompact` | 上下文压缩前 |

### Hook 定义

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "bash",
        "hooks": [
          {
            "type": "command",
            "command": "echo 'Bash command: {{.tool_name}}'",
            "timeout": 30
          }
        ]
      }
    ],
    "PermissionRequest": [
      {
        "matcher": "bash",
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/approval-script.sh",
            "async": false,
            "once": false
          }
        ]
      }
    ]
  }
}
```

### Hook 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `type` | `string` | 钩子类型：`command`、`prompt`、`agent` |
| `command` | `string` | shell 命令（type=command） |
| `prompt` | `string` | LLM 提示（type=prompt） |
| `model` | `string` | 模型覆盖（type=prompt/agent） |
| `timeout` | `int` | 超时秒数（0=默认） |
| `async` | `bool` | 异步执行（fire-and-forget） |
| `once` | `bool` | 每会话只执行一次 |
| `statusMessage` | `string` | 执行期间显示的状态消息 |

---

## MCP 配置（Model Context Protocol）

### 配置示例

```json
{
  "mcp": {
    "servers": [
      {
        "name": "github",
        "type": "stdio",
        "command": "node",
        "args": ["/path/to/github-mcp-server.js"],
        "env": {
          "GITHUB_TOKEN": "{{env.GITHUB_TOKEN}}"
        }
      },
      {
        "name": "api-server",
        "type": "sse",
        "url": "http://localhost:3000/mcp",
        "headers": {
          "Authorization": "Bearer {{env.API_TOKEN}}"
        },
        "timeoutSeconds": 30
      }
    ]
  }
}
```

### MCP 服务器字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | `string` | 服务器名称（唯一标识） |
| `type` | `string` | 类型：`stdio`、`http`、`sse` |
| `command` | `string` | 命令（stdio 类型） |
| `args` | `[]string` | 命令参数 |
| `url` | `string` | URL（http/sse 类型） |
| `env` | `map[string]string` | 环境变量 |
| `headers` | `map[string]string` | HTTP 头 |
| `timeoutSeconds` | `int` | 连接超时 |
| `enabledTools` | `[]string` | 允许的工具白名单 |
| `disabledTools` | `[]string` | 禁止的工具黑名单 |
| `toolTimeoutSeconds` | `int` | 工具调用超时 |

---

## 其他配置

### API Key Helper

用于动态获取 API key 的脚本路径。

```json
{
  "apiKeyHelper": "/path/to/get-api-key.sh"
}
```

### Cleanup Period Days

本地聊天记录保留天数（0=禁用清理）。

```json
{
  "cleanupPeriodDays": 30
}
```

### Environment Variables

应用到每个会话的环境变量。

```json
{
  "env": {
    "MY_VAR": "value",
    "API_ENDPOINT": "https://api.example.com"
  }
}
```

### Disallowed Tools

工具黑名单（不注册这些工具）。

```json
{
  "disallowedTools": ["bash", "file_write"]
}
```

### Model

覆盖默认模型 ID。

```json
{
  "model": "claude-3-5-sonnet-20241022"
}
```

### Output Style

输出样式名称。

```json
{
  "outputStyle": "concise"
}
```

### Respect Gitignore

Glob/Grep 工具是否尊重 `.gitignore`。

```json
{
  "respectGitignore": true
}
```

---

## 与 api.Options 的关系与覆盖规则

本节从“使用 `api.New` 时我能改什么？”的角度，说明 `.claude/settings*.json` 与 `api.Options` 之间的关系。

### 1. 模型相关

- **settings**：
  - `settings.model`：用于覆盖默认模型 ID（更多是兼容 Claude Code CLI 的字段）。
- **Options**：
  - `Options.Model` / `ModelFactory`：直接指定底层模型客户端，是最核心的一层。
  - `Options.ModelPool map[ModelTier]model.Model`：配置 low/mid/high 三档模型池。
  - `Options.SubagentModelMapping map[string]ModelTier`：给不同子代理（`general-purpose` / `explore` / `plan` 等）绑定不同档位。
- **优先级示意**：
  - 实际使用的模型完全由 `Options` 决定；`settings.model` 更多是 CLI 场景的默认值。

### 2. Sandbox 与权限

- **settings**：
  - `settings.sandbox.enabled`：是否启用文件系统/网络沙箱。
  - `settings.permissions.*`：工具权限规则（`allow/ask/deny` + `additionalDirectories`）。
- **Options**：
  - `Options.Sandbox.Root`：沙箱根目录，默认是 `ProjectRoot`。
  - `Options.Sandbox.AllowedPaths`：额外允许的路径，最终会与 `permissions.additionalDirectories` 一起进入 fs allowlist。
  - `Options.Sandbox.NetworkAllow`：网络白名单（当不为空时优先于默认本地网段）。
  - `Options.Sandbox.ResourceLimit`：CPU/内存/磁盘配额。

**权限加载（重要行为）**：

- `api.New` 通过 `buildSandboxManager(opts, settings)` 构造 `sandbox.Manager`：
  - 文件系统策略：`sandbox.NewFileSystemAllowList(root, ...)`，会同时加入：
    - `Options.Sandbox.Root` / `ProjectRoot`
    - `settings.permissions.additionalDirectories`
    - `Options.Sandbox.AllowedPaths`
  - 网络策略：`sandbox.NewDomainAllowList(...)`，优先使用 `Options.Sandbox.NetworkAllow`，否则使用 `defaultNetworkAllowList`。
  - 资源限制：`sandbox.NewResourceLimiter(opts.Sandbox.ResourceLimit)`。
- 构造完成后，`api.New` 会调用：

```go
mgr.ConfigurePermissions(root, settings)
```

- 这一步会从 **同一份 `settings`** 中创建 `security.PermissionMatcher`，
  并注入到 `sandbox.Manager` 内部的 `security.Sandbox` 中。
- 之后，所有工具权限判定都走 `Manager.CheckToolPermission`：
  - 规则优先级：**deny > ask > allow > unknown(=allow)**（见本文件前文）。
  - 这样保证：
    - `.claude/settings*.json` + `SettingsOverrides` 形成的最终权限配置，是运行时唯一的数据来源，
    - 不会再由其它模块单独重新读取 `settings.json`。

> 建议：**权限规则始终通过 `.claude/settings*.json` 管理**，只在 `api.Options.Sandbox` 上做运行环境相关的 override（根路径、网络、资源限制）。

### 3. 工具输出落盘（toolOutput）与 api.Executor

- **settings**：
  - `settings.toolOutput`：
    - `defaultThresholdBytes`：整体阈值，超过则写入磁盘。
    - `perToolThresholdBytes`：按工具名（不区分大小写）配置阈值。
- **Options**：
  - 当前不直接暴露对应字段，而是在 `api.New` 中消费 `settings.ToolOutput` 配置：

```go
persister := tool.NewOutputPersister()
if settings != nil && settings.ToolOutput != nil {
    cfg := settings.ToolOutput
    if cfg.DefaultThresholdBytes > 0 {
        persister.DefaultThresholdBytes = cfg.DefaultThresholdBytes
    }
    if len(cfg.PerToolThresholdBytes) > 0 {
        perTool := make(map[string]int, len(cfg.PerToolThresholdBytes))
        for name, v := range cfg.PerToolThresholdBytes {
            canon := strings.ToLower(strings.TrimSpace(name))
            if canon == "" || v <= 0 {
                continue
            }
            perTool[canon] = v
        }
        persister.PerToolThresholdBytes = perTool
    }
}
executor := tool.NewExecutor(registry, sbox).WithOutputPersister(persister)
```

- **效果**：
  - `.claude/settings.json` 中的 `toolOutput` 配置会直接影响：
    - 什么时候将 `ToolResult.Output` 落盘到 `/tmp/agentsdk/tool-output/{session}/{tool}/{ts}.output`；
    - 哪些工具的输出更早或更晚被持久化。

### 4. Approval Queue 与权限交互

- **settings**：
  - 目前不直接包含 ApprovalQueue 的专门字段（将来可以引入 `approval.storePath` 等）。
  - 但 `permissions.ask` 规则会触发“需要确认”的流程。
- **Options**：
  - `Options.PermissionRequestHandler`：宿主自定义的询问逻辑。
  - `Options.ApprovalQueue`：可选的持久化 approval 队列（`security.ApprovalQueue`）。
  - `Options.ApprovalApprover`：记录在队列里的审批人标识。
  - `Options.ApprovalWhitelistTTL`：审批通过后 session 级白名单的生存时间。
  - `Options.ApprovalWait`：是否阻塞等待外部审批完成。

在 `Runtime` 中，这些选项通过 `buildPermissionResolver` 接入：

```go
permissionResolver := buildPermissionResolver(
    hookAdapter,
    rt.opts.PermissionRequestHandler,
    rt.opts.ApprovalQueue,
    rt.opts.ApprovalApprover,
    rt.opts.ApprovalWhitelistTTL,
    rt.opts.ApprovalWait,
)
```

而 `runtimeToolExecutor.Execute` 在 `PreToolUse` hook 返回 `ErrToolUseRequiresApproval` 时，会调用上述 resolver：

- 如果 `ApprovalQueue` 配置了存储路径，则：
  - `permissions.ask` 命中的调用会进入队列；
  - 根据 `ApprovalWait` 决定是立即返回“待审批”，还是阻塞直到外部审批。

> 典型用法：在 CLI / HTTP 服务中，基于 `ProjectRoot` 创建一个 `ApprovalQueue` 并填充到 `Options.ApprovalQueue`，结合 `permissions.ask` 实现“高风险操作需要人工批准”的工作流。

---

## 完整配置示例

### 最小配置（仅启用 Approval Queue）

```json
{
  "permissions": {
    "ask": ["bash"]
  }
}
```

### 开发环境配置

```json
{
  "permissions": {
    "allow": [
      "Bash(ls:*)",
      "Bash(pwd:*)",
      "Bash(echo:*)",
      "Bash(grep:*)",
      "Bash(find:*)",
      "Read(**/*)",
      "Glob(**/*)"
    ],
    "ask": [
      "Bash(rm:*)",
      "Bash(mv:*)",
      "Bash(cp:*)",
      "Write(**/*)",
      "Edit(**/*)"
    ],
    "deny": [
      "Bash(sudo:*)",
      "Bash(su:*)",
      "Bash(dd:*)",
      "Bash(mkfs:*)",
      "Bash(regex:^rm.*-rf.*/$)"
    ],
    "additionalDirectories": [
      "/tmp",
      "/var/log"
    ]
  },
  "sandbox": {
    "enabled": false,
    "autoAllowBashIfSandboxed": true
  },
  "env": {
    "NODE_ENV": "development"
  }
}
```

### 生产环境配置

```json
{
  "permissions": {
    "ask": [
      "Bash(*)",
      "Write(**/*)",
      "Edit(**/*)"
    ],
    "deny": [
      "Bash(sudo:*)",
      "Bash(su:*)",
      "Read(**/.env)",
      "Read(**/secrets/**)",
      "Read(**/*.key)",
      "Read(**/*.pem)"
    ]
  },
  "sandbox": {
    "enabled": true,
    "autoAllowBashIfSandboxed": false,
    "allowUnsandboxedCommands": false,
    "network": {
      "allowUnixSockets": []
    }
  },
  "disallowedTools": ["bash"],
  "env": {
    "NODE_ENV": "production"
  }
}
```

### CI/CD 环境配置

```json
{
  "permissions": {
    "allow": [
      "Bash(ls:*)",
      "Bash(pwd:*)",
      "Bash(echo:*)",
      "Bash(cat:*)",
      "Bash(grep:*)",
      "Read(**/*)",
      "Write(/tmp/**)"
    ],
    "deny": [
      "Bash(sudo:*)",
      "Bash(curl:*)",
      "Bash(wget:*)",
      "Read(**/.env)"
    ]
  },
  "sandbox": {
    "enabled": true
  },
  "disableAllHooks": true
}
```

---

## 配置文件位置

### 项目级配置

```
project-root/
├── .claude/
│   ├── settings.json          # 项目配置（提交到 git）
│   ├── settings.local.json    # 本地覆盖（gitignored）
│   ├── skills/              # 技能定义
│   ├── commands/            # 斜杠命令
│   └── agents/            # 子代理定义
```

### 配置合并示例

**settings.json：**
```json
{
  "permissions": {
    "ask": ["bash"]
  },
  "env": {
    "SHARED_VAR": "value"
  }
}
```

**settings.local.json：**
```json
{
  "permissions": {
    "allow": ["Bash(ls:*"]
  },
  "env": {
    "LOCAL_VAR": "local-value"
  }
}
```

**最终生效配置：**
```json
{
  "permissions": {
    "ask": ["bash"],
    "allow": ["Bash(ls:*"]
  },
  "env": {
    "SHARED_VAR": "value",
    "LOCAL_VAR": "local-value"
  }
}
```

---

## 常见问题

### Q: 为什么必须有 `.claude/settings.json` 文件？

A: SDK 的权限检查机制 `CheckToolPermission` 会从文件系统加载配置，而不是使用 `SettingsOverrides`。如果没有配置文件，`permissions` 为 nil，所有工具默认允许。

### Q: `defaultMode` 和 `disableBypassPermissionsMode` 有什么用？

A: 这两个字段目前仅在配置验证时使用，没有在运行时实际使用。它们是为了与 Claude Code CLI 的配置格式兼容。

### Q: 如何调试权限规则？

A: 可以使用 `security.PermissionAudits()` 查看权限决策审计日志。

### Q: 规则匹配是大小写敏感的吗？

A: 工具名匹配不区分大小写（`bash` 和 `Bash` 相同），但目标路径匹配区分大小写（取决于文件系统）。

---

## 参考

- [Claude Code 官方文档](https://docs.anthropic.com/en/docs/claude-code/settings)
- [agentsdk-go GitHub](https://github.com/cexll/agentsdk-go)
