package skylark

import (
	"os"
	"strings"

	"github.com/tmc/langchaingo/embeddings"
)

// NewEmbedderFromEnv builds an OpenAI-compatible embeddings client when
// SKYLARK_EMBEDDING_API_KEY or OPENAI_API_KEY is set. Returns nil if disabled.
func NewEmbedderFromEnv() (embeddings.Embedder, error) {
	key := strings.TrimSpace(os.Getenv("SKYLARK_EMBEDDING_API_KEY"))
	if key == "" {
		key = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	if key == "" {
		return nil, nil
	}
	base := strings.TrimSpace(os.Getenv("SKYLARK_EMBEDDING_BASE_URL"))
	if base == "" {
		base = strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	}
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	model := strings.TrimSpace(os.Getenv("SKYLARK_EMBEDDING_MODEL"))
	if model == "" {
		model = "text-embedding-3-small"
	}
	cli := newOpenAICompatEmbedClient(base, key, model)
	return embeddings.NewEmbedder(embeddings.EmbedderClientFunc(cli.CreateEmbedding), nil)
}
