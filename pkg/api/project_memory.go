package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stellarlinkco/agentsdk-go/pkg/message"
	"github.com/stellarlinkco/agentsdk-go/pkg/skylark"
)

type projectMemoryEntry struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Title      string            `json:"title"`
	Content    string            `json:"content"`
	Evidence   []string          `json:"evidence,omitempty"`
	Confidence float64           `json:"confidence"`
	CreatedAt  time.Time         `json:"created_at"`
	LastSeenAt time.Time         `json:"last_seen_at"`
	Source     map[string]string `json:"source,omitempty"`
}

func maybePersistProjectMemory(ctx context.Context, opts Options, sessionID, requestID string, historyAfter []message.Message, reason string, runErr error) error {
	if opts.Skylark == nil || !opts.Skylark.Enabled || !opts.Skylark.PersistProjectMemory {
		return nil
	}
	if runErr != nil {
		return nil
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}

	content, title := extractSessionConclusion(historyAfter)
	if strings.TrimSpace(content) == "" {
		return nil
	}

	now := time.Now()
	sum := sha256.Sum256([]byte(sessionID + "\n" + title + "\n" + content))
	id := hex.EncodeToString(sum[:8])

	entry := projectMemoryEntry{
		ID:         id,
		Kind:       "session_conclusion",
		Title:      title,
		Content:    cropRunes(content, 1800),
		Confidence: 0.6,
		CreatedAt:  now,
		LastSeenAt: now,
		Source: map[string]string{
			"session_id": sessionID,
			"request_id": requestID,
			"reason":     strings.TrimSpace(reason),
		},
	}

	dir := strings.TrimSpace(opts.Skylark.ProjectMemoryDir)
	if dir == "" {
		dir = filepath.Join(opts.ProjectRoot, ".agents", "memory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "project_memory.jsonl")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	_ = ctx
	return nil
}

func extractSessionConclusion(history []message.Message) (content string, title string) {
	// Pick the last assistant message as the "conclusion" seed.
	for i := len(history) - 1; i >= 0; i-- {
		m := history[i]
		if strings.ToLower(strings.TrimSpace(m.Role)) != "assistant" {
			continue
		}
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		title = "Recent conclusion"
		if len([]rune(text)) > 40 {
			title = cropRunes(text, 40)
		} else {
			title = text
		}
		return text, title
	}
	return "", ""
}

func loadProjectMemoryDocuments(dir string, maxDocs int) ([]skylark.Document, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	path := filepath.Join(dir, "project_memory.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	if maxDocs <= 0 {
		maxDocs = 200
	}

	var docs []skylark.Document
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e projectMemoryEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if strings.TrimSpace(e.Content) == "" {
			continue
		}
		id := strings.TrimSpace(e.ID)
		if id == "" {
			sum := sha256.Sum256([]byte(e.Title + "\n" + e.Content))
			id = hex.EncodeToString(sum[:8])
		}
		text := strings.TrimSpace(e.Title + "\n" + e.Content)
		docs = append(docs, skylark.Document{
			ID:    fmt.Sprintf("memory:project:%s", id),
			Kind:  skylark.KindMemory,
			Title: strings.TrimSpace(e.Title),
			Text:  cropRunes(text, 2200),
			Meta: map[string]string{
				"memory_kind": strings.TrimSpace(e.Kind),
			},
		})
		if len(docs) >= maxDocs {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return docs, err
	}
	return docs, nil
}
