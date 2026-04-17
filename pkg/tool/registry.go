package tool

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stellarlinkco/agentsdk-go/pkg/mcp"
)

type mcpCompressedDescriptor struct {
	Description string
	Schema      *JSONSchema
}

type mcpRefreshController struct {
	timer       *time.Timer
	inFlight    bool
	failCount   int
	nextAllowed time.Time
}

func (r *Registry) mcpDescriptorCacheGet(key string) (mcpCompressedDescriptor, bool) {
	if r == nil || strings.TrimSpace(key) == "" {
		return mcpCompressedDescriptor{}, false
	}
	r.mcpDescMu.Lock()
	defer r.mcpDescMu.Unlock()
	v, ok := r.mcpDescCache[key]
	return v, ok
}

func (r *Registry) mcpDescriptorCachePut(key string, v mcpCompressedDescriptor) {
	if r == nil || strings.TrimSpace(key) == "" {
		return
	}
	r.mcpDescMu.Lock()
	defer r.mcpDescMu.Unlock()
	r.mcpDescCache[key] = v
}

func (r *Registry) mcpDescriptorCacheKey(serverName, remoteName, desc string, schema any, descMaxRunes int, pruned bool) string {
	h := sha256.New()
	writeHashString(h, serverName)
	writeHashString(h, remoteName)
	writeHashString(h, cropRunes(strings.TrimSpace(desc), descMaxRunes))
	if pruned {
		writeHashString(h, "prune_schema_desc:true")
	} else {
		writeHashString(h, "prune_schema_desc:false")
	}
	if schema != nil {
		if b, err := json.Marshal(schema); err == nil {
			_, _ = h.Write(b)
		}
	}
	sum := h.Sum(nil)
	// short key is fine; collisions are extremely unlikely for our use.
	return fmt.Sprintf("mcpdesc:%x", sum[:12])
}

func writeHashString(h hash.Hash, s string) {
	if h == nil {
		return
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	_, _ = h.Write([]byte(s))
	_, _ = h.Write([]byte{0})
}

func cropRunes(s string, max int) string {
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

func pruneJSONSchemaDescriptions(schema *JSONSchema) *JSONSchema {
	if schema == nil {
		return nil
	}
	clone := *schema
	clone.Properties = pruneSchemaMap(clone.Properties)
	// intentionally keep Required/Type/etc.
	return &clone
}

func pruneSchemaMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return m
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = pruneSchemaAny(v)
	}
	// Remove known high-token fields at top-level.
	delete(out, "description")
	delete(out, "title")
	delete(out, "examples")
	return out
}

func pruneSchemaAny(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return pruneSchemaMap(t)
	case []any:
		cp := make([]any, 0, len(t))
		for _, it := range t {
			cp = append(cp, pruneSchemaAny(it))
		}
		return cp
	default:
		return v
	}
}

func (r *Registry) enqueueMCPRefresh(serverID, sessionID string) {
	if r == nil {
		return
	}
	serverID = strings.TrimSpace(serverID)
	sessionID = strings.TrimSpace(sessionID)
	if serverID == "" && sessionID == "" {
		return
	}

	// Resolve debounce + timeout from tracked session options.
	opts := MCPServerOptions{}
	r.mu.RLock()
	for _, info := range r.mcpSessions {
		if info == nil {
			continue
		}
		if sessionID != "" && info.sessionID == sessionID {
			opts = cloneMCPServerOptions(info.opts)
			break
		}
		if opts.Timeout == 0 && serverID != "" && info.serverID == serverID {
			opts = cloneMCPServerOptions(info.opts)
		}
	}
	r.mu.RUnlock()

	debounce := opts.RefreshDebounce
	if debounce <= 0 {
		debounce = 300 * time.Millisecond
	}
	key := serverID
	if sessionID != "" {
		key = "sid:" + sessionID
	} else {
		key = "srv:" + serverID
	}

	r.mcpRefreshMu.Lock()
	ctrl := r.mcpRefreshState[key]
	if ctrl == nil {
		ctrl = &mcpRefreshController{}
		r.mcpRefreshState[key] = ctrl
	}
	if ctrl.timer == nil {
		ctrl.timer = time.AfterFunc(debounce, func() {
			r.runMCPRefresh(key, serverID, sessionID)
		})
	} else {
		ctrl.timer.Reset(debounce)
	}
	r.mcpRefreshMu.Unlock()
}

