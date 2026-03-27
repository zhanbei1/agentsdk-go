package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime"
	"time"

	"github.com/stellarlinkco/agentsdk-go/pkg/hooks"
	"github.com/stellarlinkco/agentsdk-go/pkg/message"
	"github.com/stellarlinkco/agentsdk-go/pkg/model"
	"github.com/stellarlinkco/agentsdk-go/pkg/runtime/subagents"
	"github.com/stellarlinkco/agentsdk-go/pkg/sandbox"
	"github.com/stellarlinkco/agentsdk-go/pkg/tool"
)

type runtimeToolExecutor struct {
	executor  *tool.Executor
	hooks     *runtimeHookAdapter
	history   *message.History
	allow     map[string]struct{}
	skylark   *skylarkAllowState
	root      string
	host      string
	sessionID string
}

func (t *runtimeToolExecutor) measureUsage() sandbox.ResourceUsage {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return sandbox.ResourceUsage{MemoryBytes: stats.Alloc}
}

func (t *runtimeToolExecutor) isAllowed(ctx context.Context, name string) bool {
	canon := canonicalToolName(name)
	if canon == "" {
		return false
	}
	if t.skylark != nil {
		if !t.skylark.isAllowed(name) {
			return false
		}
		subCtx, ok := subagents.FromContext(ctx)
		if !ok || len(subCtx.ToolWhitelist) == 0 {
			return true
		}
		subSet := toLowerSet(subCtx.ToolWhitelist)
		_, subAllowed := subSet[canon]
		return subAllowed
	}
	reqAllowed := len(t.allow) == 0
	if len(t.allow) > 0 {
		_, reqAllowed = t.allow[canon]
	}
	subCtx, ok := subagents.FromContext(ctx)
	if !ok || len(subCtx.ToolWhitelist) == 0 {
		return reqAllowed
	}
	subSet := toLowerSet(subCtx.ToolWhitelist)
	if len(subSet) == 0 {
		return reqAllowed
	}
	_, subAllowed := subSet[canon]
	if len(t.allow) == 0 {
		return subAllowed
	}
	return reqAllowed && subAllowed
}

func (t *runtimeToolExecutor) appendToolMessage(call model.ToolCall, content string) {
	if t.history == nil {
		return
	}
	t.history.Append(message.Message{
		Role: "tool",
		ToolCalls: []message.ToolCall{{
			ID:     call.ID,
			Name:   call.Name,
			Result: content,
		}},
	})
}

// toolPreparation holds the outcome of allow/empty-args/pre-hook phases (no registry invoke).
type toolPreparation struct {
	Call model.ToolCall

	// Denied is set when the tool is not allowed (no history mutation here).
	Denied error

	// EmptyArgsResult is returned when required params are missing (caller should append + skip invoke).
	EmptyArgsResult *tool.CallResult

	// PreHookErr is set when PreToolUse fails (caller should append error JSON + skip invoke).
	PreHookErr error
}

func (t *runtimeToolExecutor) prepareToolCall(ctx context.Context, call model.ToolCall) toolPreparation {
	if t.executor == nil {
		return toolPreparation{Call: call, Denied: errors.New("tool executor not initialised")}
	}
	if !t.isAllowed(ctx, call.Name) {
		return toolPreparation{Call: call, Denied: fmt.Errorf("tool %s is not whitelisted", call.Name)}
	}

	if len(call.Arguments) == 0 {
		if reg := t.executor.Registry(); reg != nil {
			if impl, err := reg.Get(call.Name); err == nil {
				if schema := impl.Schema(); schema != nil && len(schema.Required) > 0 {
					errMsg := fmt.Sprintf(
						"tool %q called with empty arguments but requires %v; "+
							"the API proxy likely stripped tool_use.input — check proxy configuration",
						call.Name, schema.Required)
					log.Printf("WARNING: %s (id=%s)", errMsg, call.ID)
					now := time.Now()
					return toolPreparation{
						Call: call,
						EmptyArgsResult: &tool.CallResult{
							Call:        tool.Call{Name: call.Name, Params: map[string]any{}, SessionID: t.sessionID},
							Result:      &tool.ToolResult{Success: false, Output: errMsg, Data: map[string]any{"error": "empty_arguments"}},
							StartedAt:   now,
							CompletedAt: now,
						},
					}
				}
			}
		}
	}

	var (
		params map[string]any
		preErr error
	)
	if t.hooks != nil {
		params, preErr = t.hooks.PreToolUse(ctx, coreToolUsePayload(call))
	}
	if preErr != nil {
		return toolPreparation{Call: call, PreHookErr: preErr}
	}
	if params != nil {
		call.Arguments = params
	}
	return toolPreparation{Call: call}
}

