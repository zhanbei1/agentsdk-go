package api

import (
	"strings"

	"github.com/stellarlinkco/agentsdk-go/pkg/message"
)

func augmentSkylarkProgressiveSystemPrompt(base string, historyBefore []message.Message, o *SkylarkOptions) string {
	out := strings.TrimSpace(base)
	if o == nil {
		return out
	}
	limit := o.ProgressiveMiniMemoryMaxRunes
	if limit <= 0 {
		limit = 400
	}

	mini := buildSkylarkProgressiveMiniMemory(historyBefore, limit)
	if strings.TrimSpace(mini) == "" {
		return out
	}
	return strings.TrimSpace(out) + "\n\n" + mini
}

func buildSkylarkProgressiveMiniMemory(historyBefore []message.Message, maxRunes int) string {
	if maxRunes <= 0 || len(historyBefore) == 0 {
		return ""
	}

	// Pick a handful of recent assistant outputs as "sticky context".
	// This is deliberately simple and bounded; deeper details should be fetched via retrieve_knowledge.
	const maxItems = 3
	items := make([]string, 0, maxItems)

	for i := len(historyBefore) - 1; i >= 0 && len(items) < maxItems; i-- {
		m := historyBefore[i]
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role != "assistant" {
			continue
		}
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		items = append(items, text)
	}

	if len(items) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Recent context (auto)\n\n")
	b.WriteString("- 下面是近期对话的简短摘录（用于避免复问时遗忘）；更完整细节请用 `retrieve_knowledge`。\n")
	for _, it := range items {
		b.WriteString("- ")
		b.WriteString(cropRunes(it, 220))
		b.WriteByte('\n')
	}

	return cropRunes(strings.TrimSpace(b.String()), maxRunes)
}

func cropRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" || max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}
