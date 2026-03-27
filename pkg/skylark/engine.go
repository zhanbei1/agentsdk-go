package skylark

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	searchquery "github.com/blevesearch/bleve/v2/search/query"
	"github.com/tmc/langchaingo/embeddings"
)

// Engine indexes project corpus (memory, rules, skills, tools) and supports
// hybrid Bleve + optional vector ranking.
type Engine struct {
	dataDir string

	idx      bleve.Index
	vectors  *vectorStore
	corpus   *corpusStore
	embedder embeddings.Embedder

	TextWeight float64
	VecWeight  float64
}

// NewEngine opens or creates indexes under dataDir (.agents/skylark by default).
func NewEngine(dataDir string, emb embeddings.Embedder) (*Engine, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("skylark: dataDir is empty")
	}
	dataDir = filepath.Clean(dataDir)
	e := &Engine{
		dataDir:    dataDir,
		vectors:    newVectorStore(filepath.Join(dataDir, "vectors.json")),
		corpus:     newCorpusStore(filepath.Join(dataDir, "corpus.json")),
		embedder:   emb,
		TextWeight: 0.55,
		VecWeight:  0.45,
	}
	if err := e.vectors.load(); err != nil {
		return nil, fmt.Errorf("skylark: load vectors: %w", err)
	}
	if err := e.corpus.load(); err != nil {
		return nil, fmt.Errorf("skylark: load corpus: %w", err)
	}
	idxPath := filepath.Join(e.dataDir, "bleve")
	var idx bleve.Index
	if _, err := os.Stat(idxPath); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("skylark: stat bleve: %w", err)
		}
		if err := os.MkdirAll(e.dataDir, 0o755); err != nil {
			return nil, err
		}
		m := buildIndexMapping()
		var err2 error
		idx, err2 = bleve.New(idxPath, m)
		if err2 != nil {
			return nil, fmt.Errorf("skylark: create bleve: %w", err2)
		}
	} else {
		var err error
		idx, err = bleve.Open(idxPath)
		if err != nil {
			return nil, fmt.Errorf("skylark: open bleve: %w", err)
		}
	}
	e.idx = idx
	return e, nil
}

func buildIndexMapping() mapping.IndexMapping {
	idxMap := bleve.NewIndexMapping()
	doc := bleve.NewDocumentMapping()

	kind := bleve.NewKeywordFieldMapping()
	doc.AddFieldMappingsAt("kind", kind)

	title := bleve.NewTextFieldMapping()
	title.Analyzer = "en"
	doc.AddFieldMappingsAt("title", title)

	text := bleve.NewTextFieldMapping()
	text.Analyzer = "en"
	doc.AddFieldMappingsAt("text", text)

	idxMap.DefaultMapping = doc
	return idxMap
}

type indexDoc struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

// Rebuild replaces the in-memory Bleve index and optional vectors from documents.
func (e *Engine) Rebuild(ctx context.Context, docs []Document) error {
	if e == nil || e.idx == nil {
		return fmt.Errorf("skylark: engine is nil")
	}
	if err := e.idx.Close(); err != nil {
		return fmt.Errorf("skylark: close index: %w", err)
	}
	idxPath := filepath.Join(e.dataDir, "bleve")
	_ = os.RemoveAll(idxPath)
	if err := os.MkdirAll(e.dataDir, 0o755); err != nil {
		return err
	}
	m := buildIndexMapping()
	idx, err := bleve.New(idxPath, m)
	if err != nil {
		return fmt.Errorf("skylark: recreate bleve: %w", err)
	}
	e.idx = idx

	batch := e.idx.NewBatch()
	for _, d := range docs {
		id := strings.TrimSpace(d.ID)
		if id == "" {
			continue
		}
		body := indexDoc{Kind: d.Kind, Title: d.Title, Text: d.Text}
		if err := batch.Index(id, body); err != nil {
			return err
		}
	}
	if err := e.idx.Batch(batch); err != nil {
		return err
	}
	if err := e.corpus.replace(docs); err != nil {
		return err
	}

	// Vectors
	e.vectors = newVectorStore(filepath.Join(e.dataDir, "vectors.json"))
	if e.embedder != nil && len(docs) > 0 {
		texts := make([]string, 0, len(docs))
		ids := make([]string, 0, len(docs))
		for _, d := range docs {
			id := strings.TrimSpace(d.ID)
			if id == "" {
				continue
			}
			combined := strings.TrimSpace(d.Title + "\n" + d.Text)
			if combined == "" {
				continue
			}
			texts = append(texts, combined)
			ids = append(ids, id)
		}
		vecs, err := e.embedder.EmbedDocuments(ctx, texts)
		if err != nil {
			return fmt.Errorf("skylark: embed corpus: %w", err)
		}
		if len(vecs) != len(ids) {
			return fmt.Errorf("skylark: embedding length mismatch")
		}
		for i, id := range ids {
			e.vectors.set(id, vecs[i])
		}
	}
	return e.vectors.persist()
}

