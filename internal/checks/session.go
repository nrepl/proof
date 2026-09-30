package checks

import (
	"slices"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

const bogusSession = "proof-no-such-session"

func sessionChecks() []*check.Check {
	return []*check.Check{
		{
			ID:       "session.clone",
			Title:    "clone returns a new session",
			Severity: check.Fail,
			Why:      "CIDER clones two sessions while connecting and gives up if new-session is missing.",
			Refs:     []check.Ref{ciderClone, specClone},
			Run: func(t *check.T) {
				c := t.Connect()
				first := t.Request(c, nrepl.Message{"op": "clone"}).Str("new-session")
				if first == "" {
					t.Stopf("clone reply has no new-session")
				}
				if second := t.Request(c, nrepl.Message{"op": "clone"}).Str("new-session"); second == first {
					t.Violatef("two clones returned the same session %q", first)
				}
			},
		},
		{
			ID:       "session.persistent",
			Title:    "Definitions persist within a session",
			Severity: check.Fail,
			Why:      "Every REPL workflow depends on later evals seeing earlier definitions.",
			Refs:     []check.Ref{specClone},
			Snippets: []string{"define"},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				s := t.Snippet("define")
				c := t.Connect()
				session := t.Session(c)
				t.Eval(c, s.Code, session)
				resp := t.Eval(c, s.Use, session)
				if vs := resp.Values(); !slices.Equal(vs, []string{s.Value}) {
					t.Violatef("after evaluating %q, %q gave %q, want [%q]", s.Code, s.Use, vs, s.Value)
				}
			},
		},
		{
			ID:       "session.across-connections",
			Title:    "A session can be used from another connection",
			Severity: check.Warn,
			Why:      "Sessions belong to the server, not the socket; clients that reconnect expect their session to still be there.",
			Refs:     []check.Ref{issue("nrepl/nrepl", 183), issue("babashka/babashka.nrepl", 72)},
			Snippets: []string{"session-state"},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				s := t.Snippet("session-state")
				first := t.Connect()
				session := t.Session(first)
				t.Eval(first, s.Code, session)
				second := t.Connect()
				resp := t.Eval(second, s.Use, session)
				if vs := resp.Values(); !slices.Equal(vs, []string{s.Value}) {
					t.Violatef("on a second connection, %q gave %q (status %v), want [%q]", s.Use, vs, resp.Status(), s.Value)
				}
			},
		},
		{
			ID:       "session.isolated",
			Title:    "Sessions don't share state",
			Severity: check.Fail,
			Why:      "CIDER keeps a separate tooling session so its own evals don't clobber the user's *1, *2 and friends; shared state corrupts them.",
			Refs:     []check.Ref{ciderClone, issue("babashka/babashka.nrepl", 72)},
			Snippets: []string{"session-state"},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				s := t.Snippet("session-state")
				c := t.Connect()
				first := t.Session(c)
				t.Eval(c, s.Code, first)
				// Clone the second session only now, so it can't have
				// inherited the state.
				resp := t.Eval(c, s.Use, t.Session(c))
				if vs := resp.Values(); slices.Equal(vs, []string{s.Value}) {
					t.Violatef("a fresh session sees state set in another one: %q gave %q", s.Use, vs)
				}
			},
		},
		{
			ID:       "session.ephemeral",
			Title:    "Requests without a session work",
			Severity: check.Fail,
			Why:      "Calva's connection handshake starts with an eval that carries no session.",
			Refs:     []check.Ref{calvaHandshake},
			Snippets: []string{"value"},
			Run: func(t *check.T) {
				s := t.Snippet("value")
				c := t.Connect()
				resp := t.Eval(c, s.Code, nil)
				if vs := resp.Values(); !slices.Equal(vs, []string{s.Value}) {
					t.Violatef("evaluating %q without a session gave %q (status %v), want [%q]", s.Code, vs, resp.Status(), s.Value)
				}
			},
		},
		{
			ID:       "session.close",
			Title:    "close replies with session-closed",
			Severity: check.Fail,
			Why:      "vim-fireplace only forgets a session when it sees session-closed, and nREPL tells clients to check for it.",
			Refs:     []check.Ref{fireplaceClosed, nreplSessionClosed, specClose},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				c := t.Connect()
				session := t.Session(c)
				resp := t.Request(c, session.With(nrepl.Message{"op": "close"}))
				if !resp.HasStatus("session-closed") {
					t.Violatef("close reply status is %v, with no session-closed", resp.Status())
				}
			},
		},
		{
			ID:       "session.unknown",
			Title:    "A request for an unknown session gets unknown-session",
			Severity: check.Fail,
			Why:      "Conjure recovers from a stale session by watching for unknown-session; a server that quietly accepts the request leaves it using a session that doesn't exist.",
			Refs:     []check.Ref{conjureSession, nreplUnknownSess},
			Snippets: []string{"value"},
			Run: func(t *check.T) {
				s := t.Snippet("value")
				c := t.Connect()
				resp := t.Eval(c, s.Code, nrepl.Message{"session": bogusSession})
				if !resp.HasStatus("unknown-session") {
					t.Violatef("eval in session %q got status %v (values %q), with no unknown-session", bogusSession, resp.Status(), resp.Values())
				}
			},
		},
		{
			ID:       "session.closed",
			Title:    "A closed session is gone",
			Severity: check.Fail,
			Why:      "Conjure relies on unknown-session to notice a session has gone away.",
			Refs:     []check.Ref{conjureSession, nreplUnknownSess},
			Snippets: []string{"value"},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				s := t.Snippet("value")
				c := t.Connect()
				session := t.Session(c)
				t.Request(c, session.With(nrepl.Message{"op": "close"}))
				resp := t.Eval(c, s.Code, session)
				if !resp.HasStatus("unknown-session") {
					t.Violatef("eval in the closed session got status %v (values %q), with no unknown-session", resp.Status(), resp.Values())
				}
			},
		},
	}
}
