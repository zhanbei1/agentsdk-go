package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellarlinkco/agentsdk-go/pkg/message"
)

func TestMaybePersistProjectMemory_WritesJSONLWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	opts := Options{
		ProjectRoot: dir,
		Skylark: &SkylarkOptions{
			Enabled:              true,
			PersistProjectMemory: true,
			ProjectMemoryDir:     filepath.Join(dir, ".agents", "memory"),
		},
	}

	h := []message.Message{
		{Role: "user", Content: "问题"},
		{Role: "assistant", Content: "结论：可以分层记忆并做 prefetch。"},
	}

	if err := maybePersistProjectMemory(context.Background(), opts, "s1", "r1", h, "run_success", nil); err != nil {
		t.Fatalf("persist: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(opts.Skylark.ProjectMemoryDir, "project_memory.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		t.Fatalf("expected jsonl content")
	}
}
