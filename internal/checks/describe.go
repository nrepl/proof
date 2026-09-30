package checks

import (
	"strings"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

// Ops every server must support. The draft spec lists clone and close as
// optional, but no mainstream client can work without sessions.
var requiredOps = []string{"describe", "eval", "clone", "close"}

func describeChecks() []*check.Check {
	return []*check.Check{
		{
			ID:       "describe.reply",
			Title:    "describe gets a reply",
			Severity: check.Fail,
			Why:      "CIDER and Calva send describe while connecting and won't finish connecting without an answer.",
			Refs:     []check.Ref{specDescribe, ciderOpSupported},
			Run: func(t *check.T) {
				c := t.Connect()
				t.Request(c, nrepl.Message{"op": "describe"})
			},
		},
		{
			ID:       "describe.ops-dict",
			Title:    "ops is a dict keyed by op name",
			Severity: check.Fail,
			Why:      "CIDER and Calva look ops up by key. The draft spec prefers a list, but that breaks both.",
			Refs:     []check.Ref{ciderOpSupported, calvaOpSupported, specDescribe},
			Requires: []string{"describe.reply"},
			Run: func(t *check.T) {
				switch ops := describe(t).Get("ops").(type) {
				case map[string]any:
				case nil:
					t.Violatef("describe reply has no ops")
				case []any:
					t.Violatef("ops is a list; clients index it by op name, so it must be a dict of op name to (possibly empty) dict")
				default:
					t.Violatef("ops is %s", typeName(ops))
				}
			},
		},
		{
			ID:       "describe.required-ops",
			Title:    "describe lists describe, eval, clone and close",
			Severity: check.Fail,
			Why:      "Clients check ops before using them, and CIDER can't connect without clone.",
			Refs:     []check.Ref{ciderClone, ciderOpSupported, specDescribe},
			Requires: []string{"describe.reply", "describe.ops-dict"},
			Run: func(t *check.T) {
				ops, ok := describe(t).Get("ops").(map[string]any)
				if !ok {
					t.Skipf("ops isn't a dict (see describe.ops-dict)")
				}
				var missing []string
				for _, op := range requiredOps {
					if _, ok := ops[op]; !ok {
						missing = append(missing, op)
					}
				}
				if len(missing) > 0 {
					t.Violatef("ops doesn't list %s", strings.Join(missing, ", "))
				}
				t.Notef("advertised ops: %s", strings.Join(nrepl.Message(ops).Keys(), " "))
			},
		},
		{
			ID:       "describe.versions",
			Title:    "versions identifies the runtime",
			Severity: check.Warn,
			Why:      "CIDER identifies the runtime by the keys of versions and falls back to generic behaviour when it can't.",
			Refs:     []check.Ref{ciderRuntime, nreplDescribe, issue("jank-lang/jank", 782)},
			Requires: []string{"describe.reply"},
			Run: func(t *check.T) {
				raw := describe(t).Get("versions")
				versions, ok := raw.(map[string]any)
				if !ok {
					if raw != nil {
						t.Stopf("versions is %s, not a dict", typeName(raw))
					}
					t.Stopf("describe reply has no versions")
				}
				if len(versions) == 0 {
					t.Stopf("versions is empty")
				}
				// CIDER reads version-string from these entries; the shape of
				// the others (babashka, let-go, ...) varies by runtime.
				for _, name := range []string{"nrepl", "clojure", "java"} {
					v, ok := versions[name]
					if !ok {
						continue
					}
					entry, ok := v.(map[string]any)
					if !ok {
						t.Violatef("versions.%s is %s; CIDER reads versions.%s.version-string", name, typeName(v), name)
					} else if _, ok := entry["version-string"].(string); !ok {
						t.Violatef("versions.%s has no version-string, which CIDER reads", name)
					}
				}
				t.Notef("versions: %s; CIDER would treat this server as %s", strings.Join(nrepl.Message(versions).Keys(), " "), ciderRuntimeFor(versions))
			},
		},
	}
}

// describe sends a describe request on a fresh connection.
func describe(t *check.T) nrepl.Response {
	return t.Request(t.Connect(), nrepl.Message{"op": "describe"})
}

// ciderRuntimeFor mirrors cider-runtime in cider-session.el.
func ciderRuntimeFor(versions map[string]any) string {
	if clj, ok := versions["clojure"].(map[string]any); ok {
		if _, ok := clj["version-string"]; ok {
			return "clojure"
		}
	}
	for _, rt := range []struct{ key, name string }{
		{"babashka", "babashka"},
		{"nbb-nrepl", "nbb"},
		{"scittle-nrepl", "scittle"},
		{"let-go", "let-go"},
	} {
		if _, ok := versions[rt.key]; ok {
			return rt.name
		}
	}
	return "generic"
}