func (r *Registry) runMCPRefresh(key, serverID, sessionID string) {
	if r == nil {
		return
	}
	r.mcpRefreshMu.Lock()
	ctrl := r.mcpRefreshState[key]
	if ctrl == nil {
		ctrl = &mcpRefreshController{}
		r.mcpRefreshState[key] = ctrl
	}
	now := time.Now()
	if ctrl.inFlight {
		r.mcpRefreshMu.Unlock()
		return
	}
	if !ctrl.nextAllowed.IsZero() && now.Before(ctrl.nextAllowed) {
		// reschedule at nextAllowed
		delay := time.Until(ctrl.nextAllowed)
		if delay < 0 {
			delay = 0
		}
		if ctrl.timer == nil {
			ctrl.timer = time.AfterFunc(delay, func() {
				r.runMCPRefresh(key, serverID, sessionID)
			})
		} else {
			ctrl.timer.Reset(delay)
		}
		r.mcpRefreshMu.Unlock()
		return
	}
	ctrl.inFlight = true
	r.mcpRefreshMu.Unlock()

	go func() {
		refreshCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := r.refreshMCPTools(refreshCtx, serverID, sessionID)

		r.mcpRefreshMu.Lock()
		defer r.mcpRefreshMu.Unlock()
		ctrl := r.mcpRefreshState[key]
		if ctrl == nil {
			ctrl = &mcpRefreshController{}
			r.mcpRefreshState[key] = ctrl
		}
		ctrl.inFlight = false
		if err == nil {
			ctrl.failCount = 0
			ctrl.nextAllowed = time.Time{}
			return
		}
		ctrl.failCount++
		// Exponential backoff capped at 30s.
		backoff := time.Duration(ctrl.failCount*ctrl.failCount) * 250 * time.Millisecond
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
		ctrl.nextAllowed = time.Now().Add(backoff)
		log.Printf("tool registry: refresh MCP tools: %v (backoff=%s)", err, backoff)
	}()
}

// Registry keeps the mapping between tool names and implementations.
type Registry struct {
	mu          sync.RWMutex
	tools       map[string]Tool
	mcpSessions []*mcpSessionInfo
	validator   Validator

	mcpDescMu    sync.Mutex
	mcpDescCache map[string]mcpCompressedDescriptor

	mcpRefreshMu    sync.Mutex
	mcpRefreshState map[string]*mcpRefreshController
}

type mcpListChangedHandler = func(context.Context, *mcp.ClientSession)

var newMCPClient = func(ctx context.Context, spec string, handler mcpListChangedHandler) (*mcp.ClientSession, error) {
	return connectMCPClientWithOptions(ctx, spec, MCPServerOptions{}, handler)
}

var buildMCPTransport = mcp.BuildSessionTransport //nolint:staticcheck // TODO: migrate to ConnectSession

type MCPServerOptions struct {
	Headers       map[string]string
	Env           map[string]string
	Timeout       time.Duration
	EnabledTools  []string
	DisabledTools []string
	ToolTimeout   time.Duration

	// ToolDescriptionMaxRunes caps MCP tool description length exposed to the model.
	// Default: 240.
	ToolDescriptionMaxRunes int
	// DisableSchemaDescriptionPrune disables removing nested "description"/"title"/"examples"
	// fields from MCP tool schemas. Pruning reduces prompt size without affecting validation.
	// Default: false (i.e. pruning is enabled).
	DisableSchemaDescriptionPrune bool

	// RefreshDebounce controls how long to debounce ToolListChanged before refresh.
	// Default: 300ms.
	RefreshDebounce time.Duration
}

var newMCPClientWithOptions = func(ctx context.Context, spec string, opts MCPServerOptions, handler mcpListChangedHandler) (*mcp.ClientSession, error) {
	return connectMCPClientWithOptions(ctx, spec, opts, handler)
}

// NewRegistry creates a registry backed by the default validator.
func NewRegistry() *Registry {
	return &Registry{
		tools:           make(map[string]Tool),
		validator:       DefaultValidator{},
		mcpDescCache:    make(map[string]mcpCompressedDescriptor),
		mcpRefreshState: make(map[string]*mcpRefreshController),
	}
}

