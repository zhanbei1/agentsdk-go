package middleware

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

const reflectionKey = "reflection.records"

type ReflectionRecord struct {
	Stage     string            `json:"stage"`
	When      time.Time         `json:"when"`
	Iteration int               `json:"iteration"`
	ToolName  string            `json:"tool_name,omitempty"`
	ToolUseID string            `json:"tool_use_id,omitempty"`
	Kind      string            `json:"kind"`
	Message   string            `json:"message"`
	Advice    string            `json:"advice,omitempty"`
	Evidence  map[string]string `json:"evidence,omitempty"`
}

// ReflectionMiddleware writes structured reflection records into middleware.State.Values.
// It never blocks execution and never returns errors.
type ReflectionMiddleware struct{}

func NewReflectionMiddleware() *ReflectionMiddleware { return &ReflectionMiddleware{} }

func (r *ReflectionMiddleware) Name() string { return "reflection" }

func (r *ReflectionMiddleware) BeforeAgent(context.Context, *State) error { return nil }
func (r *ReflectionMiddleware) BeforeTool(context.Context, *State) error  { return nil }

func (r *ReflectionMiddleware) AfterTool(ctx context.Context, st *State) error {
	if st == nil {
		return nil
	}
	toolName, toolUseID := extractToolCallInfo(st.ToolCall)
	toolErr := extractError(st.ToolResult)
	if toolErr == nil {
		return nil
	}
	rec := ReflectionRecord{
		Stage:     "AfterTool",
		When:      time.Now(),
		Iteration: st.Iteration,
		ToolName:  toolName,
		ToolUseID: toolUseID,
		Kind:      classifyError(toolErr),
		Message:   toolErr.Error(),
		Advice:    adviceFor(toolErr),
		Evidence:  map[string]string{},
	}
	if st.Values != nil {
		if sid, ok := st.Values["session_id"].(string); ok && strings.TrimSpace(sid) != "" {
			rec.Evidence["session_id"] = sid
		}
		if rid, ok := st.Values["request_id"].(string); ok && strings.TrimSpace(rid) != "" {
			rec.Evidence["request_id"] = rid
		}
	}
	appendReflectionRecord(st, rec)
	_ = ctx
	return nil
}

func (r *ReflectionMiddleware) AfterAgent(ctx context.Context, st *State) error {
	if st == nil {
		return nil
	}
	// If AfterAgent is invoked with a model stop reason indicating trouble, record it.
	if st.Values != nil {
		if sr, ok := st.Values["model.stop_reason"].(string); ok {
			sr = strings.TrimSpace(sr)
			if sr == "length" || sr == "max_tokens" {
				rec := ReflectionRecord{
					Stage:     "AfterAgent",
					When:      time.Now(),
					Iteration: st.Iteration,
					Kind:      "token_limit",
					Message:   "model stopped due to token/length limits",
					Advice:    "启用/调高 compaction 或减少工具输出写入上下文；必要时提高 TokenLimit。",
					Evidence:  map[string]string{"stop_reason": sr},
				}
				appendReflectionRecord(st, rec)
			}
		}
	}
	_ = ctx
	return nil
}

func appendReflectionRecord(st *State, rec ReflectionRecord) {
	if st == nil {
		return
	}
	if st.Values == nil {
		st.Values = map[string]any{}
	}
	raw := st.Values[reflectionKey]
	switch v := raw.(type) {
	case []ReflectionRecord:
		st.Values[reflectionKey] = append(v, rec)
	case []any:
		// tolerate external injection
		st.Values[reflectionKey] = append(v, rec)
	default:
		st.Values[reflectionKey] = []ReflectionRecord{rec}
	}
}

func classifyError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "validation"):
		return "validation"
	case strings.Contains(msg, "not whitelisted"):
		return "whitelist"
	case strings.Contains(msg, "blocked"):
		return "safety_denied"
	case strings.Contains(msg, "mcp") && strings.Contains(msg, "session"):
		return "mcp"
	default:
		return "tool_error"
	}
}

func adviceFor(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "工具超时：尝试缩小输入/分页；或提高 ToolTimeout。"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not whitelisted"):
		return "工具未在白名单：在 ToolWhitelist 中加入该工具，或在 Skylark 模式下先 retrieve_capabilities 并 unlock。"
	case strings.Contains(msg, "validation"):
		return "参数校验失败：检查 schema required 字段与参数类型。"
	case strings.Contains(msg, "blocked"):
		return "被安全钩子拦截：改用更安全的命令/拆分操作，或显式关闭安全钩子（不推荐）。"
	default:
		return ""
	}
}

func extractToolCallInfo(v any) (name string, id string) {
	if v == nil {
		return "", ""
	}
	rv := reflect.Indirect(reflect.ValueOf(v))
	if rv.IsValid() && rv.Kind() == reflect.Struct {
		if f := rv.FieldByName("Name"); f.IsValid() && f.Kind() == reflect.String {
			name = f.String()
		}
		if f := rv.FieldByName("ID"); f.IsValid() && f.Kind() == reflect.String {
			id = f.String()
		}
	}
	return strings.TrimSpace(name), strings.TrimSpace(id)
}

func extractError(v any) error {
	if v == nil {
		return nil
	}
	if err, ok := v.(error); ok {
		return err
	}
	rv := reflect.Indirect(reflect.ValueOf(v))
	if !rv.IsValid() || rv.Kind() != reflect.Struct {
		return nil
	}
	// common pattern: field "Err error"
	f := rv.FieldByName("Err")
	if !f.IsValid() || f.IsZero() {
		return nil
	}
	if f.CanInterface() {
		if err, ok := f.Interface().(error); ok {
			return err
		}
	}
	return fmt.Errorf("%v", f)
}
