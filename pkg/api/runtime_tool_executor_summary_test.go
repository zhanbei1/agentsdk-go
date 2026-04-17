package api

import "testing"

func TestSummarizeToolOutput_JSONObjectKeys(t *testing.T) {
	got := summarizeToolOutput(`{"b":2,"a":1,"nested":{"x":true}}`, 200)
	if got == "" {
		t.Fatalf("expected summary")
	}
	if got[0:4] != "JSON" {
		t.Fatalf("expected JSON summary, got=%q", got)
	}
}