// Register inserts a tool when its name is not in use.
func (r *Registry) Register(tool Tool) error {
	if tool == nil {
		return fmt.Errorf("tool is nil")
	}
	name := tool.Name()
	if name == "" {
		return fmt.Errorf("tool name is empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %s already registered", name)
	}

	r.tools[name] = tool
	return nil
}

// Get fetches a tool by name.
func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, exists := r.tools[name]
	if !exists {
		return nil, fmt.Errorf("tool %s not found", name)
	}
	return tool, nil
}

// List produces a snapshot of all registered tools.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}
	return tools
}

// SetValidator swaps the validator instance used before execution.
func (r *Registry) SetValidator(v Validator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.validator = v
}

// Execute runs a registered tool after optional schema validation.

func (r *Registry) Execute(ctx context.Context, name string, params map[string]interface{}) (_ *ToolResult, err error) {
	tool, err := r.Get(name)
	if err != nil {
		return nil, err
	}

	if schema := tool.Schema(); schema != nil {
		r.mu.RLock()
		validator := r.validator
		r.mu.RUnlock()

		if validator != nil {
			if err := validator.Validate(params, schema); err != nil {
				return nil, fmt.Errorf("tool %s validation failed: %w", name, err)
			}
		}
	}

	result, execErr := tool.Execute(ctx, params)
	return result, execErr
}

// RegisterMCPServer discovers tools exposed by an MCP server and registers them.
// serverPath accepts either an http(s) URL (SSE transport) or a stdio command.
func (r *Registry) RegisterMCPServer(ctx context.Context, serverPath, serverName string) error {
	ctx = nonNilContext(ctx)
	if strings.TrimSpace(serverPath) == "" {
		return fmt.Errorf("server path is empty")
	}
	serverName = strings.TrimSpace(serverName)
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	session, err := newMCPClient(connectCtx, serverPath, r.mcpToolsChangedHandler(serverPath))
	if err != nil {
		if ctxErr := connectCtx.Err(); ctxErr != nil {
			return fmt.Errorf("connect MCP client: %w", ctxErr)
		}
		return fmt.Errorf("connect MCP client: %w", err)
	}
	if session == nil {
		return fmt.Errorf("connect MCP client: session is nil")
	}
	success := false
	defer func() {
		if !success {
			_ = session.Close()
		}
	}()

	if err := connectCtx.Err(); err != nil {
		return fmt.Errorf("initialize MCP client: connect context: %w", err)
	}
	if session.InitializeResult() == nil {
		return fmt.Errorf("initialize MCP client: mcp session missing initialize result")
	}

	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var tools []*mcp.Tool
	for tool, iterErr := range session.Tools(listCtx, nil) {
		if iterErr != nil {
			return fmt.Errorf("list MCP tools: %w", iterErr)
		}
		tools = append(tools, tool)
	}
	if len(tools) == 0 {
		return fmt.Errorf("MCP server returned no tools")
	}

	wrappers, names, err := r.buildRemoteToolWrappers(session, serverName, tools, MCPServerOptions{})
	if err != nil {
		return err
	}
	if err := r.registerMCPSession(serverPath, serverName, session, wrappers, names, MCPServerOptions{}); err != nil {
		return err
	}

	success = true
	return nil
}

