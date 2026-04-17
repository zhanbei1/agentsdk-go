package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stellarlinkco/agentsdk-go/pkg/middleware"
	"github.com/stellarlinkco/agentsdk-go/pkg/model"
	"github.com/stellarlinkco/agentsdk-go/pkg/sandbox"
	"github.com/stellarlinkco/agentsdk-go/pkg/tool"
)

const rollbackDescription = `Rolls back the most recent write/edit operation for the current session (best-effort).`

var rollbackSchema = &tool.JSONSchema{
	Type:       "object",
	Properties: map[string]interface{}{},
}

type RollbackLastStepTool struct {
	base *fileSandbox
}

func NewRollbackLastStepTool() *RollbackLastStepTool {
	return NewRollbackLastStepToolWithRoot("")
}

func NewRollbackLastStepToolWithRoot(root string) *RollbackLastStepTool {
	return &RollbackLastStepTool{base: newFileSandbox(root)}
}

func NewRollbackLastStepToolWithSandbox(root string, policy sandbox.FileSystemPolicy) *RollbackLastStepTool {
	return &RollbackLastStepTool{base: newFileSandboxWithSandbox(root, policy)}
}

func (r *RollbackLastStepTool) Name() string { return "rollback_last_step" }

func (r *RollbackLastStepTool) Description() string { return rollbackDescription }

func (r *RollbackLastStepTool) Schema() *tool.JSONSchema { return rollbackSchema }

func (r *RollbackLastStepTool) Execute(ctx context.Context, _ map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if r == nil || r.base == nil {
		return nil, errors.New("rollback tool is not initialised")
	}
	sessionID := extractSessionID(ctx)
	ent, err := tool.ReadLatestJournalEntry(sessionID)
	if err != nil {
		return nil, err
	}
	target := filepath.Clean(ent.TargetPath)
	if target == "" {
		return nil, errors.New("journal entry missing target_path")
	}
	if !isUnderRoot(target, r.base.root) {
		return nil, fmt.Errorf("refusing to rollback path outside sandbox: %s", target)
	}

	if !ent.Existed {
		// File did not exist before; best-effort delete.
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return &tool.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("rolled back by deleting %s", displayPath(target, r.base.root)),
			Data: map[string]interface{}{
				"path":    displayPath(target, r.base.root),
				"action":  "delete",
				"session": sessionID,
			},
		}, nil
	}

	beforePath := strings.TrimSpace(ent.BeforePath)
	if beforePath == "" {
		return nil, errors.New("journal entry missing before_path")
	}
	data, err := os.ReadFile(beforePath)
	if err != nil {
		return nil, err
	}
	mode := os.FileMode(ent.FileMode)
	if mode == 0 {
		mode = 0o600
	}
	if err := os.WriteFile(target, data, mode); err != nil {
		return nil, err
	}
	return &tool.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("rolled back %s", displayPath(target, r.base.root)),
		Data: map[string]interface{}{
			"path":        displayPath(target, r.base.root),
			"action":      "restore",
			"before_path": beforePath,
			"session":     sessionID,
		},
	}, nil
}

func extractSessionID(ctx context.Context) string {
	const fallback = "default"
	var session string
	if ctx != nil {
		if st, ok := ctx.Value(model.MiddlewareStateKey).(*middleware.State); ok && st != nil {
			if value, ok := st.Values["session_id"]; ok && value != nil {
				if s, err := coerceString(value); err == nil {
					session = s
				}
			}
			if session == "" {
				if value, ok := st.Values["trace.session_id"]; ok && value != nil {
					if s, err := coerceString(value); err == nil {
						session = s
					}
				}
			}
		}
		if session == "" {
			if value, ok := ctx.Value(middleware.TraceSessionIDContextKey).(string); ok {
				session = value
			} else if value, ok := ctx.Value(middleware.SessionIDContextKey).(string); ok {
				session = value
			}
		}
	}
	session = strings.TrimSpace(session)
	if session == "" {
		return fallback
	}
	return session
}

func isUnderRoot(path string, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if root == "" || root == "." {
		// Default sandbox root: assume ok.
		return true
	}
	if path == root {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(path, root+sep)
}
