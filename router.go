package main

import (
	"fmt"
	"strings"
	"time"
)

type Router struct {
	enabled bool
	peers   map[string]string // alias → inbox URL
}

func newRouter(enabled bool, peers map[string]string) *Router {
	if peers == nil {
		peers = make(map[string]string)
	}
	return &Router{enabled: enabled, peers: peers}
}

func extractAddress(content string) (alias, body string, ok bool) {
	if !strings.HasPrefix(content, "@") {
		return "", "", false
	}
	rest := content[1:]
	end := strings.IndexFunc(rest, func(r rune) bool { return !isAliasRune(r) })
	if end < 0 {
		if rest == "" {
			return "", "", false
		}
		return rest, "", true
	}
	alias = rest[:end]
	if alias == "" {
		return "", "", false
	}
	body = strings.TrimLeft(rest[end:], " \t\r\n")
	return alias, body, true
}

func isAliasRune(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_' || r == '-'
}

func (r *Router) Route(alias, body, from, traceID string) error {
	if !r.enabled {
		return fmt.Errorf("router disabled")
	}
	url, ok := r.peers[alias]
	if !ok {
		return fmt.Errorf("unknown peer '%s'", alias)
	}
	env := Envelope{
		From:      from,
		TraceID:   traceID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Messages:  []Message{{Role: "assistant", Content: body}},
	}
	return sendEnvelope(url, env)
}