func (r *Registry) RegisterMCPServerWithOptions(ctx context.Context, serverPath, serverName string, opts MCPServerOptions) error {
	ctx = nonNilContext(ctx)
	if strings.TrimSpace(serverPath) == "" {
		return fmt.Errorf("server path is empty")
	}
	serverName = strings.TrimSpace(serverName)

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	session, err := newMCPClientWithOptions(connectCtx, serverPath, opts, r.mcpToolsChangedHandler(serverPath))
	if err != nil {
		if ctxErr := connectCtx.Err(); ctxErr != nil {
			return fmt.Errorf("connect MCP client: %w", ctxErr)
		}
		return fmt.Errorf("connect MCP client: %w", err)
	}
	if session == nil {
		return fmt.Errorf("connect MCP client: session is nil")
	}
	success := false
	defer func() {
		if !success {
			_ = session.Close()
		}
	}()

	if err := connectCtx.Err(); err != nil {
		return fmt.Errorf("initialize MCP client: connect context: %w", err)
	}
	if session.InitializeResult() == nil {
		return fmt.Errorf("initialize MCP client: mcp session missing initialize result")
	}

	listCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var tools []*mcp.Tool
	for tool, iterErr := range session.Tools(listCtx, nil) {
		if iterErr != nil {
			return fmt.Errorf("list MCP tools: %w", iterErr)
		}
		tools = append(tools, tool)
	}
	if len(tools) == 0 {
		return fmt.Errorf("MCP server returned no tools")
	}

	wrappers, names, err := r.buildRemoteToolWrappers(session, serverName, tools, opts)
	if err != nil {
		return err
	}
	if err := r.registerMCPSession(serverPath, serverName, session, wrappers, names, opts); err != nil {
		return err
	}

	success = true
	return nil
}

// Close terminates all tracked MCP sessions.
// Errors are logged and ignored to avoid masking shutdown flows.
func (r *Registry) Close() {
	r.mu.Lock()
	sessions := r.mcpSessions
	r.mcpSessions = nil
	r.mu.Unlock()

	for _, info := range sessions {
		if info == nil || info.session == nil {
			continue
		}
		if err := info.session.Close(); err != nil {
			log.Printf("tool registry: close MCP session: %v", err)
		}
	}
}

func connectMCPClientWithOptions(ctx context.Context, spec string, opts MCPServerOptions, handler mcpListChangedHandler) (*mcp.ClientSession, error) {
	transport, err := buildMCPTransport(ctx, spec)
	if err != nil {
		return nil, err
	}
	if err := applyMCPTransportOptions(transport, opts); err != nil {
		return nil, err
	}

	var clientOpts *mcp.ClientOptions
	if handler != nil {
		clientOpts = &mcp.ClientOptions{
			ToolListChangedHandler: toolListChangedHandler(handler),
		}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "agentsdk-go", Version: "dev"}, clientOpts)

	dialCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-done:
		}
	}()

	session, err := client.Connect(dialCtx, transport, nil)
	close(done)
	if err != nil {
		cancel()
		return nil, err
	}
	return session, nil
}

func toolListChangedHandler(handler mcpListChangedHandler) func(context.Context, *mcp.ToolListChangedRequest) {
	return func(ctx context.Context, req *mcp.ToolListChangedRequest) {
		if handler == nil || req == nil || req.Session == nil {
			return
		}
		handler(ctx, req.Session)
	}
}

type mcpSessionInfo struct {
	serverID   string
	serverName string
	sessionID  string
	session    *mcp.ClientSession
	toolNames  map[string]struct{}
	opts       MCPServerOptions
}

