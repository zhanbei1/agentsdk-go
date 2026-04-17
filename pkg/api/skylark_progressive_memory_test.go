package api

import (
	"testing"

	"github.com/stellarlinkco/agentsdk-go/pkg/message"
)

func TestBuildSkylarkProgressiveMiniMemory_Bounded(t *testing.T) {
	h := []message.Message{
		{Role: "user", Content: "问题 A"},
		{Role: "assistant", Content: "结论 A：应该这样做。细节略。"},
		{Role: "assistant", Content: "结论 B：还需要注意另一点。"},
		{Role: "assistant", Content: "结论 C：最后再补充一点。"},
	}

	got := buildSkylarkProgressiveMiniMemory(h, 120)
	if got == "" {
		t.Fatalf("expected non-empty mini memory")
	}
	if len([]rune(got)) > 120 {
		t.Fatalf("expected bounded output, got=%d runes", len([]rune(got)))
	}
}