// SearchIndex runs hybrid retrieval over indexed documents (not session history).
func (e *Engine) SearchIndex(ctx context.Context, queryText string, kinds map[string]struct{}, limit int) ([]Hit, error) {
	if e == nil || e.idx == nil {
		return nil, fmt.Errorf("skylark: engine is nil")
	}
	q := strings.TrimSpace(queryText)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 8
	}

	var mq searchquery.Query
	if len(kinds) > 0 {
		var disj []searchquery.Query
		for k := range kinds {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			tq := searchquery.NewTermQuery(k)
			tq.SetField("kind")
			disj = append(disj, tq)
		}
		if len(disj) == 0 {
			mq = searchquery.NewMatchQuery(q)
		} else {
			kindQ := searchquery.NewDisjunctionQuery(disj)
			textQ := searchquery.NewMatchQuery(q)
			textQ.SetField("text")
			titleQ := searchquery.NewMatchQuery(q)
			titleQ.SetField("title")
			content := searchquery.NewDisjunctionQuery([]searchquery.Query{textQ, titleQ})
			mq = searchquery.NewConjunctionQuery([]searchquery.Query{kindQ, content})
		}
	} else {
		textQ := searchquery.NewMatchQuery(q)
		textQ.SetField("text")
		titleQ := searchquery.NewMatchQuery(q)
		titleQ.SetField("title")
		mq = searchquery.NewDisjunctionQuery([]searchquery.Query{textQ, titleQ})
	}

	req := bleve.NewSearchRequestOptions(mq, limit*3, 0, false)
	req.SortBy([]string{"-_score"})
	res, err := e.idx.SearchInContext(ctx, req)
	if err != nil {
		return nil, err
	}

	type cand struct {
		id    string
		score float64
	}
	var cands []cand
	for _, h := range res.Hits {
		cands = append(cands, cand{id: h.ID, score: h.Score})
	}
	textScores := make([]float64, len(cands))
	for i := range cands {
		textScores[i] = cands[i].score
	}
	textNorm := normalizeScores(textScores)

	var qVec []float32
	if e.embedder != nil {
		qVec, err = e.embedder.EmbedQuery(ctx, q)
		if err != nil {
			qVec = nil
		}
	}

	hits := make([]Hit, 0, len(cands))
	for i, c := range cands {
		doc, ok := e.corpus.get(c.id)
		if !ok {
			continue
		}
		hybrid := textNorm[i]
		if len(qVec) > 0 && e.embedder != nil {
			if docVec, ok := e.vectors.get(c.id); ok && len(docVec) == len(qVec) {
				hybrid = e.TextWeight*textNorm[i] + e.VecWeight*cosineSimilarity(qVec, docVec)
			}
		}
		snippet := snippetFrom(doc.Text, q, 240)
		hits = append(hits, Hit{
			ID:      c.id,
			Kind:    doc.Kind,
			Title:   doc.Title,
			Snippet: snippet,
			Score:   hybrid,
			Text:    doc.Text,
			Meta:    doc.Meta,
		})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func snippetFrom(text, query string, max int) string {
	t := strings.TrimSpace(text)
	if len(t) <= max {
		return t
	}
	lower := strings.ToLower(t)
	q := strings.Fields(strings.ToLower(query))
	if len(q) == 0 {
		if max > len(t) {
			return t
		}
		return t[:max] + "…"
	}
	best := 0
	for _, w := range q {
		if len(w) < 2 {
			continue
		}
		if i := strings.Index(lower, w); i >= 0 {
			if best == 0 || i < best {
				best = i
			}
		}
	}
	start := best
	if start > 40 {
		start -= 20
	}
	if start+max > len(t) {
		start = len(t) - max
		if start < 0 {
			start = 0
		}
	}
	out := t[start:]
	if len(out) > max {
		out = out[:max] + "…"
	}
	return out
}

// Close releases Bleve resources.
func (e *Engine) Close() error {
	if e == nil || e.idx == nil {
		return nil
	}
	return e.idx.Close()
}
