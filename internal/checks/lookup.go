package checks

import (
	"fmt"
	"net/url"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

// A symbol no server has.
const noSuchSymbol = "proof-no-such-thing"

func lookupChecks() []*check.Check {
	return []*check.Check{
		{
			ID:       "lookup.lookup",
			Title:    "lookup says what a symbol is",
			Severity: check.Fail,
			Why:      "CIDER and Conjure look symbols up with lookup when the server has no info. CIDER's docs need its info to be a dict with a name or an ns and strings where it expects them, jumping to a definition needs line to be an integer, and Conjure needs column to count from 1.",
			Refs:     []check.Ref{ciderInfoOps, ciderLookup, ciderDocHeading, ciderFindVar, conjureInfo, conjureColumn, nreplLookup},
			Snippets: []string{"lookup", "value"},
			Requires: []string{"describe.reply", "describe.ops-dict", "session.clone"},
			Run: func(t *check.T) {
				requireFallbackOp(t, "lookup", "info")
				s := t.Snippet("lookup")
				what := fmt.Sprintf("looking up %q", s.Code)
				c, fields := toolingSession(t)
				resp := t.Request(c, fields.With(nrepl.Message{"op": "lookup", "sym": s.Code}))
				for _, p := range infoProblems(field[map[string]any](t, resp, "info", what)) {
					t.Violatef("%s: %s", what, p)
				}
			},
		},
		{
			ID:       "lookup.lookup-unknown",
			Title:    "lookup finds nothing for a symbol that doesn't exist",
			Severity: check.Fail,
			Why:      "CIDER takes any dict in lookup's info for a symbol it found, so an empty one for a symbol that doesn't exist breaks its docs and jumping to a definition with an Emacs error, instead of saying it can't resolve the symbol.",
			Refs:     []check.Ref{ciderLookup, ciderDocHeading, nreplLookup},
			Snippets: []string{"value"},
			Requires: []string{"describe.reply", "describe.ops-dict", "session.clone"},
			Run: func(t *check.T) {
				requireFallbackOp(t, "lookup", "info")
				c, fields := toolingSession(t)
				resp := t.Request(c, fields.With(nrepl.Message{"op": "lookup", "sym": noSuchSymbol}))
				if info, ok := resp.Get("info").(map[string]any); ok && !resp.HasStatus("lookup-error") {
					t.Violatef("looking up a symbol that doesn't exist gave the info %v", info)
				}
			},
		},
		{
			ID:       "lookup.info",
			Title:    "info says what a symbol is",
			Severity: check.Fail,
			Why:      "CIDER, Calva, Conjure and vim-fireplace look symbols up with info, CIDER with sym and the others with symbol. Calva reads only the first message of the reply, status included, and needs file to be a URI to jump to a definition, CIDER's docs need a name or an ns and strings where it expects them, jumping to a definition needs line to be an integer, and Conjure needs column to count from 1.",
			Refs:     []check.Ref{ciderInfo, ciderDocHeading, ciderFindVar, calvaInfo, calvaHover, conjureInfo, conjureColumn, fireplaceInfo},
			Snippets: []string{"lookup", "value"},
			Requires: []string{"describe.reply", "describe.ops-dict", "session.clone"},
			Run: func(t *check.T) {
				op := ciderOp(t, "info")
				s := t.Snippet("lookup")
				c, fields := toolingSession(t)
				for _, req := range infoRequests(t, op, s.Code) {
					what := fmt.Sprintf("looking up %q with %s", s.Code, req.client)
					first := t.Request(c, fields.With(req.msg)).Messages[0]
					if !first.Has("status") {
						t.Violatef("%s: the first message of the reply has no status: %s", what, first)
					}
					problems := infoProblems(first)
					if file, ok := first["file"].(string); ok && req.client != "CIDER's sym" {
						if u, err := url.Parse(file); err != nil || u.Scheme == "" {
							problems = append(problems, fmt.Sprintf("file %q isn't a URI, so Calva can't jump to it", file))
						}
					}
					for _, p := range problems {
						t.Violatef("%s: %s", what, p)
					}
				}
			},
		},
		{
			ID:       "lookup.info-unknown",
			Title:    "info says no-info for a symbol that doesn't exist",
			Severity: check.Fail,
			Why:      "CIDER takes any reply to info without no-info for a symbol it found, so for a symbol that doesn't exist its docs and jumping to a definition fail with an Emacs error, instead of saying it can't resolve the symbol. Conjure, which sends symbol, shows the whole reply instead.",
			Refs:     []check.Ref{ciderInfo, ciderDocHeading, conjureInfo},
			Snippets: []string{"value"},
			Requires: []string{"describe.reply", "describe.ops-dict", "session.clone"},
			Run: func(t *check.T) {
				op := ciderOp(t, "info")
				c, fields := toolingSession(t)
				for _, req := range infoRequests(t, op, noSuchSymbol) {
					if resp := t.Request(c, fields.With(req.msg)); !resp.HasStatus("no-info") {
						t.Violatef("looking up a symbol that doesn't exist with %s gave status %v, without no-info", req.client, resp.Status())
					}
				}
			},
		},
		{
			ID:       "lookup.eldoc",
			Title:    "eldoc gives the arglists of a function",
			Severity: check.Fail,
			Why:      "CIDER shows the arglists of the function at the cursor from eldoc, and fails with an error on every move of the cursor when eldoc isn't a list of lists of strings, or ns or docstring isn't a string.",
			Refs:     []check.Ref{ciderEldoc},
			Snippets: []string{"lookup", "value"},
			Requires: []string{"describe.reply", "describe.ops-dict", "session.clone"},
			Run: func(t *check.T) {
				op := ciderOp(t, "eldoc")
				s := t.Snippet("lookup")
				what := fmt.Sprintf("eldoc for %q", s.Code)
				c, fields := toolingSession(t)
				resp := t.Request(c, fields.With(nrepl.Message{"op": op, "sym": s.Code, "context": "nil"}))
				for _, a := range field[[]any](t, resp, "eldoc", what) {
					if !stringList(a) {
						t.Violatef("%s has the arglist %v, which isn't a list of strings", what, a)
					}
				}
				wrongTypes[string](resp.First("eldoc"), []string{"ns", "docstring"}, func(problem, _ string) {
					t.Violatef("%s: %s, not a string", what, problem)
				})
			},
		},
	}
}

// infoRequest is how a client asks info about a symbol.
type infoRequest struct {
	client string
	msg    nrepl.Message
}

// infoRequests are the info requests of CIDER (with op, which may be
// cider/info) and, when the server has info, of the other clients.
func infoRequests(t *check.T, op, sym string) []infoRequest {
	reqs := []infoRequest{{"CIDER's sym", nrepl.Message{"op": op, "sym": sym, "context": "nil", "download-sources-jar": "1"}}}
	if op == "info" {
		reqs = append(reqs, infoRequest{"the symbol of Calva, Conjure and vim-fireplace", nrepl.Message{"op": "info", "symbol": sym}})
	} else {
		t.Notef("the server has no info, which Calva, Conjure and vim-fireplace ask for, only %s", op)
	}
	return reqs
}

// infoProblems lists what clients can't use in the info about a symbol.
func infoProblems(info map[string]any) []string {
	var problems []string
	report := func(problem, _ string) { problems = append(problems, problem) }
	m := nrepl.Message(info)
	if info["name"] == nil && info["ns"] == nil {
		problems = append(problems, "there's neither a name nor an ns")
	}
	wrongTypes[string](m, []string{"name", "ns", "doc", "arglists-str", "file", "added", "deprecated"}, report)
	wrongTypes[int64](m, []string{"line", "column"}, report)
	wrongTypes[[]any](m, []string{"see-also"}, report)
	if n, ok := info["column"].(int64); ok && n < 1 {
		problems = append(problems, fmt.Sprintf("column is %d, but columns count from 1", n))
	}
	return problems
}

// stringList reports whether v is a list of strings.
func stringList(v any) bool {
	l, ok := v.([]any)
	if !ok {
		return false
	}
	for _, x := range l {
		if _, ok := x.(string); !ok {
			return false
		}
	}
	return true
}
