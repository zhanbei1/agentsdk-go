package skylark

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type corpusStore struct {
	mu   sync.RWMutex
	path string
	byID map[string]Document
}

func newCorpusStore(path string) *corpusStore {
	return &corpusStore{path: path, byID: map[string]Document{}}
}

func (c *corpusStore) load() error {
	if c.path == "" {
		return nil
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var payload struct {
		Docs []Document `json:"docs"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byID = map[string]Document{}
	for _, d := range payload.Docs {
		if d.ID == "" {
			continue
		}
		c.byID[d.ID] = d
	}
	return nil
}

func (c *corpusStore) replace(docs []Document) error {
	c.mu.Lock()
	c.byID = map[string]Document{}
	for _, d := range docs {
		if d.ID == "" {
			continue
		}
		c.byID[d.ID] = d
	}
	c.mu.Unlock()
	if c.path == "" {
		return nil
	}
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	payload := struct {
		Docs []Document `json:"docs"`
	}{Docs: docs}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0o600)
}

func (c *corpusStore) get(id string) (Document, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	d, ok := c.byID[id]
	return d, ok
}
