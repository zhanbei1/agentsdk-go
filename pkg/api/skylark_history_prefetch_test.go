package api

import (
	"strings"
	"testing"

	"github.com/stellarlinkco/agentsdk-go/pkg/message"
)

func TestBuildSkylarkHistoryPrefetchInjection_FollowupTriggers(t *testing.T) {
	h := []message.Message{
		{Role: "user", Content: "我们要怎么做多级记忆？"},
		{Role: "assistant", Content: "可以分 L0-L3：瞬时、会话、项目持久、检索索引。"},
		{Role: "user", Content: "那 MCP 工具怎么优化？"},
		{Role: "assistant", Content: "做工具描述压缩 + refresh 防抖即可。"},
	}

	o := &SkylarkOptions{
		HistoryPrefetchMaxHits:        3,
		HistoryPrefetchMaxRunes:       400,
		HistoryPrefetchHints:          []string{"上次"},
		ProgressiveMiniMemoryMaxRunes: 200,
	}

	inj := buildSkylarkHistoryPrefetchInjection("上次你说的多级记忆再展开下", h, o)
	if strings.TrimSpace(inj) == "" {
		t.Fatalf("expected injection for follow-up prompt")
	}
	if !strings.Contains(inj, "Related previous conversation") {
		t.Fatalf("expected header, got=%q", inj)
	}
}

func TestBuildSkylarkHistoryPrefetchInjection_NonFollowupSkips(t *testing.T) {
	h := []message.Message{
		{Role: "user", Content: "A"},
		{Role: "assistant", Content: "B"},
	}
	o := &SkylarkOptions{
		HistoryPrefetchMaxHits:  3,
		HistoryPrefetchMaxRunes: 400,
		HistoryPrefetchHints:    []string{"上次"},
	}

	inj := buildSkylarkHistoryPrefetchInjection("请给一个新的实现方案", h, o)
	if strings.TrimSpace(inj) != "" {
		t.Fatalf("expected no injection, got=%q", inj)
	}
}
