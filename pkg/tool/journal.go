package tool

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const defaultToolJournalBaseDir = "/tmp/agentsdk/journal"

type journalEntry struct {
	Version   int       `json:"v"`
	CreatedAt time.Time `json:"created_at"`

	SessionID string `json:"session_id"`
	ToolName  string `json:"tool_name"`

	TargetPath string `json:"target_path"`
	Existed    bool   `json:"existed"`
	FileMode   uint32 `json:"file_mode,omitempty"`

	BeforePath string `json:"before_path,omitempty"`
}

func (e *Executor) maybeJournal(call Call) error {
	if e == nil {
		return nil
	}
	name := strings.ToLower(strings.TrimSpace(call.Name))
	if name != "write" && name != "edit" {
		return nil
	}
	session := strings.TrimSpace(call.SessionID)
	if session == "" {
		session = "default"
	}
	target, ok := extractTargetFilePath(call.Params)
	if !ok {
		return nil
	}
	target = filepath.Clean(target)
	if target == "" {
		return nil
	}

	// Only journal absolute paths to avoid ambiguity.
	if !filepath.IsAbs(target) {
		// If Path (sandbox root) is provided, resolve relative target under it.
		root := strings.TrimSpace(call.Path)
		if root == "" {
			return nil
		}
		target = filepath.Clean(filepath.Join(root, target))
	}

	info, err := os.Stat(target)
	existed := err == nil
	mode := uint32(0)
	if existed {
		mode = uint32(info.Mode())
	}

	base := defaultToolJournalBaseDir
	dir := filepath.Join(base, sanitizePathComponent(session), "fs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	entry := journalEntry{
		Version:    1,
		CreatedAt:  time.Now().UTC(),
		SessionID:  session,
		ToolName:   name,
		TargetPath: target,
		Existed:    existed,
		FileMode:   mode,
	}

	if existed && !info.IsDir() {
		beforeDir := filepath.Join(dir, "before")
		if err := os.MkdirAll(beforeDir, 0o700); err != nil {
			return err
		}
		beforeName := fmt.Sprintf("%d.before", time.Now().UnixNano())
		beforePath := filepath.Join(beforeDir, beforeName)
		data, readErr := os.ReadFile(target)
		if readErr == nil {
			if writeErr := os.WriteFile(beforePath, data, 0o600); writeErr == nil {
				entry.BeforePath = beforePath
			}
		}
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	logPath := filepath.Join(dir, "journal.jsonl")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func extractTargetFilePath(params map[string]any) (string, bool) {
	if params == nil {
		return "", false
	}
	// write/edit share "file_path"
	if raw, ok := params["file_path"]; ok && raw != nil {
		if s, ok := raw.(string); ok {
			s = strings.TrimSpace(s)
			if s != "" {
				return s, true
			}
		}
	}
	return "", false
}

func readLatestJournalEntry(sessionID string) (*journalEntry, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "default"
	}
	dir := filepath.Join(defaultToolJournalBaseDir, sanitizePathComponent(sessionID), "fs")
	logPath := filepath.Join(dir, "journal.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		return nil, errors.New("journal is empty")
	}
	// Parse from the end until we find a valid entry.
	for i := len(lines) - 1; i >= 0; i-- {
		var ent journalEntry
		if json.Unmarshal([]byte(lines[i]), &ent) == nil && strings.TrimSpace(ent.TargetPath) != "" {
			return &ent, nil
		}
	}
	return nil, errors.New("no valid journal entry found")
}

// ReadLatestJournalEntry is used by rollback tools to restore the most recent
// write/edit step. It is intentionally best-effort and only exposes the
// minimal data needed to restore files.
func ReadLatestJournalEntry(sessionID string) (*journalEntry, error) {
	return readLatestJournalEntry(sessionID)
}

func listJournalEntries(sessionID string, limit int) ([]journalEntry, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "default"
	}
	dir := filepath.Join(defaultToolJournalBaseDir, sanitizePathComponent(sessionID), "fs")
	logPath := filepath.Join(dir, "journal.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil, err
	}
	rawLines := strings.Split(strings.TrimSpace(string(data)), "\n")
	entries := make([]journalEntry, 0, len(rawLines))
	for _, line := range rawLines {
		var ent journalEntry
		if json.Unmarshal([]byte(line), &ent) == nil && strings.TrimSpace(ent.TargetPath) != "" {
			entries = append(entries, ent)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].CreatedAt.Before(entries[j].CreatedAt) })
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}
