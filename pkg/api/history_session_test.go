package api

import (
	"testing"

	"github.com/stellarlinkco/agentsdk-go/pkg/message"
)

func TestFilterSessionMessagesByRole(t *testing.T) {
	msgs := []message.Message{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "User", Content: "u2"},
	}
	got := FilterSessionMessagesByRole(msgs, "assistant")
	if len(got) != 1 || got[0].Content != "a1" {
		t.Fatalf("got %#v", got)
	}
}

func TestTrimSessionMessages(t *testing.T) {
	msgs := []message.Message{
		{Role: "user", Content: "1"},
		{Role: "assistant", Content: "2"},
		{Role: "user", Content: "3"},
	}
	got := TrimSessionMessages(msgs, 2)
	if len(got) != 2 || got[0].Content != "2" || got[1].Content != "3" {
		t.Fatalf("got %#v", got)
	}
	if len(TrimSessionMessages(msgs, 0)) != 3 {
		t.Fatal("max 0 should keep all")
	}
}

func TestSessionHistoryLoaderFromOptions_policyAndTransform(t *testing.T) {
	opts := Options{
		SessionHistoryLoader: func(id string) ([]message.Message, error) {
			if id != "s" {
				t.Fatalf("id=%q", id)
			}
			return []message.Message{
				{Role: "user", Content: "old"},
				{Role: "assistant", Content: "mid"},
				{Role: "user", Content: "new"},
				{Role: "assistant", Content: "last"},
			}, nil
		},
		SessionHistoryMaxMessages: 2,
		SessionHistoryTransform: func(id string, msgs []message.Message) []message.Message {
			if len(msgs) != 2 {
				t.Fatalf("expected 2 msgs after trim, got %d", len(msgs))
			}
			return msgs
		},
	}
	lh := sessionHistoryLoaderFromOptions(opts)
	if lh == nil {
		t.Fatal("expected loader")
	}
	out, err := lh("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Content != "new" || out[1].Content != "last" {
		t.Fatalf("got %#v", out)
	}
}

func TestSessionHistoryLoaderFromOptions_roleFilter(t *testing.T) {
	opts := Options{
		SessionHistoryLoader: func(string) ([]message.Message, error) {
			return []message.Message{
				{Role: "user", Content: "u"},
				{Role: "assistant", Content: "a"},
			}, nil
		},
		SessionHistoryRoles: []string{"assistant"},
	}
	lh := sessionHistoryLoaderFromOptions(opts)
	out, err := lh("x")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Content != "a" {
		t.Fatalf("got %#v", out)
	}
}

func TestSessionHistoryLoaderFromOptions_nilLoader(t *testing.T) {
	if sessionHistoryLoaderFromOptions(Options{}) != nil {
		t.Fatal("expected nil")
	}
}