func (r *Registry) registerMCPSession(serverID, serverName string, session *mcp.ClientSession, wrappers []Tool, names []string, opts MCPServerOptions) error {
	if session == nil {
		return fmt.Errorf("mcp session is nil")
	}
	if len(wrappers) != len(names) {
		return fmt.Errorf("mcp tools mismatch")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, name := range names {
		if _, exists := r.tools[name]; exists {
			return fmt.Errorf("tool %s already registered", name)
		}
	}
	for i, tool := range wrappers {
		r.tools[names[i]] = tool
	}
	info := &mcpSessionInfo{
		serverID:   strings.TrimSpace(serverID),
		serverName: strings.TrimSpace(serverName),
		sessionID:  session.ID(),
		session:    session,
		toolNames:  toNameSet(names),
		opts:       cloneMCPServerOptions(opts),
	}
	r.mcpSessions = append(r.mcpSessions, info)
	return nil
}

func (r *Registry) buildRemoteToolWrappers(session *mcp.ClientSession, serverName string, tools []*mcp.Tool, opts MCPServerOptions) ([]Tool, []string, error) {
	wrappers := make([]Tool, 0, len(tools))
	names := make([]string, 0, len(tools))
	seen := map[string]struct{}{}
	filter := newMCPToolFilter(opts.EnabledTools, opts.DisabledTools)
	descMax := opts.ToolDescriptionMaxRunes
	if descMax <= 0 {
		descMax = 240
	}
	pruneSchemaDesc := !opts.DisableSchemaDescriptionPrune
	for _, desc := range tools {
		if desc == nil || strings.TrimSpace(desc.Name) == "" {
			return nil, nil, fmt.Errorf("encountered MCP tool with empty name")
		}
		toolName := desc.Name
		if serverName != "" {
			toolName = fmt.Sprintf("%s__%s", serverName, desc.Name)
		}
		if !filter.allows(desc.Name, toolName) {
			continue
		}
		if _, ok := seen[toolName]; ok {
			return nil, nil, fmt.Errorf("tool %s already registered", toolName)
		}
		seen[toolName] = struct{}{}

		cacheKey := ""
		if r != nil {
			cacheKey = r.mcpDescriptorCacheKey(serverName, desc.Name, desc.Description, desc.InputSchema, descMax, pruneSchemaDesc)
			if cacheKey != "" {
				if cached, ok := r.mcpDescriptorCacheGet(cacheKey); ok {
					wrappers = append(wrappers, &remoteTool{
						name:        toolName,
						remoteName:  desc.Name,
						description: cached.Description,
						schema:      cached.Schema,
						session:     session,
						timeout:     opts.ToolTimeout,
					})
					names = append(names, toolName)
					continue
				}
			}
		}

		schema, err := convertMCPSchema(desc.InputSchema)
		if err != nil {
			return nil, nil, fmt.Errorf("parse schema for %s: %w", desc.Name, err)
		}
		if pruneSchemaDesc {
			schema = pruneJSONSchemaDescriptions(schema)
		}
		compressedDesc := cropRunes(strings.TrimSpace(desc.Description), descMax)

		if cacheKey != "" && r != nil {
			r.mcpDescriptorCachePut(cacheKey, mcpCompressedDescriptor{Description: compressedDesc, Schema: schema})
		}
		wrappers = append(wrappers, &remoteTool{
			name:        toolName,
			remoteName:  desc.Name,
			description: compressedDesc,
			schema:      schema,
			session:     session,
			timeout:     opts.ToolTimeout,
		})
		names = append(names, toolName)
	}
	if len(wrappers) == 0 {
		return nil, nil, fmt.Errorf("MCP server returned no tools after applying filters")
	}
	return wrappers, names, nil
}

func (r *Registry) mcpToolsChangedHandler(serverID string) mcpListChangedHandler {
	if r == nil {
		return nil
	}
	serverID = strings.TrimSpace(serverID)
	return func(ctx context.Context, session *mcp.ClientSession) {
		sessionID := ""
		if session != nil {
			sessionID = session.ID()
		}
		r.enqueueMCPRefresh(serverID, sessionID)
	}
}

func (r *Registry) refreshMCPTools(ctx context.Context, serverID, sessionID string) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	serverID = strings.TrimSpace(serverID)
	sessionID = strings.TrimSpace(sessionID)

	var (
		serverName string
		session    *mcp.ClientSession
		opts       MCPServerOptions
	)
	r.mu.RLock()
	for _, info := range r.mcpSessions {
		if info == nil {
			continue
		}
		if sessionID != "" && info.sessionID == sessionID {
			serverName = info.serverName
			session = info.session
			opts = cloneMCPServerOptions(info.opts)
			break
		}
		if session == nil && serverID != "" && info.serverID == serverID {
			serverName = info.serverName
			session = info.session
			opts = cloneMCPServerOptions(info.opts)
		}
	}
	r.mu.RUnlock()
	if session == nil {
		return fmt.Errorf("mcp session not found")
	}

	listCtx, cancel := context.WithTimeout(nonNilContext(ctx), 10*time.Second)
	defer cancel()

	var tools []*mcp.Tool
	for tool, iterErr := range session.Tools(listCtx, nil) {
		if iterErr != nil {
			return fmt.Errorf("list MCP tools: %w", iterErr)
		}
		tools = append(tools, tool)
	}
	if len(tools) == 0 {
		return fmt.Errorf("MCP server returned no tools")
	}

	wrappers, names, err := r.buildRemoteToolWrappers(session, serverName, tools, opts)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	info := r.findMCPSessionLocked(serverID, sessionID)
	if info == nil {
		return fmt.Errorf("mcp session not tracked")
	}
	for _, name := range names {
		if _, exists := r.tools[name]; exists {
			if info.toolNames == nil {
				return fmt.Errorf("tool %s already registered", name)
			}
			if _, ok := info.toolNames[name]; !ok {
				return fmt.Errorf("tool %s already registered", name)
			}
		}
	}
	for name := range info.toolNames {
		delete(r.tools, name)
	}
	for i, tool := range wrappers {
		r.tools[names[i]] = tool
	}
	info.toolNames = toNameSet(names)
	if info.sessionID == "" {
		info.sessionID = session.ID()
	}
	if info.serverID == "" {
		info.serverID = serverID
	}
	if info.serverName == "" {
		info.serverName = serverName
	}
	return nil
}

