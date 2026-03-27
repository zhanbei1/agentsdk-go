package api

import (
	"context"

	"github.com/stellarlinkco/agentsdk-go/pkg/message"
)

type skylarkRunContextKey struct{}

// skylarkRunBundle is injected into the agent context so retrieval tools can read session state.
type skylarkRunBundle struct {
	Allow   *skylarkAllowState
	History *message.History

	// Default top-k when the model omits limit / unlock_top_n (from SkylarkOptions).
	DefaultKnowledgeLimit    int
	DefaultCapabilitiesLimit int
	DefaultUnlockTopN        int
}

func withSkylarkRun(ctx context.Context, b *skylarkRunBundle) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, skylarkRunContextKey{}, b)
}

func skylarkRunFromContext(ctx context.Context) *skylarkRunBundle {
	if ctx == nil {
		return nil
	}
	b, _ := ctx.Value(skylarkRunContextKey{}).(*skylarkRunBundle)
	return b
}
