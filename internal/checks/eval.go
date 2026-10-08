package checks

import (
	"slices"
	"strings"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/internal/profile"
	"github.com/nrepl/proof/nrepl"
)

func evalChecks() []*check.Check {
	return []*check.Check{
		{
			ID:       "eval.value",
			Title:    "eval returns the value",
			Severity: check.Fail,
			Why:      "Every client shows the value messages as the result.",
			Refs:     []check.Ref{specEval},
			Snippets: []string{"value"},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				s, resp := evalSnippet(t, "value")
				if vs := resp.Values(); !slices.Equal(vs, []string{s.Value}) {
					t.Violatef("evaluating %q gave values %q, want [%q]", s.Code, vs, s.Value)
				}
			},
		},
		{
			ID:       "eval.stdout",
			Title:    "Printed output arrives as out before done",
			Severity: check.Fail,
			Why:      "Clients show out as the user's output; anything missing is lost.",
			Refs:     []check.Ref{specEval, issue("babashka/babashka.nrepl", 8)},
			Snippets: []string{"stdout"},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				s, resp := evalSnippet(t, "stdout")
				if got := resp.Out(); got != s.Out {
					t.Violatef("evaluating %q produced out %q, want %q", s.Code, got, s.Out)
				}
			},
		},
		{
			ID:       "eval.stdout-order",
			Title:    "Output arrives before the value",
			Severity: check.Warn,
			Why:      "Clients print messages in the order they arrive, so output that trails the value lands after the result.",
			Refs:     []check.Ref{specEval},
			Snippets: []string{"stdout"},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				_, resp := evalSnippet(t, "stdout")
				sawValue := false
				for _, m := range resp.Messages {
					if m.Has("out") && sawValue {
						t.Stopf("out arrived after the value: %s", m)
					}
					if m.Has("value") {
						sawValue = true
					}
				}
			},
		},
		{
			ID:       "eval.stderr",
			Title:    "Error output arrives as err",
			Severity: check.Fail,
			Why:      "Clients show err as the user's error output; anything missing is lost.",
			Refs:     []check.Ref{specEval, issue("babashka/babashka.nrepl", 28)},
			Snippets: []string{"stderr"},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				s, resp := evalSnippet(t, "stderr")
				if got := resp.Err(); got != s.Err {
					t.Violatef("evaluating %q produced err %q, want %q", s.Code, got, s.Err)
				}
			},
		},
		{
			ID:       "eval.error-status",
			Title:    "An eval that throws gets eval-error",
			Severity: check.Fail,
			Why:      "CIDER, Calva, REPLy and rebel-readline detect a failed evaluation by the eval-error status.",
			Refs:     []check.Ref{ciderEvalError, calvaEvalError, replyTerminal, nreplEvalError},
			Snippets: []string{"throw"},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				s, resp := evalSnippet(t, "throw")
				if !resp.HasStatus("eval-error") {
					t.Violatef("evaluating %q got status %v, with no eval-error", s.Code, resp.Status())
				}
				if vs := resp.Values(); len(vs) > 0 {
					t.Notef("the failed eval also returned values %q", vs)
				}
			},
		},
		{
			ID:       "eval.error-report",
			Title:    "A failed eval explains itself in err and ex",
			Severity: check.Warn,
			Why:      "err is what users see when an eval fails, as clients show it like any other error output, and ex is what nREPL sends to say what was thrown.",
			Refs:     []check.Ref{ciderStderr, nreplEvalError},
			Snippets: []string{"throw"},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				_, resp := evalSnippet(t, "throw")
				if resp.Err() == "" {
					t.Violatef("no err output, so the user sees no error message")
				}
				if resp.First("ex") == nil {
					t.Violatef("no ex field")
				} else {
					t.Notef("ex is %q (its format is up to the implementation)", resp.Str("ex"))
				}
				if strings.Contains(resp.Err(), "\x1b[") {
					t.Violatef("err contains ANSI escape codes, which show up raw in most clients")
				}
			},
		},
		{
			ID:       "eval.survives-error",
			Title:    "A session keeps working after an error",
			Severity: check.Fail,
			Why:      "Users hit errors constantly; a session that dies on one leaves every client stuck.",
			Refs:     []check.Ref{specEval, issue("babashka/nbb", 306)},
			Snippets: []string{"throw", "value"},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				thrower, s := t.Snippet("throw"), t.Snippet("value")
				c := t.Connect()
				session := t.Session(c)
				t.Eval(c, thrower.Code, session)
				resp := t.Eval(c, s.Code, session)
				if vs := resp.Values(); !slices.Equal(vs, []string{s.Value}) {
					t.Violatef("after the error, evaluating %q gave %q, want [%q]", s.Code, vs, s.Value)
				}
			},
		},
		{
			ID:       "eval.multiple-forms",
			Title:    "Each form in the code gets its own value",
			Severity: check.Fail,
			Why:      "Clients show one result per value message; a server that only returns the last one loses the rest.",
			Refs:     []check.Ref{issue("babashka/nbb", 294), issue("nrepl/nrepl", 147)},
			Snippets: []string{"multiple"},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				s, resp := evalSnippet(t, "multiple")
				if vs := resp.Values(); !slices.Equal(vs, s.Values) {
					t.Violatef("evaluating %q gave values %q, want %q", s.Code, vs, s.Values)
				}
			},
		},
		{
			ID:       "eval.no-code",
			Title:    "An eval without code gets no-code",
			Severity: check.Warn,
			Why:      "No client sends this on purpose, but the reference implementation answers with no-code and a server must still reply with done.",
			Refs:     []check.Ref{nreplNoCode},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				c := t.Connect()
				resp := t.Request(c, t.Session(c).With(nrepl.Message{"op": "eval"}))
				if !resp.HasStatus("no-code") {
					t.Violatef("reply status is %v, with no no-code", resp.Status())
				}
			},
		},
		{
			ID:       "eval.ns",
			Title:    "eval replies say which namespace they ran in",
			Severity: check.Warn,
			Why:      "CIDER and Calva update the REPL prompt from the ns field; without it the prompt goes stale.",
			Refs:     []check.Ref{ciderPayloadCond, issue("nrepl/nrepl", 171)},
			Snippets: []string{"value"},
			Needs:    []string{"namespaces"},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				_, resp := evalSnippet(t, "value")
				if resp.First("ns") == nil {
					t.Violatef("no message in the reply carries ns")
				} else {
					t.Notef("ns is %q", resp.Str("ns"))
				}
			},
		},
		{
			ID:       "eval.unknown-ns",
			Title:    "An eval in a namespace that doesn't exist gets namespace-not-found",
			Severity: check.Fail,
			Why:      "CIDER, Conjure and vim-fireplace report the missing namespace from this status; servers that fall back to another namespace run the code in the wrong place.",
			Refs:     []check.Ref{ciderNsNotFound, conjureNsMissing, fireplaceNsMissing, nreplNsNotFound, issue("babashka/babashka.nrepl", 45)},
			Snippets: []string{"value"},
			Needs:    []string{"namespaces"},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				s := t.Snippet("value")
				c := t.Connect()
				resp := t.Eval(c, s.Code, t.Session(c).With(nrepl.Message{"ns": "proof.no-such-namespace"}))
				if !resp.HasStatus("namespace-not-found") {
					t.Violatef("reply status is %v, with no namespace-not-found (values: %q)", resp.Status(), resp.Values())
				}
			},
		},
	}
}

// evalSnippet evaluates a snippet in a fresh session on a new connection.
func evalSnippet(t *check.T, name string) (profile.Snippet, nrepl.Response) {
	s := t.Snippet(name)
	c := t.Connect()
	return s, t.Eval(c, s.Code, t.Session(c))
}
