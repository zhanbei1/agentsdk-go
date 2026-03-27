package skylark

// Document is a single searchable unit stored in Bleve + optional vector index.
type Document struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Text  string `json:"text"`
	// Extra JSON metadata (paths, server id, tool name, etc.)
	Meta map[string]string `json:"meta,omitempty"`
}

// Hit is one ranked search result.
type Hit struct {
	ID      string            `json:"id"`
	Kind    string            `json:"kind"`
	Title   string            `json:"title"`
	Snippet string            `json:"snippet"`
	Score   float64           `json:"score"`
	Text    string            `json:"text,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
}