func (r *Registry) findMCPSessionLocked(serverID, sessionID string) *mcpSessionInfo {
	serverID = strings.TrimSpace(serverID)
	sessionID = strings.TrimSpace(sessionID)
	for _, info := range r.mcpSessions {
		if info == nil {
			continue
		}
		if sessionID != "" && info.sessionID == sessionID {
			return info
		}
		if info.sessionID == "" && info.session != nil && sessionID != "" && info.session.ID() == sessionID {
			return info
		}
		if serverID != "" && info.serverID == serverID {
			return info
		}
	}
	return nil
}

func toNameSet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		out[name] = struct{}{}
	}
	return out
}

type mcpToolFilter struct {
	enabled  map[string]struct{}
	disabled map[string]struct{}
}

func newMCPToolFilter(enabled, disabled []string) mcpToolFilter {
	return mcpToolFilter{
		enabled:  normalizeMCPToolNameSet(enabled),
		disabled: normalizeMCPToolNameSet(disabled),
	}
}

func normalizeMCPToolNameSet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		out[name] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (f mcpToolFilter) allows(remoteName, localName string) bool {
	if len(f.enabled) > 0 && !f.matches(f.enabled, remoteName, localName) {
		return false
	}
	if len(f.disabled) > 0 && f.matches(f.disabled, remoteName, localName) {
		return false
	}
	return true
}

func (f mcpToolFilter) matches(set map[string]struct{}, names ...string) bool {
	if len(set) == 0 {
		return false
	}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, ok := set[name]; ok {
			return true
		}
	}
	return false
}

