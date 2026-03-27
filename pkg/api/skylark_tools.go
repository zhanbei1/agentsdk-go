package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/stellarlinkco/agentsdk-go/pkg/message"
	"github.com/stellarlinkco/agentsdk-go/pkg/skylark"
	"github.com/stellarlinkco/agentsdk-go/pkg/tool"
)

const (
	retrieveKnowledgeToolName    = "retrieve_knowledge"
	retrieveCapabilitiesToolName = "retrieve_capabilities"
)

type retrieveKnowledgeParams struct {
	Query string   `json:"query"`
	Kinds []string `json:"kinds"`
	Limit int      `json:"limit"`
}

type retrieveCapabilitiesParams struct {
	Query       string   `json:"query"`
	Kinds       []string `json:"kinds"`
	Limit       int      `json:"limit"`
	Unlock      bool     `json:"unlock"`
	UnlockTopN  int      `json:"unlock_top_n"`
	UnlockNames []string `json:"unlock_names"`
}

type retrieveKnowledgeTool struct {
	engine *skylark.Engine
}

func (t *retrieveKnowledgeTool) Name() string { return retrieveKnowledgeToolName }

func (t *retrieveKnowledgeTool) Description() string {
	return `Search project knowledge: memory (AGENTS.md), context, documents, and optional conversation history. Uses hybrid Bleve text search plus optional vector similarity when embeddings are configured.`
}

func (t *retrieveKnowledgeTool) Schema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Natural language query to search for.",
			},
			"kinds": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
					"enum": []string{skylark.KindContext, skylark.KindDocument, skylark.KindMemory, skylark.KindHistory},
				},
				"description": "Optional subset: context, document, memory, history. Omit to search all indexed knowledge kinds plus history.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Max hits (default from SkylarkOptions, typically 3).",
			},
		},
		Required: []string{"query"},
	}
}

func (t *retrieveKnowledgeTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	if t == nil || t.engine == nil {
		return nil, errors.New("skylark: engine not initialised")
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	var p retrieveKnowledgeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	q := strings.TrimSpace(p.Query)
	if q == "" {
		return nil, errors.New("query is required")
	}
	limit := p.Limit
	if limit <= 0 {
		if b := skylarkRunFromContext(ctx); b != nil && b.DefaultKnowledgeLimit > 0 {
			limit = b.DefaultKnowledgeLimit
		} else {
			limit = 8
		}
	}

	explicitKinds := len(p.Kinds) > 0
	var indexKinds map[string]struct{}
	wantHistory := false
	if !explicitKinds {
		indexKinds = nil
		wantHistory = true
	} else {
		indexKinds = map[string]struct{}{}
		for _, raw := range p.Kinds {
			k := strings.ToLower(strings.TrimSpace(raw))
			if k == "" {
				continue
			}
			if k == skylark.KindHistory {
				wantHistory = true
				continue
			}
			indexKinds[k] = struct{}{}
		}
	}

	var hits []skylark.Hit
	if !explicitKinds {
		idxHits, err := t.engine.SearchIndex(ctx, q, nil, limit)
		if err != nil {
			return nil, err
		}
		hits = append(hits, idxHits...)
	} else if len(indexKinds) > 0 {
		idxHits, err := t.engine.SearchIndex(ctx, q, indexKinds, limit)
		if err != nil {
			return nil, err
		}
		hits = append(hits, idxHits...)
	}
	if wantHistory {
		b := skylarkRunFromContext(ctx)
		if b != nil && b.History != nil {
			turns := historyToTurns(b.History)
			hh := skylark.SearchHistory(q, turns, limit)
			hits = append(hits, hh...)
		}
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}

	out, err := json.MarshalIndent(map[string]any{"hits": hits}, "", "  ")
	if err != nil {
		return nil, err
	}
	return &tool.ToolResult{Success: true, Output: string(out)}, nil
}

type retrieveCapabilitiesTool struct {
	engine *skylark.Engine
}

func (t *retrieveCapabilitiesTool) Name() string { return retrieveCapabilitiesToolName }

func (t *retrieveCapabilitiesTool) Description() string {
	return `Search available skills, built-in tools, and MCP tools. Optionally unlock tool names for subsequent turns (progressive exposure).`
}

func (t *retrieveCapabilitiesTool) Schema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": `Natural language query (e.g. read file, grep, pdf skill).`,
			},
			"kinds": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
					"enum": []string{skylark.KindSkill, skylark.KindTool, skylark.KindMCP},
				},
				"description": "Optional subset: skill, tool, mcp.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Max hits (default from SkylarkOptions, typically 2).",
			},
			"unlock": map[string]any{
				"type":        "boolean",
				"description": "If true, expose tools/skills for execution based on unlock_top_n / unlock_names.",
			},
			"unlock_top_n": map[string]any{
				"type":        "integer",
				"description": "When unlock=true, unlock up to N tool/MCP hits (default from SkylarkOptions, typically 2). Also unlocks the skill tool once if any skill is in the results.",
			},
			"unlock_names": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "Explicit tool names to unlock (e.g. bash, read, skill).",
			},
		},
		Required: []string{"query"},
	}
}

