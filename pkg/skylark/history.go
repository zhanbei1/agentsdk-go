package skylark

import (
	"sort"
	"strconv"
	"strings"
)

// HistoryTurn is one message line for retrieval (no import of pkg/message).
type HistoryTurn struct {
	Role string
	Text string
}

// SearchHistory ranks conversation turns by simple token overlap (no Bleve).
func SearchHistory(query string, turns []HistoryTurn, limit int) []Hit {
	q := strings.TrimSpace(query)
	if q == "" || len(turns) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 8
	}
	qTok := tokenize(q)
	if len(qTok) == 0 {
		return nil
	}
	type scored struct {
		idx   int
		score float64
	}
	var out []scored
	for i, t := range turns {
		tok := tokenize(t.Role + " " + t.Text)
		if len(tok) == 0 {
			continue
		}
		score := overlapScore(qTok, tok)
		if score <= 0 {
			continue
		}
		out = append(out, scored{idx: i, score: score})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].idx < out[j].idx
	})
	hits := make([]Hit, 0, len(out))
	for k, s := range out {
		if k >= limit {
			break
		}
		t := turns[s.idx]
		text := strings.TrimSpace(t.Role + ": " + t.Text)
		hits = append(hits, Hit{
			ID:      "history:" + strconv.Itoa(s.idx),
			Kind:    KindHistory,
			Title:   t.Role,
			Snippet: snippetFrom(text, q, 320),
			Score:   s.score,
			Text:    text,
		})
	}
	return hits
}

func tokenize(s string) map[string]int {
	s = strings.ToLower(s)
	var cur strings.Builder
	toks := map[string]int{}
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		w := cur.String()
		if len(w) >= 2 {
			toks[w]++
		}
		cur.Reset()
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return toks
}

func overlapScore(q, doc map[string]int) float64 {
	var num, den float64
	for w, c := range q {
		if d, ok := doc[w]; ok {
			num += float64(minInt(c, d))
		}
		den += float64(c)
	}
	if den == 0 {
		return 0
	}
	return num / den
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