func (t *runtimeToolExecutor) invokeToolCall(ctx context.Context, call model.ToolCall) (*tool.CallResult, error, string) {
	callSpec := tool.Call{
		Name:      call.Name,
		Params:    call.Arguments,
		Path:      t.root,
		Host:      t.host,
		Usage:     t.measureUsage(),
		SessionID: t.sessionID,
	}
	if emit := streamEmitFromContext(ctx); emit != nil {
		callSpec.StreamSink = func(chunk string, isStderr bool) {
			evt := StreamEvent{
				Type:      EventToolExecutionOutput,
				ToolUseID: call.ID,
				Name:      call.Name,
				Output:    chunk,
			}
			evt.IsStderr = &isStderr
			emit(ctx, evt)
		}
	}
	result, err := t.executor.Execute(ctx, callSpec)
	content := ""
	if result != nil && result.Result != nil {
		content = result.Result.Output
	}
	if err != nil {
		content = fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return result, err, content
}

// finalizeToolCall runs PostToolUse when applicable and appends the tool message to history.
func (t *runtimeToolExecutor) finalizeToolCall(ctx context.Context, call model.ToolCall, result *tool.CallResult, execErr error, content string, prep toolPreparation) error {
	switch {
	case prep.Denied != nil:
		return prep.Denied
	case prep.EmptyArgsResult != nil:
		t.appendToolMessage(call, prep.EmptyArgsResult.Result.Output)
		return nil
	case prep.PreHookErr != nil:
		errContent := fmt.Sprintf(`{"error":%q}`, prep.PreHookErr.Error())
		t.appendToolMessage(call, errContent)
		return prep.PreHookErr
	}
	if t.hooks != nil {
		if hookErr := t.hooks.PostToolUse(ctx, coreToolResultPayload(call, result, execErr)); hookErr != nil && execErr == nil {
			t.appendToolMessage(call, content)
			return hookErr
		}
	}
	t.appendToolMessage(call, content)
	return nil
}

func (t *runtimeToolExecutor) Execute(ctx context.Context, call model.ToolCall) (*tool.CallResult, error) {
	prep := t.prepareToolCall(ctx, call)
	call = prep.Call
	switch {
	case prep.Denied != nil:
		return nil, prep.Denied
	case prep.EmptyArgsResult != nil:
		t.appendToolMessage(call, prep.EmptyArgsResult.Result.Output)
		return prep.EmptyArgsResult, nil
	case prep.PreHookErr != nil:
		errContent := fmt.Sprintf(`{"error":%q}`, prep.PreHookErr.Error())
		t.appendToolMessage(call, errContent)
		now := time.Now()
		return &tool.CallResult{
			Call:        tool.Call{Name: call.Name, Params: map[string]any{}, SessionID: t.sessionID},
			Result:      &tool.ToolResult{Success: false, Output: errContent, Data: map[string]any{"error": prep.PreHookErr.Error()}},
			Err:         prep.PreHookErr,
			StartedAt:   now,
			CompletedAt: now,
		}, prep.PreHookErr
	}
	result, err, content := t.invokeToolCall(ctx, call)
	if finErr := t.finalizeToolCall(ctx, call, result, err, content, toolPreparation{}); finErr != nil {
		return result, finErr
	}
	return result, err
}

func coreToolUsePayload(call model.ToolCall) hooks.ToolUsePayload {
	return hooks.ToolUsePayload{Name: call.Name, Params: call.Arguments}
}

func coreToolResultPayload(call model.ToolCall, res *tool.CallResult, err error) hooks.ToolResultPayload {
	payload := hooks.ToolResultPayload{Name: call.Name}
	if res != nil && res.Result != nil {
		payload.Result = res.Result.Output
		payload.Duration = res.Duration()
	}
	payload.Err = err
	return payload
}
