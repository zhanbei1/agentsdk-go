package skylark

// Knowledge kinds for retrieve_knowledge.
const (
	KindContext  = "context"  // project rules + bundled context text
	KindDocument = "document" // rules/docs/skills markdown bodies
	KindMemory   = "memory"   // AGENTS.md / memory
	KindHistory  = "history"  // conversation turns (session-scoped)
)

// Capability kinds for retrieve_capabilities.
const (
	KindSkill = "skill"
	KindTool  = "tool"
	KindMCP   = "mcp"
)
