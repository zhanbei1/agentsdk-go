package api

import (
	"strings"

	"github.com/stellarlinkco/agentsdk-go/pkg/message"
)

// FilterSessionMessagesByRole keeps messages whose Role is in roles (case-insensitive, trimmed).
func FilterSessionMessagesByRole(msgs []message.Message, roles ...string) []message.Message {
	if len(msgs) == 0 || len(roles) == 0 {
		return append([]message.Message(nil), msgs...)
	}
	set := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		k := strings.ToLower(strings.TrimSpace(r))
		if k == "" {
			continue
		}
		set[k] = struct{}{}
	}
	if len(set) == 0 {
		return append([]message.Message(nil), msgs...)
	}
	out := make([]message.Message, 0, len(msgs))
	for _, m := range msgs {
		if _, ok := set[strings.ToLower(strings.TrimSpace(m.Role))]; ok {
			out = append(out, m)
		}
	}
	return out
}

// TrimSessionMessages keeps at most the last max messages (oldest dropped first).
// max <= 0 returns a copy of msgs unchanged.
func TrimSessionMessages(msgs []message.Message, max int) []message.Message {
	if max <= 0 || len(msgs) <= max {
		return append([]message.Message(nil), msgs...)
	}
	return append([]message.Message(nil), msgs[len(msgs)-max:]...)
}

func applySessionHistoryPolicy(msgs []message.Message, maxN int, roles []string) []message.Message {
	out := msgs
	if len(roles) > 0 {
		out = FilterSessionMessagesByRole(out, roles...)
	}
	if maxN > 0 {
		out = TrimSessionMessages(out, maxN)
	}
	return out
}

func sessionHistoryLoaderFromOptions(opts Options) func(string) ([]message.Message, error) {
	if opts.SessionHistoryLoader == nil {
		return nil
	}
	base := opts.SessionHistoryLoader
	maxN := opts.SessionHistoryMaxMessages
	roles := opts.SessionHistoryRoles
	custom := opts.SessionHistoryTransform
	return func(id string) ([]message.Message, error) {
		msgs, err := base(id)
		if err != nil {
			return nil, err
		}
		msgs = applySessionHistoryPolicy(msgs, maxN, roles)
		if custom != nil {
			msgs = custom(id, msgs)
		}
		return msgs, nil
	}
}
