package api

import (
	"context"
	"errors"
	"maps"
	"strings"
	"sync"

	"github.com/stellarlinkco/agentsdk-go/pkg/middleware"
	"github.com/stellarlinkco/agentsdk-go/pkg/model"
	"github.com/stellarlinkco/agentsdk-go/pkg/tool"
)

type indexedToolCall struct {
	index int
	call  model.ToolCall
}

type toolExecution struct {
	call      model.ToolCall
	result    *tool.CallResult
	err       error
	beforeErr error
	afterErr  error
}

func partitionToolCalls(registry *tool.Registry, calls []model.ToolCall) ([]indexedToolCall, []indexedToolCall) {
	var concurrent []indexedToolCall
	var serial []indexedToolCall
	for i, call := range calls {
		if registry == nil {
			serial = append(serial, indexedToolCall{index: i, call: call})
			continue
		}
		impl, err := registry.Get(call.Name)
		if err != nil {
			serial = append(serial, indexedToolCall{index: i, call: call})
			continue
		}
		meta := tool.MetadataOf(impl)
		if meta.IsReadOnly && meta.IsConcurrencySafe {
			concurrent = append(concurrent, indexedToolCall{index: i, call: call})
			continue
		}
		serial = append(serial, indexedToolCall{index: i, call: call})
	}
	return concurrent, serial
}

func cloneMiddlewareState(base *middleware.State) *middleware.State {
	if base == nil {
		return &middleware.State{Values: map[string]any{}}
	}
	cloned := &middleware.State{
		Iteration:   base.Iteration,
		Agent:       base.Agent,
		ModelInput:  base.ModelInput,
		ModelOutput: base.ModelOutput,
		ToolCall:    base.ToolCall,
		ToolResult:  base.ToolResult,
	}
	if len(base.Values) > 0 {
		cloned.Values = maps.Clone(base.Values)
	} else {
		cloned.Values = map[string]any{}
	}
	return cloned
}

func (rt *Runtime) executeSingleToolCall(ctx context.Context, call model.ToolCall, tools *runtimeToolExecutor, chain *middleware.Chain, baseState *middleware.State, tracer Tracer, agentSpan SpanContext, sessionID, requestID string) toolExecution {
	exec := toolExecution{call: call}
	state := cloneMiddlewareState(baseState)
	state.ToolCall = call
	if err := chain.Execute(ctx, middleware.StageBeforeTool, state); err != nil {
		exec.beforeErr = err
	}
	if tools == nil {
		exec.err = errors.New("api: tool executor is nil")
		return exec
	}
	toolSpan := SpanContext(nil)
	if tracer != nil {
		toolSpan = tracer.StartToolSpan(agentSpan, strings.TrimSpace(call.Name))
	}
	res, err := tools.Execute(ctx, call)
	if tracer != nil {
		tracer.EndSpan(toolSpan, map[string]any{
			"session_id":  strings.TrimSpace(sessionID),
			"request_id":  strings.TrimSpace(requestID),
			"tool_use_id": strings.TrimSpace(call.ID),
			"tool_name":   strings.TrimSpace(call.Name),
		}, err)
	}
	exec.result = res
	exec.err = err
	state.ToolResult = res
	if afterErr := chain.Execute(ctx, middleware.StageAfterTool, state); afterErr != nil {
		exec.afterErr = afterErr
	}
	return exec
}

func (rt *Runtime) executeToolCalls(ctx context.Context, calls []model.ToolCall, tools *runtimeToolExecutor, chain *middleware.Chain, baseState *middleware.State, tracer Tracer, agentSpan SpanContext, req Request) error {
	concurrentCalls, serialCalls := partitionToolCalls(rt.registry, calls)

	var firstMiddlewareErr error
	recordMiddlewareErr := func(exec toolExecution) {
		if firstMiddlewareErr == nil && exec.beforeErr != nil {
			firstMiddlewareErr = exec.beforeErr
		}
		if firstMiddlewareErr == nil && exec.afterErr != nil {
			firstMiddlewareErr = exec.afterErr
		}
	}

	if len(concurrentCalls) > 0 {
		results := make([]toolExecution, len(concurrentCalls))
		groupCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		limit := rt.opts.ToolConcurrency
		if limit <= 0 {
			limit = 1
		}
		sem := make(chan struct{}, limit)
		var wg sync.WaitGroup
		errCh := make(chan error, len(concurrentCalls))
		concurrentExec := tools.withoutHistory()
		for i, item := range concurrentCalls {
			i := i
			item := item
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
				case <-groupCtx.Done():
					errCh <- groupCtx.Err()
					return
				}
				defer func() { <-sem }()
				results[i] = rt.executeSingleToolCall(groupCtx, item.call, concurrentExec, chain, baseState, tracer, agentSpan, req.SessionID, req.RequestID)
				if results[i].err != nil && (errors.Is(results[i].err, context.Canceled) || errors.Is(results[i].err, context.DeadlineExceeded)) {
					errCh <- results[i].err
					cancel()
					return
				}
			}()
		}
		wg.Wait()
		close(errCh)
		var groupErr error
		for err := range errCh {
			if err != nil {
				groupErr = err
				break
			}
		}
		for i, exec := range results {
			recordMiddlewareErr(exec)
			tools.appendCallResult(concurrentCalls[i].call, exec.result, exec.err)
		}
		if groupErr != nil {
			return groupErr
		}
	}

	for _, item := range serialCalls {
		exec := rt.executeSingleToolCall(ctx, item.call, tools, chain, baseState, tracer, agentSpan, req.SessionID, req.RequestID)
		recordMiddlewareErr(exec)
		if exec.err != nil && (errors.Is(exec.err, context.Canceled) || errors.Is(exec.err, context.DeadlineExceeded)) {
			return exec.err
		}
	}

	return firstMiddlewareErr
}
