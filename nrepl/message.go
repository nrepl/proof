// Package nrepl is a small nREPL client built for testing servers rather
// than for everyday use. It keeps a transcript of everything sent and
// received, including frames that fail to decode, so checks can inspect
// the exact wire traffic after the fact.
package nrepl

import (
	"maps"
	"slices"
	"sort"
	"strings"
)

// Message is a request or response. Values are whatever the bencode
// decoder produced: string, int64, []any or map[string]any.
type Message map[string]any

// Str returns a string field, or "" if it's missing or not a string.
func (m Message) Str(key string) string {
	s, _ := m[key].(string)
	return s
}

// Has reports whether the message carries the key.
func (m Message) Has(key string) bool {
	_, ok := m[key]
	return ok
}

// Status returns the status flags. A status that isn't a list of strings
// comes back as nil; the wire rules report that separately.
func (m Message) Status() []string {
	list, ok := m["status"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// HasStatus reports whether every given flag is in the status.
func (m Message) HasStatus(flags ...string) bool {
	return containsAll(m.Status(), flags)
}

func containsAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

// With returns a copy of m with other's fields added.
func (m Message) With(other Message) Message {
	out := make(Message, len(m)+len(other))
	maps.Copy(out, m)
	maps.Copy(out, other)
	return out
}

// Keys returns the message keys in sorted order.
func (m Message) Keys() []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Response is every message received for one request, up to and
// including the first one whose status contains "done".
type Response struct {
	Messages []Message
	// Done is false when the request timed out before a "done" arrived.
	Done bool
}

// Values returns every "value" in order.
func (r Response) Values() []string {
	var vs []string
	for _, m := range r.Messages {
		if m.Has("value") {
			vs = append(vs, m.Str("value"))
		}
	}
	return vs
}

// Out concatenates every "out" chunk. Servers are free to split output
// across messages however they like.
func (r Response) Out() string { return r.concat("out") }

// Err concatenates every "err" chunk.
func (r Response) Err() string { return r.concat("err") }

func (r Response) concat(key string) string {
	var sb strings.Builder
	for _, m := range r.Messages {
		sb.WriteString(m.Str(key))
	}
	return sb.String()
}

// Status is the union of status flags across all messages.
func (r Response) Status() []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range r.Messages {
		for _, s := range m.Status() {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

// HasStatus reports whether any message carries all the given flags
// between them.
func (r Response) HasStatus(flags ...string) bool {
	return containsAll(r.Status(), flags)
}

// Get returns the first value of key across the messages, or nil.
func (r Response) Get(key string) any {
	return r.First(key)[key]
}

// Str returns the first string value of key across the messages, or "".
func (r Response) Str(key string) string {
	return r.First(key).Str(key)
}

// First returns the first message carrying key, or nil.
func (r Response) First(key string) Message {
	for _, m := range r.Messages {
		if m.Has(key) {
			return m
		}
	}
	return nil
}