func cloneMCPServerOptions(src MCPServerOptions) MCPServerOptions {
	out := src
	out.Headers = cloneStringMap(src.Headers)
	out.Env = cloneStringMap(src.Env)
	out.EnabledTools = append([]string(nil), src.EnabledTools...)
	out.DisabledTools = append([]string(nil), src.DisabledTools...)
	return out
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func applyMCPTransportOptions(transport mcp.Transport, opts MCPServerOptions) error {
	if transport == nil {
		return errors.New("mcp transport is nil")
	}
	if len(opts.Headers) == 0 && len(opts.Env) == 0 {
		return nil
	}

	switch impl := transport.(type) {
	case *mcp.CommandTransport:
		if len(opts.Env) == 0 {
			return nil
		}
		if impl == nil || impl.Command == nil {
			return errors.New("mcp stdio transport missing command")
		}
		impl.Command.Env = mergeEnv(impl.Command.Env, opts.Env)
	case *mcp.SSEClientTransport:
		if len(opts.Headers) == 0 {
			return nil
		}
		impl.HTTPClient = withInjectedHeaders(impl.HTTPClient, opts.Headers)
	case *mcp.StreamableClientTransport:
		if len(opts.Headers) == 0 {
			return nil
		}
		impl.HTTPClient = withInjectedHeaders(impl.HTTPClient, opts.Headers)
	}
	return nil
}

func withInjectedHeaders(client *http.Client, headers map[string]string) *http.Client {
	if len(headers) == 0 {
		return client
	}
	if client == nil {
		client = &http.Client{}
	}

	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = &headerRoundTripper{
		base:    base,
		headers: normalizeHeaders(headers),
	}
	return client
}

func normalizeHeaders(headers map[string]string) http.Header {
	if len(headers) == 0 {
		return nil
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(http.Header, len(keys))
	for _, raw := range keys {
		key := http.CanonicalHeaderKey(strings.TrimSpace(raw))
		out.Set(key, strings.TrimSpace(headers[raw]))
	}
	return out
}

type headerRoundTripper struct {
	base    http.RoundTripper
	headers http.Header
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := h.base
	if base == nil {
		base = http.DefaultTransport
	}
	if req == nil {
		return nil, errors.New("request is nil")
	}
	if len(h.headers) == 0 {
		return base.RoundTrip(req)
	}

	clone := req.Clone(req.Context())
	clone.Header = clone.Header.Clone()
	for k, vals := range h.headers {
		clone.Header.Del(k)
		for _, v := range vals {
			if strings.TrimSpace(v) == "" {
				continue
			}
			clone.Header.Add(k, v)
		}
	}
	return base.RoundTrip(clone)
}

func mergeEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	if base == nil {
		base = os.Environ()
	}

	keys := make([]string, 0, len(extra))
	trimmed := make(map[string]string, len(extra))
	for k, v := range extra {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		trimmed[key] = v
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(base)+len(keys))
	seen := map[string]struct{}{}
	for _, entry := range base {
		k, _, ok := strings.Cut(entry, "=")
		if !ok || k == "" {
			continue
		}
		if _, ok := trimmed[k]; ok {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, entry)
	}
	for _, key := range keys {
		out = append(out, fmt.Sprintf("%s=%s", key, trimmed[key]))
	}
	return out
}

func convertMCPSchema(raw any) (*JSONSchema, error) {
	if raw == nil {
		return nil, nil
	}
	var (
		data []byte
		err  error
	)
	switch v := raw.(type) {
	case json.RawMessage:
		if len(v) == 0 {
			return nil, nil
		}
		data = v
	case []byte:
		if len(v) == 0 {
			return nil, nil
		}
		data = v
	default:
		data, err = json.Marshal(raw)
		if err != nil {
			return nil, err
		}
	}
	var schema JSONSchema
	if err := json.Unmarshal(data, &schema); err == nil && schema.Type != "" {
		return &schema, nil
	}
	var generic map[string]interface{}
	if err := json.Unmarshal(data, &generic); err != nil {
		return nil, err
	}
	if t, ok := generic["type"].(string); ok {
		schema.Type = t
	}
	if props, ok := generic["properties"].(map[string]interface{}); ok {
		schema.Properties = props
	}
	if req, ok := generic["required"].([]interface{}); ok {
		for _, value := range req {
			if name, ok := value.(string); ok {
				schema.Required = append(schema.Required, name)
			}
		}
	}
	return &schema, nil
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

type remoteTool struct {
	name        string
	remoteName  string
	description string
	schema      *JSONSchema
	session     *mcp.ClientSession
	timeout     time.Duration
}

func (r *remoteTool) Name() string        { return r.name }
func (r *remoteTool) Description() string { return r.description }
func (r *remoteTool) Schema() *JSONSchema { return r.schema }

func (r *remoteTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	if r.session == nil {
		return nil, fmt.Errorf("mcp session is nil")
	}
	if params == nil {
		params = map[string]interface{}{}
	}
	callCtx := nonNilContext(ctx)
	if r.timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(callCtx, r.timeout)
		defer cancel()
	}
	remoteName := r.remoteName
	if remoteName == "" {
		remoteName = r.name
	}
	res, err := r.session.CallTool(callCtx, &mcp.CallToolParams{
		Name:      remoteName,
		Arguments: params,
	})
	if err != nil {
		if r.timeout > 0 && errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("mcp tool %s timeout after %s: %w", remoteName, r.timeout, err)
		}
		return nil, err
	}
	if res == nil {
		return nil, fmt.Errorf("MCP call returned nil result")
	}
	output := firstTextContent(res.Content)
	if output == "" {
		if payload, err := json.Marshal(res.Content); err == nil {
			output = string(payload)
		}
	}
	return &ToolResult{
		Success: true,
		Output:  output,
		Data:    res.Content,
	}, nil
}

func firstTextContent(content []mcp.Content) string {
	for _, part := range content {
		if txt, ok := part.(*mcp.TextContent); ok {
			return txt.Text
		}
	}
	return ""
}
