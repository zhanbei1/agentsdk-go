package skylark

import "testing"

func TestSearchHistory(t *testing.T) {
	turns := []HistoryTurn{
		{Role: "user", Text: "fix the login bug"},
		{Role: "assistant", Text: "I'll grep for auth"},
	}
	hits := SearchHistory("login auth", turns, 5)
	if len(hits) == 0 {
		t.Fatalf("expected hits")
	}
	if hits[0].Kind != KindHistory {
		t.Fatalf("kind: %s", hits[0].Kind)
	}
}

func TestCosineSimilarity(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	if cosineSimilarity(a, b) < 0.99 {
		t.Fatalf("expected ~1")
	}
}
