package middleware

import (
	"context"
	"errors"
	"testing"
)

type fakeToolCall struct {
	ID   string
	Name string
}

type fakeToolResult struct {
	Err error
}

func TestReflectionMiddleware_AfterToolAppendsRecord(t *testing.T) {
	mw := NewReflectionMiddleware()
	st := &State{
		Iteration: 2,
		ToolCall:  fakeToolCall{ID: "c1", Name: "demo"},
		ToolResult: fakeToolResult{
			Err: errors.New("boom"),
		},
		Values: map[string]any{"session_id": "s1", "request_id": "r1"},
	}

	if err := mw.AfterTool(context.Background(), st); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	raw := st.Values[reflectionKey]
	if raw == nil {
		t.Fatalf("expected reflection records")
	}
	recs, ok := raw.([]ReflectionRecord)
	if !ok || len(recs) != 1 {
		t.Fatalf("unexpected records: %#v", raw)
	}
	if recs[0].ToolName != "demo" || recs[0].ToolUseID != "c1" {
		t.Fatalf("unexpected tool fields: %+v", recs[0])
	}
}
