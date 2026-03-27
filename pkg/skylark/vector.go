package skylark

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
)

// vectorStore holds per-document embedding vectors (persisted as JSON).
type vectorStore struct {
	mu    sync.RWMutex
	path  string
	byID  map[string][]float32
	dim   int
	dirty bool
}

func newVectorStore(path string) *vectorStore {
	return &vectorStore{path: path, byID: map[string][]float32{}}
}

func (v *vectorStore) load() error {
	if v.path == "" {
		return nil
	}
	data, err := os.ReadFile(v.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var payload struct {
		Vectors map[string][]float32 `json:"vectors"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.byID = payload.Vectors
	if len(v.byID) > 0 {
		for _, vec := range v.byID {
			v.dim = len(vec)
			break
		}
	}
	return nil
}

func (v *vectorStore) persist() error {
	if v.path == "" {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.dirty {
		return nil
	}
	dir := filepath.Dir(v.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	payload := struct {
		Vectors map[string][]float32 `json:"vectors"`
	}{Vectors: v.byID}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(v.path, data, 0o600); err != nil {
		return err
	}
	v.dirty = false
	return nil
}

func (v *vectorStore) set(id string, vec []float32) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(vec) == 0 {
		delete(v.byID, id)
	} else {
		v.byID[id] = vec
		if v.dim == 0 {
			v.dim = len(vec)
		}
	}
	v.dirty = true
}

func (v *vectorStore) get(id string) ([]float32, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	vec, ok := v.byID[id]
	return vec, ok
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func normalizeScores(scores []float64) []float64 {
	if len(scores) == 0 {
		return scores
	}
	minV, maxV := scores[0], scores[0]
	for _, s := range scores {
		if s < minV {
			minV = s
		}
		if s > maxV {
			maxV = s
		}
	}
	out := make([]float64, len(scores))
	den := maxV - minV
	if den <= 1e-9 {
		for i := range out {
			out[i] = 1
		}
		return out
	}
	for i, s := range scores {
		out[i] = (s - minV) / den
	}
	return out
}
