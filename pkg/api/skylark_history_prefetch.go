package api

import (
	"strings"

	"github.com/stellarlinkco/agentsdk-go/pkg/message"
	"github.com/stellarlinkco/agentsdk-go/pkg/skylark"
)

func buildSkylarkHistoryPrefetchInjection(prompt string, historyBefore []message.Message, o *SkylarkOptions) string {
	if o == nil {
		return ""
	}
	p := strings.TrimSpace(prompt)
	if p == "" || len(historyBefore) == 0 {
		return ""
	}
	if !shouldSkylarkPrefetchHistory(p, o) {
		return ""
	}

	hits := skylark.SearchHistory(p, historyToTurnsForPrefetch(historyBefore), o.HistoryPrefetchMaxHits)
	if len(hits) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Related previous conversation (auto)\n\n")
	for _, h := range hits {
		sn := strings.TrimSpace(h.Snippet)
		if sn == "" {
			sn = strings.TrimSpace(h.Text)
		}
		if sn == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(cropRunes(sn, 260))
		b.WriteByte('\n')
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return ""
	}
	return cropRunes(out, o.HistoryPrefetchMaxRunes)
}

func shouldSkylarkPrefetchHistory(prompt string, o *SkylarkOptions) bool {
	if o == nil {
		return false
	}
	p := strings.TrimSpace(prompt)
	if p == "" {
		return false
	}
	hints := o.HistoryPrefetchHints
	if len(hints) == 0 {
		// defaults should be set in Options.withDefaults; keep a safe fallback.
		hints = []string{"上次", "之前", "刚才", "继续", "再问", "previous", "again", "last time"}
	}

	low := strings.ToLower(p)
	for _, raw := range hints {
		h := strings.TrimSpace(raw)
		if h == "" {
			continue
		}
		// Mixed-language matching: ASCII-ish hints match lowercased;
		// non-ASCII hints match raw prompt too.
		if strings.Contains(low, strings.ToLower(h)) || strings.Contains(p, h) {
			return true
		}
	}
	return false
}

func historyToTurnsForPrefetch(msgs []message.Message) []skylark.HistoryTurn {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]skylark.HistoryTurn, 0, len(msgs))
	for _, m := range msgs {
		var b strings.Builder
		if strings.TrimSpace(m.Content) != "" {
			b.WriteString(strings.TrimSpace(m.Content))
		}
		for _, tb := range m.ContentBlocks {
			if strings.TrimSpace(tb.Text) != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(tb.Text)
			}
		}
		// Intentionally exclude tool outputs and reasoning blocks to reduce noise.
		text := strings.TrimSpace(b.String())
		if text == "" {
			continue
		}
		out = append(out, skylark.HistoryTurn{Role: m.Role, Text: text})
	}
	return out
}
