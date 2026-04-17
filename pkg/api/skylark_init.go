package api

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/stellarlinkco/agentsdk-go/pkg/config"
	"github.com/stellarlinkco/agentsdk-go/pkg/skylark"
	"github.com/stellarlinkco/agentsdk-go/pkg/tool"
	"github.com/tmc/langchaingo/embeddings"
)

// SkylarkOptions configures progressive retrieval (Skylark mode): the model only
// sees retrieve_knowledge / retrieve_capabilities until it unlocks more tools.
type SkylarkOptions struct {
	Enabled bool
	// DataDir stores Bleve index, corpus.json, vectors.json. Default: <ProjectRoot>/.agents/skylark
	DataDir string
	// DisableEmbedding forces Bleve-only search (no vector API calls).
	DisableEmbedding bool
	// Embedder optional; when nil and DisableEmbedding is false, uses env-based OpenAI-compatible embeddings.
	Embedder embeddings.Embedder `json:"-"`
	// KeepAutoSkills preserves legacy automatic skill activation when true (default false).
	KeepAutoSkills bool

	// EnableOneShotRouting skips progressive tool lock for short prompts (see SimplePromptMaxRunes).
	// nil 表示开启（默认与 Skylark 同开）；显式 *false 可关闭。
	EnableOneShotRouting *bool
	// SimplePromptMaxRunes upper bound for one-shot routing (withDefaults 中 ≤0 时为 10)。
	SimplePromptMaxRunes int
	// ComplexityHints: if the prompt contains any hint (case-insensitive), force progressive Skylark.
	ComplexityHints []string

	// DefaultKnowledgeLimit default top-k for retrieve_knowledge when limit is omitted (default 3).
	DefaultKnowledgeLimit int
	// DefaultCapabilitiesLimit for retrieve_capabilities when limit omitted (default 2).
	DefaultCapabilitiesLimit int
	// DefaultUnlockTopN when unlock=true and unlock_top_n omitted (default 2).
	DefaultUnlockTopN int

	// ProgressiveMiniMemoryMaxRunes controls the max size of always-on "mini memory"
	// appended to the system prompt in progressive Skylark mode. This mitigates
	// "forgot last answer" failure modes when the model doesn't call retrieve_knowledge.
	// Default: 400.
	ProgressiveMiniMemoryMaxRunes int

	// HistoryPrefetchMaxHits controls how many conversation hits to inject when
	// a "follow-up" prompt is detected in progressive Skylark mode. Default: 4.
	HistoryPrefetchMaxHits int

	// HistoryPrefetchMaxRunes caps the injected prefetch snippet size. Default: 900.
	HistoryPrefetchMaxRunes int

	// HistoryPrefetchHints triggers prefetch when prompt contains any hint.
	// If empty, sensible bilingual defaults are used.
	HistoryPrefetchHints []string

	// PersistProjectMemory enables writing short session conclusions into
	// <ProjectRoot>/.agents/memory/*.jsonl for cross-session recall. Default: false.
	PersistProjectMemory bool
	// ProjectMemoryDir overrides the directory used to store memory JSONL files.
	// Default: <ProjectRoot>/.agents/memory
	ProjectMemoryDir string
}

func buildSkylarkEngine(ctx context.Context, opts Options, settings *config.Settings, memory, rules string, registry *tool.Registry) (*skylark.Engine, error) {
	if opts.Skylark == nil || !opts.Skylark.Enabled {
		return nil, nil
	}
	dataDir := strings.TrimSpace(opts.Skylark.DataDir)
	if dataDir == "" {
		dataDir = filepath.Join(opts.ProjectRoot, ".agents", "skylark")
	}
	var emb embeddings.Embedder
	if opts.Skylark.Embedder != nil {
		emb = opts.Skylark.Embedder
	} else if !opts.Skylark.DisableEmbedding {
		var err error
		emb, err = skylark.NewEmbedderFromEnv()
		if err != nil {
			return nil, fmt.Errorf("skylark: embedder: %w", err)
		}
		// No API key: embedder is nil → Bleve-only indexing (see docs/skylark.md).
	}
	eng, err := skylark.NewEngine(dataDir, emb)
	if err != nil {
		return nil, err
	}
	memDocs, err := loadProjectMemoryDocuments(strings.TrimSpace(opts.Skylark.ProjectMemoryDir), 200)
	if err != nil {
		_ = eng.Close()
		return nil, fmt.Errorf("skylark: load project memory: %w", err)
	}
	docs := buildSkylarkDocuments(memory, rules, opts.skReg, registry.List(), memDocs)
	if err := eng.Rebuild(ctx, docs); err != nil {
		_ = eng.Close()
		return nil, err
	}
	if err := registerSkylarkRetrievalTools(registry, eng, opts, settings); err != nil {
		_ = eng.Close()
		return nil, err
	}
	return eng, nil
}

func registerSkylarkRetrievalTools(registry *tool.Registry, engine *skylark.Engine, opts Options, settings *config.Settings) error {
	if registry == nil || engine == nil {
		return fmt.Errorf("api: registerSkylarkRetrievalTools: nil argument")
	}
	dis := effectiveDisallowedToolSet(opts, settings)
	tools := []tool.Tool{
		&retrieveKnowledgeTool{engine: engine},
		&retrieveCapabilitiesTool{engine: engine},
	}
	for _, t := range tools {
		if t == nil {
			continue
		}
		name := canonicalToolName(t.Name())
		if name == "" {
			continue
		}
		if dis != nil {
			if _, blocked := dis[name]; blocked {
				continue
			}
		}
		if err := registry.Register(t); err != nil {
			return fmt.Errorf("api: register tool %s: %w", t.Name(), err)
		}
	}
	return nil
}

func skylarkSystemPromptAppend() string {
	return `## Skylark (progressive retrieval)

- Use retrieve_knowledge to pull memory, rules, documents, and optional conversation history on demand.
- Use retrieve_capabilities to discover skills, tools, and MCP integrations. Set unlock=true (and optionally unlock_top_n) to expose tools for later turns.
- Do not assume tools are visible until you retrieve and unlock them.`
}

// effectiveDisallowedToolSet merges Options.DisallowedTools with settings.json disallowedTools.
func effectiveDisallowedToolSet(opts Options, settings *config.Settings) map[string]struct{} {
	dis := toLowerSet(opts.DisallowedTools)
	if settings != nil && len(settings.DisallowedTools) > 0 {
		if dis == nil {
			dis = map[string]struct{}{}
		}
		for _, name := range settings.DisallowedTools {
			if key := canonicalToolName(name); key != "" {
				dis[key] = struct{}{}
			}
		}
	}
	return dis
}
