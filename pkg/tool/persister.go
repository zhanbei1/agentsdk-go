package tool

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultToolOutputThresholdBytes = 64 * 1024
const defaultToolOutputSnippetMaxRunes = 900

// OutputPersister stores large ToolResult.Output payloads on disk and replaces
// the inline output with a reference string plus OutputRef metadata.
//
// The persisted file layout is:
//
//	/tmp/agentsdk/tool-output/{session_id}/{tool_name}/{timestamp}.output
//
// Callers may override BaseDir and thresholds for tests or custom deployments.
type OutputPersister struct {
	BaseDir               string
	DefaultThresholdBytes int
	PerToolThresholdBytes map[string]int

	// DefaultThresholdRunes, when > 0, takes precedence over byte thresholds and
	// treats ToolResult.Output length as runes (Unicode code points).
	DefaultThresholdRunes int
	PerToolThresholdRunes map[string]int

	// SnippetMaxRunes controls the size of the summary snippet embedded alongside
	// the output reference. Default: 900.
	SnippetMaxRunes int
}

func NewOutputPersister() *OutputPersister {
	return &OutputPersister{
		BaseDir:               toolOutputBaseDir(),
		DefaultThresholdBytes: defaultToolOutputThresholdBytes,
		SnippetMaxRunes:       defaultToolOutputSnippetMaxRunes,
	}
}

func (p *OutputPersister) MaybePersist(call Call, result *ToolResult) error {
	if p == nil || result == nil {
		return nil
	}
	if result.OutputRef != nil {
		return nil
	}
	output := result.Output
	if output == "" {
		return nil
	}

	runeThreshold := p.runeThresholdFor(call.Name)
	byteThreshold := p.thresholdFor(call.Name)
	if runeThreshold <= 0 && byteThreshold <= 0 {
		return nil
	}
	if runeThreshold > 0 {
		if len([]rune(output)) <= runeThreshold {
			return nil
		}
	} else if len(output) <= byteThreshold {
		return nil
	}

	base := strings.TrimSpace(p.BaseDir)
	if base == "" {
		return errors.New("tool output base directory is empty")
	}

	sessionDir := sanitizePathComponent(call.SessionID)
	toolDir := sanitizePathComponent(call.Name)
	dir := filepath.Join(base, sessionDir, toolDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	f, path, err := createToolOutputFile(dir)
	if err != nil {
		return err
	}
	if err := finalizeToolOutputFile(f, path, output); err != nil {
		return err
	}

	snippet := summarizeToolOutput(output, p.snippetMaxRunes())
	if strings.TrimSpace(snippet) == "" {
		result.Output = formatToolOutputReference(path)
	} else {
		result.Output = formatToolOutputReference(path) + "\n\n" + snippet
	}
	result.OutputRef = &OutputRef{
		Path:      path,
		SizeBytes: int64(len(output)),
		Truncated: false,
	}
	return nil
}

func (p *OutputPersister) runeThresholdFor(toolName string) int {
	if p == nil {
		return 0
	}
	canon := strings.ToLower(strings.TrimSpace(toolName))
	if canon != "" && len(p.PerToolThresholdRunes) > 0 {
		if threshold, ok := p.PerToolThresholdRunes[canon]; ok && threshold > 0 {
			return threshold
		}
	}
	if p.DefaultThresholdRunes > 0 {
		return p.DefaultThresholdRunes
	}
	return 0
}

func (p *OutputPersister) thresholdFor(toolName string) int {
	if p == nil {
		return 0
	}
	canon := strings.ToLower(strings.TrimSpace(toolName))
	if canon != "" && len(p.PerToolThresholdBytes) > 0 {
		if threshold, ok := p.PerToolThresholdBytes[canon]; ok && threshold > 0 {
			return threshold
		}
	}
	if p.DefaultThresholdBytes > 0 {
		return p.DefaultThresholdBytes
	}
	return defaultToolOutputThresholdBytes
}

func createToolOutputFile(dir string) (*os.File, string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, "", errors.New("output directory is empty")
	}

	return createToolOutputFileWithTimestamp(dir, time.Now().UnixNano())
}

func createToolOutputFileWithTimestamp(dir string, ts int64) (*os.File, string, error) {
	for attempts := 0; attempts < 16; attempts++ {
		filename := strconv.FormatInt(ts, 10) + ".output"
		path := filepath.Join(dir, filename)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				ts++
				continue
			}
			return nil, "", err
		}
		return f, path, nil
	}
	return nil, "", fmt.Errorf("output file collision under %s", dir)
}

type toolOutputFile interface {
	WriteString(string) (int, error)
	Close() error
}

func finalizeToolOutputFile(f toolOutputFile, path string, output string) error {
	_, writeErr := f.WriteString(output)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return errors.Join(writeErr, closeErr)
	}
	return nil
}

func formatToolOutputReference(path string) string {
	return fmt.Sprintf("[Output saved to: %s]", path)
}

func (p *OutputPersister) snippetMaxRunes() int {
	if p == nil || p.SnippetMaxRunes <= 0 {
		return defaultToolOutputSnippetMaxRunes
	}
	return p.SnippetMaxRunes
}

func summarizeToolOutput(raw string, maxRunes int) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if maxRunes <= 0 {
		maxRunes = defaultToolOutputSnippetMaxRunes
	}

	var anyVal any
	if json.Unmarshal([]byte(raw), &anyVal) == nil {
		switch v := anyVal.(type) {
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if len(keys) > 30 {
				keys = append(keys[:30], "…")
			}
			head := fmt.Sprintf("JSON object (keys=%d): %s", len(v), strings.Join(keys, ", "))
			return cropToolOutputRunes(head, maxRunes)
		case []any:
			head := fmt.Sprintf("JSON array (len=%d)", len(v))
			return cropToolOutputRunes(head, maxRunes)
		default:
		}
	}

	lines := strings.Split(raw, "\n")
	if len(lines) <= 1 {
		return cropToolOutputRunes(raw, maxRunes)
	}
	headN, tailN := 12, 8
	if len(lines) < headN+tailN+1 {
		return cropToolOutputRunes(raw, maxRunes)
	}
	head := lines[:headN]
	tail := lines[len(lines)-tailN:]
	s := strings.TrimSpace(strings.Join(head, "\n") + "\n...\n" + strings.Join(tail, "\n"))
	return cropToolOutputRunes(s, maxRunes)
}

func cropToolOutputRunes(s string, max int) string {
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

func sanitizePathComponent(value string) string {
	const fallback = "default"
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	var b strings.Builder
	b.Grow(len(trimmed))
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	sanitized := strings.Trim(b.String(), "-")
	if sanitized == "" {
		return fallback
	}
	return sanitized
}