func (t *retrieveCapabilitiesTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	if t == nil || t.engine == nil {
		return nil, errors.New("skylark: engine not initialised")
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	var p retrieveCapabilitiesParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	q := strings.TrimSpace(p.Query)
	if q == "" {
		return nil, errors.New("query is required")
	}
	limit := p.Limit
	if limit <= 0 {
		if b := skylarkRunFromContext(ctx); b != nil && b.DefaultCapabilitiesLimit > 0 {
			limit = b.DefaultCapabilitiesLimit
		} else {
			limit = 8
		}
	}

	kmap := map[string]struct{}{}
	for _, k := range p.Kinds {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" {
			continue
		}
		kmap[k] = struct{}{}
	}
	if len(kmap) == 0 {
		kmap[skylark.KindSkill] = struct{}{}
		kmap[skylark.KindTool] = struct{}{}
		kmap[skylark.KindMCP] = struct{}{}
	}

	hits, err := t.engine.SearchIndex(ctx, q, kmap, limit)
	if err != nil {
		return nil, err
	}

	b := skylarkRunFromContext(ctx)
	if b != nil && b.Allow != nil {
		if len(p.UnlockNames) > 0 {
			b.Allow.unlock(p.UnlockNames)
		}
		if p.Unlock {
			topN := p.UnlockTopN
			if topN <= 0 {
				if bundle := skylarkRunFromContext(ctx); bundle != nil && bundle.DefaultUnlockTopN > 0 {
					topN = bundle.DefaultUnlockTopN
				} else {
					topN = 3
				}
			}
			skillUnlocked := false
			for _, h := range hits {
				if h.Kind == skylark.KindSkill && !skillUnlocked {
					b.Allow.unlock([]string{"skill"})
					skillUnlocked = true
				}
				if topN <= 0 {
					continue
				}
				if h.Kind != skylark.KindTool && h.Kind != skylark.KindMCP {
					continue
				}
				name := strings.TrimSpace(h.Title)
				if name == "" {
					continue
				}
				b.Allow.unlock([]string{name})
				topN--
			}
		}
	}

	payload := map[string]any{"hits": hits}
	if b != nil && b.Allow != nil {
		payload["unlocked_preview"] = keysOfAllowed(b.Allow)
	}
	out, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, err
	}
	return &tool.ToolResult{Success: true, Output: string(out)}, nil
}

func keysOfAllowed(s *skylarkAllowState) []string {
	if s == nil {
		return nil
	}
	m := s.allowedMap()
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func historyToTurns(h *message.History) []skylark.HistoryTurn {
	if h == nil {
		return nil
	}
	msgs := h.All()
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
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				fmt.Fprintf(&b, "tool %s → %s", tc.Name, strings.TrimSpace(tc.Result))
			}
		}
		if strings.TrimSpace(m.ReasoningContent) != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(m.ReasoningContent)
		}
		out = append(out, skylark.HistoryTurn{Role: m.Role, Text: b.String()})
	}
	return out
}
