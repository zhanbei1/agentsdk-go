package api

import (
	"strings"
)

// effectiveOneShotRouting: nil pointer means on (default); *false disables; *true enables.
func effectiveOneShotRouting(o *SkylarkOptions) bool {
	if o == nil {
		return false
	}
	if o.EnableOneShotRouting == nil {
		return true
	}
	return *o.EnableOneShotRouting
}

// isSkylarkSimplePrompt reports whether the user prompt should skip progressive
// Skylark (full tool exposure + optional memory/rules re-injection for one-shot).
func isSkylarkSimplePrompt(prompt string, o *SkylarkOptions) bool {
	if o == nil || !effectiveOneShotRouting(o) {
		return false
	}
	p := strings.TrimSpace(prompt)
	if p == "" {
		return false
	}
	maxR := o.SimplePromptMaxRunes
	if maxR <= 0 {
		maxR = 10
	}
	if len([]rune(p)) > maxR {
		return false
	}
	if strings.Count(p, "\n") > 1 {
		return false
	}
	low := strings.ToLower(p)
	for _, hint := range o.ComplexityHints {
		h := strings.TrimSpace(hint)
		if h == "" {
			continue
		}
		if strings.Contains(low, strings.ToLower(h)) {
			return false
		}
	}
	return true
}

func augmentSkylarkOneShotSystemPrompt(base, agentsMD, rulesMD string) string {
	out := strings.TrimSpace(base)
	if agentsMD != "" {
		out = out + "\n\n## Memory\n\n" + strings.TrimSpace(agentsMD)
	}
	if rulesMD != "" {
		out = out + "\n\n## Project Rules\n\n" + strings.TrimSpace(rulesMD)
	}
	return strings.TrimSpace(out) + "\n\n" + skylarkOneShotRouteHint()
}

func skylarkOneShotRouteHint() string {
	return "One-shot route (brief request): answer from context or call tools directly. retrieve_knowledge / retrieve_capabilities are optional."
}

func applySkylarkBundleDefaults(b *skylarkRunBundle, o *SkylarkOptions) {
	if b == nil || o == nil {
		return
	}
	kl := o.DefaultKnowledgeLimit
	if kl <= 0 {
		kl = 3
	}
	b.DefaultKnowledgeLimit = kl
	cl := o.DefaultCapabilitiesLimit
	if cl <= 0 {
		cl = 2
	}
	b.DefaultCapabilitiesLimit = cl
	un := o.DefaultUnlockTopN
	if un <= 0 {
		un = 2
	}
	b.DefaultUnlockTopN = un
}
