package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stellarlinkco/agentsdk-go/pkg/message"
	"github.com/stellarlinkco/agentsdk-go/pkg/middleware"
	"github.com/stellarlinkco/agentsdk-go/pkg/model"
	"github.com/stellarlinkco/agentsdk-go/pkg/tool"
)

type timedTool struct {
	name  string
	delay time.Duration
	meta  tool.Metadata

	mu      sync.Mutex
	started []time.Time
	ended   []time.Time
}

func (t *timedTool) Name() string             { return t.name }
func (t *timedTool) Description() string      { return t.name }
func (t *timedTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (t *timedTool) Metadata() tool.Metadata  { return t.meta }
func (t *timedTool) Execute(context.Context, map[string]any) (*tool.ToolResult, error) {
	t.mu.Lock()
	t.started = append(t.started, time.Now())
	t.mu.Unlock()
	time.Sleep(t.delay)
	t.mu.Lock()
	t.ended = append(t.ended, time.Now())
	t.mu.Unlock()
	return &tool.ToolResult{Success: true, Output: t.name}, nil
}

func (t *timedTool) firstStart() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.started) == 0 {
		return time.Time{}
	}
	return t.started[0]
}

func (t *timedTool) lastEnd() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.ended) == 0 {
		return time.Time{}
	}
	return t.ended[len(t.ended)-1]
}

func TestRunLoopExecutesReadOnlyConcurrencySafeToolsInParallel(t *testing.T) {
	t.Parallel()

	reg := tool.NewRegistry()
	read1 := &timedTool{name: "read1", delay: 120 * time.Millisecond, meta: tool.Metadata{IsReadOnly: true, IsConcurrencySafe: true}}
	read2 := &timedTool{name: "read2", delay: 120 * time.Millisecond, meta: tool.Metadata{IsReadOnly: true, IsConcurrencySafe: true}}
	read3 := &timedTool{name: "read3", delay: 120 * time.Millisecond, meta: tool.Metadata{IsReadOnly: true, IsConcurrencySafe: true}}
	write := &timedTool{name: "write", delay: 120 * time.Millisecond}
	for _, impl := range []tool.Tool{read1, read2, read3, write} {
		if err := reg.Register(impl); err != nil {
			t.Fatalf("register %s: %v", impl.Name(), err)
		}
	}

	mdl := &stubModel{responses: []*model.Response{
		{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{
			{ID: "r1", Name: "read1", Arguments: map[string]any{}},
			{ID: "r2", Name: "read2", Arguments: map[string]any{}},
			{ID: "r3", Name: "read3", Arguments: map[string]any{}},
			{ID: "w1", Name: "write", Arguments: map[string]any{}},
		}}},
		{Message: model.Message{Role: "assistant", Content: "done"}},
	}}

	rt := &Runtime{
		opts:     Options{ToolConcurrency: 3},
		registry: reg,
		executor: tool.NewExecutor(reg, nil),
	}
	prep := preparedRun{
		ctx:        context.Background(),
		prompt:     "hi",
		history:    message.NewHistory(),
		normalized: Request{SessionID: "s", RequestID: "r"},
	}

	start := time.Now()
	resp, err := rt.runLoop(prep, mdl, &runtimeHookAdapter{}, &runtimeToolExecutor{executor: rt.executor, hooks: &runtimeHookAdapter{}, history: prep.history, host: "localhost"}, middleware.NewChain(nil), false)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("runLoop: %v", err)
	}
	if resp == nil || resp.Message.Content != "done" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if elapsed >= 360*time.Millisecond {
		t.Fatalf("expected concurrent execution to finish faster, elapsed=%s", elapsed)
	}

	latestReadEnd := read1.lastEnd()
	for _, tool := range []*timedTool{read2, read3} {
		if end := tool.lastEnd(); end.After(latestReadEnd) {
			latestReadEnd = end
		}
	}
	if write.firstStart().Before(latestReadEnd) {
		t.Fatalf("write tool started before read-only batch completed: write=%s latestReadEnd=%s", write.firstStart(), latestReadEnd)
	}
}
