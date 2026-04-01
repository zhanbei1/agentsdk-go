# 会话历史加载（Session History）

## 背景

`pkg/message.History` 仅在 **当前进程内存** 中保存多轮消息。`Runtime` 为每个 `SessionID` 维护一份 `History`；若业务 **每次请求是新进程**（例如无状态容器重启），此前轮次不会自动出现，除非在打开会话时把持久化记录灌回内存。

SDK 在 `historyStore` 上长期支持 **首次 `Get(sessionID)` 时调用可选 `loader`**，此前未从 `api.Options` 暴露。现在通过 `Options.SessionHistoryLoader` 等字段，由应用在 `NewRuntime` 时接入自己的存储层。

**说明**：Skylark 的 Bleve 索引 **不持久化用户聊天记录**；`retrieve_knowledge` 中的 `history` 只检索 **当前内存中的** `History`。跨进程续聊应依赖本节的 Loader（或你在请求路径上自行 `Append` / `Replace`）。

## 行为摘要

1. 某 `SessionID` **第一次**在本进程内被 `histories.Get` 使用时，若配置了 `SessionHistoryLoader`，则调用加载函数。
2. 若加载成功且返回非空 `[]message.Message`，则 `History.Replace(msgs)`，之后该会话与「进程内一直驻留」的行为一致。
3. `Loader` 返回错误或空切片时，行为与未配置 Loader 相同：从空历史开始（空切片不写入，见 `runtime_helpers.go` 中现有逻辑）。
4. `ForgetSession(sessionID)` 后，下一次对该 ID 的 `Get` 会 **跳过一次** Loader（避免删会话后立即又从存储拉回）；再次因淘汰等原因新建会话桶时，Loader 仍可再次运行（见既有 `session_cleanup_test`）。

## `api.Options` 字段

| 字段 | 含义 |
|------|------|
| `SessionHistoryLoader` | `func(sessionID string) ([]message.Message, error)`。从 DB / 文件 / Redis 等读取该会话消息。`nil` 表示不自动加载。 |
| `SessionHistoryMaxMessages` | `> 0` 时，在加载后 **只保留时间顺序上最后 N 条**，用于控制上下文长度。`0` 表示不截断。 |
| `SessionHistoryRoles` | 非空时 **按角色白名单** 过滤（`Role` 大小写不敏感）。空表示不过滤。 |
| `SessionHistoryTransform` | `func(sessionID string, msgs []message.Message) []message.Message`。在内置策略之后执行，可做自定义排序、按 token 裁剪、合并轮次等。 |

### 处理顺序

加载完成后依次执行：

1. 若 `SessionHistoryRoles` 非空 → 按角色过滤。  
2. 若 `SessionHistoryMaxMessages > 0` → 取末尾 N 条。  
3. 若 `SessionHistoryTransform` 非空 → 调用自定义变换。

## 辅助函数

定义在 `pkg/api/history_session.go`，可在 `SessionHistoryTransform` 或应用代码中复用：

- `FilterSessionMessagesByRole(msgs, roles...)`
- `TrimSessionMessages(msgs, max)`

## 不建议仅用 `assistant`

只保留 `SessionHistoryRoles: []string{"assistant"}` 会丢掉用户原始问题，多轮续聊时模型往往 **缺少提问上下文**。更常见的做法是：**保留 `user` 与 `assistant`**，用 `SessionHistoryMaxMessages` 或 Transform 按条数 / token 预算截断。

## 示例

```go
rt, err := api.NewRuntime(ctx, api.Options{
    // ...
    SessionHistoryLoader: func(sessionID string) ([]message.Message, error) {
        return yourStore.LoadMessages(sessionID)
    },
    SessionHistoryMaxMessages: 50,
    SessionHistoryTransform: func(id string, msgs []message.Message) []message.Message {
        // 可选：按项目策略再裁剪
        return msgs
    },
})
```

持久化格式、版本迁移与加密由应用自行负责；SDK 只约定 Loader 的输入为 `sessionID`，输出为 `[]message.Message`。

## 代码入口

- `pkg/api/options.go`：选项定义与 `frozen()` 中对 `SessionHistoryRoles` 的拷贝。  
- `pkg/api/agent.go`：`NewRuntime` 中将合成后的 loader 赋给 `histories.loader`。  
- `pkg/api/history_session.go`：策略组合与导出辅助函数。  
- `pkg/api/runtime_helpers.go`：`historyStore.Get` 内首次会话调用 `loader`。
