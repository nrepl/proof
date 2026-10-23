package checks

import (
	"slices"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

func completionChecks() []*check.Check {
	return []*check.Check{
		{
			ID:       "completion.completions",
			Title:    "completions offers what the code could be",
			Severity: check.Fail,
			Why:      "CIDER and Conjure complete code with completions when the server has no complete. Conjure reads the completions only from the message with done, and CIDER stops completing with an error at a candidate that isn't a dict with a candidate.",
			Refs:     []check.Ref{ciderCompleteOps, ciderCandidates, conjureComplete, nreplCompletions},
			Snippets: []string{"completion", "value"},
			Requires: []string{"describe.reply", "describe.ops-dict", "session.clone"},
			Run: func(t *check.T) {
				requireFallbackOp(t, "completions", "complete")
				s := t.Snippet("completion")
				c, fields := toolingSession(t)
				resp := t.Request(c, fields.With(nrepl.Message{"op": "completions", "prefix": s.Code}))
				completionsWhere(t, "completions", resp, false)
				checkCandidates(t, "completions", resp, s.Value)
			},
		},
		{
			ID:       "completion.complete",
			Title:    "complete offers what the code could be, given a prefix",
			Severity: check.Fail,
			Why:      "CIDER completes code with complete and its prefix, and stops completing with an error at a candidate that isn't a dict with a candidate.",
			Refs:     []check.Ref{ciderCompleteOps, ciderComplete, ciderCandidates},
			Snippets: []string{"completion", "value"},
			Requires: []string{"describe.reply", "describe.ops-dict", "session.clone"},
			Run: func(t *check.T) {
				op := ciderOp(t, "complete")
				s := t.Snippet("completion")
				c, fields := toolingSession(t)
				resp := t.Request(c, fields.With(nrepl.Message{"op": op, "prefix": s.Code, "context": "nil",
					"sort-order": "by-name", "enhanced-cljs-completion?": "t"}))
				checkCandidates(t, op+" with a prefix", resp, s.Value)
			},
		},
		{
			ID:       "completion.complete-symbol",
			Title:    "complete offers what the code could be, given a symbol",
			Severity: check.Fail,
			Why:      "Calva, Conjure and vim-fireplace send complete a symbol rather than a prefix (and Calva a context of 0 outside a form). Calva reads the completions from the first message only and loses them all at a candidate that isn't a dict, and Conjure reads them from the message with done only.",
			Refs:     []check.Ref{calvaComplete, calvaFirstReply, calvaCandidates, conjureComplete, fireplaceComplete},
			Snippets: []string{"completion", "value"},
			Requires: []string{"describe.reply", "describe.ops-dict", "session.clone"},
			Run: func(t *check.T) {
				requireOp(t, "complete")
				s := t.Snippet("completion")
				c, fields := toolingSession(t)
				for _, req := range []struct {
					client string
					msg    nrepl.Message
				}{
					{"Calva's", nrepl.Message{"symbol": s.Code, "context": 0, "enhanced-cljs-completion?": "t"}},
					{"Conjure's", nrepl.Message{"symbol": s.Code, "extra-metadata": []any{"arglists", "doc"}, "enhanced-cljs-completion?": "t"}},
				} {
					what := "complete with " + req.client + " symbol"
					resp := t.Request(c, fields.With(nrepl.Message{"op": "complete"}).With(req.msg))
					completionsWhere(t, what, resp, true)
					checkCandidates(t, what, resp, s.Value)
				}
			},
		},
	}
}

// completionsWhere makes sure the completions of a reply come with done,
// and in its first message if first.
func completionsWhere(t *check.T, what string, resp nrepl.Response, first bool) {
	if last := resp.Messages[len(resp.Messages)-1]; !last.Has("completions") {
		t.Violatef("%s: the completions didn't come with done: %s", what, last)
	}
	if first && !resp.Messages[0].Has("completions") {
		t.Violatef("%s: the completions didn't come in the first message: %s", what, resp.Messages[0])
	}
}

// checkCandidates makes sure each candidate of a reply is a dict with a
// candidate, and that want is among them.
func checkCandidates(t *check.T, what string, resp nrepl.Response, want string) {
	var candidates []string
	for _, c := range field[[]any](t, resp, "completions", what) {
		d, ok := c.(map[string]any)
		candidate, _ := d["candidate"].(string)
		if !ok || candidate == "" {
			t.Stopf("%s: a completion is %v, not a dict with a candidate", what, c)
		}
		candidates = append(candidates, candidate)
	}
	if !slices.Contains(candidates, want) {
		t.Violatef("%s: %q isn't among the candidates %q", what, want, candidates)
	}
}

// field returns a field of a reply, stopping the check when it's missing
// or isn't a T. what says what the reply was for.
func field[T any](t *check.T, resp nrepl.Response, key, what string) T {
	v := resp.Get(key)
	x, ok := v.(T)
	switch {
	case v == nil:
		t.Stopf("%s gave no %s", what, key)
	case !ok:
		t.Stopf("%s gave %s that is %s, not %s", what, key, typeName(v), typeName(x))
	}
	return x
}

// requireFallbackOp skips the check unless describe lists op, or when it
// lists preferred, which clients use instead.
func requireFallbackOp(t *check.T, op, preferred string) {
	if requireOp(t, op)[preferred] != nil {
		t.Skipf("clients use %s instead of %s, which the server has too", preferred, op)
	}
}

// ciderOp returns what CIDER calls op: cider/op, which newer versions of
// cider-nrepl list, or else op. It skips the check when the server has
// neither.
func ciderOp(t *check.T, op string) string {
	ops := describedOps(t, op)
	switch {
	case ops["cider/"+op] != nil:
		return "cider/" + op
	case ops[op] != nil:
		return op
	}
	t.Skipf("the server doesn't advertise %s or cider/%s", op, op)
	return ""
}

// toolingSession opens a connection and a session for the ops that look
// things up, and returns the fields clients send with them: the session,
// and the namespace the session's evals run in, if the server says.
func toolingSession(t *check.T) (*nrepl.Conn, nrepl.Message) {
	c := t.Connect()
	fields := t.Session(c)
	resp := t.Eval(c, t.Snippet("value").Code, fields)
	if ns := resp.Str("ns"); ns != "" {
		fields = fields.With(nrepl.Message{"ns": ns})
	}
	return c, fields
}
