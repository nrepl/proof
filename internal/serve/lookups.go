package serve

import (
	"maps"
	"slices"
	"strings"

	"github.com/nrepl/proof/nrepl"
)

// A var as completions and lookup describe it.
type entry struct {
	name, ns, kind string
}

// visible lists what code in ns can refer to by a plain name, the way eval
// resolves it: special forms first, then clojure.core, then ns.
func (d *definitions) visible(ns string) []entry {
	byName := map[string]entry{}
	d.mu.Lock()
	for qualified, v := range d.vars {
		vns, name, _ := strings.Cut(qualified, "/")
		if vns != ns && vns != "clojure.core" || qualifyIn(ns, name) != qualified {
			continue
		}
		kind := "var"
		if _, ok := v.(*function); ok {
			kind = "function"
		}
		byName[name] = entry{name, vns, kind}
	}
	d.mu.Unlock()
	for name := range specials {
		byName[name] = entry{name, "clojure.core", "special-form"}
	}
	entries := slices.Collect(maps.Values(byName))
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.name, b.name) })
	return entries
}

// lookupNS is the namespace completions and lookup look in: the request's,
// or else the session's. ok is false for a namespace that doesn't exist.
func (s *Server) lookupNS(req nrepl.Message, sess *session) (ns string, ok bool) {
	if ns, given := req["ns"].(string); given {
		return ns, s.defs.hasNS(ns)
	}
	return sess.b.currentNS(), true
}

// completions offers the vars whose names start with the prefix, as
// nREPL's completions does, in a single message. Like nREPL, it falls back
// to clojure.core for a namespace that doesn't exist.
func (s *Server) completions(c *conn, req nrepl.Message, sess *session) {
	prefix, ok := req["prefix"].(string)
	if !ok {
		c.reply(req, map[string]any{"status": []any{"done", "completions-error", "error"}, "message": "no prefix"})
		return
	}
	ns, _ := s.lookupNS(req, sess)
	completions := []any{}
	for _, e := range s.defs.visible(ns) {
		if strings.HasPrefix(e.name, prefix) {
			completions = append(completions, map[string]any{"candidate": e.name, "ns": e.ns, "type": e.kind})
		}
	}
	c.reply(req, map[string]any{"completions": completions, "status": done})
}

// lookup says what a symbol refers to, as nREPL's lookup does: an info
// dict, or an empty list for nothing.
func (s *Server) lookup(c *conn, req nrepl.Message, sess *session) {
	sym, _ := req["sym"].(string)
	ns, known := s.lookupNS(req, sess)
	message := ""
	switch {
	case sym == "":
		message = "no conversion to symbol"
	case !known:
		message = "No namespace: " + ns + " found"
	}
	if message != "" {
		c.reply(req, map[string]any{"status": []any{"done", "error", "lookup-error"}, "message": message})
		return
	}
	var info any = []any{}
	if specials[sym] != nil {
		info = map[string]any{"name": sym, "ns": "clojure.core", "special-form": "true"}
	} else if q := qualifyIn(ns, sym); s.defs.has(q) {
		vns, name, _ := strings.Cut(q, "/")
		info = map[string]any{"name": name, "ns": vns}
	}
	c.reply(req, map[string]any{"info": info, "status": done})
}
