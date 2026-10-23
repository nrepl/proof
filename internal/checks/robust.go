package checks

import (
	"slices"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

// The robust checks run last, as they may take the server down.
func robustChecks() []*check.Check {
	return []*check.Check{
		{
			ID:       "robust.wrong-types",
			Title:    "A session keeps working after a request with fields of the wrong type",
			Severity: check.Warn,
			Why:      "No client we know of sends line as a string, but one with a bug (see client.field-types) shouldn't take its session down: nREPL never answers that eval or anything after it in the session.",
			Refs:     []check.Ref{issue("nrepl/nrepl", 477), nreplLineColumn},
			Snippets: []string{"value"},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				s := t.Snippet("value")
				c := t.Connect()
				session := t.Session(c)
				t.Eval(c, s.Code, session.With(nrepl.Message{"line": "proof", "column": []any{"proof"}}))
				if vs := t.Eval(c, s.Code, session).Values(); !slices.Equal(vs, []string{s.Value}) {
					t.Violatef("after an eval with line as a string, evaluating %q gave values %q, want [%q]", s.Code, vs, s.Value)
				}
			},
		},
		{
			ID:       "robust.sessions-at-once",
			Title:    "An eval in one session doesn't hold up the others",
			Severity: check.Fail,
			Why:      "Each client has sessions of its own (e.g. an editor and neorepl, connected to the same server), so a server that runs one eval at a time leaves every other client waiting for whatever the code in one of them does.",
			Refs:     []check.Ref{issue("jank-lang/jank", 1022), ciderClone},
			Snippets: []string{"sleep", "value"},
			Requires: []string{"interrupt.running"},
			Run: func(t *check.T) {
				sleeping := startSleep(t)
				defer sleeping.stop()
				if s, resp := evalSnippet(t, "value"); !slices.Equal(resp.Values(), []string{s.Value}) {
					t.Violatef("evaluating %q in another session while the sleep snippet ran gave values %q, want [%q]", s.Code, resp.Values(), s.Value)
				}
				sleeping.interrupt(t, sleeping.id)
			},
		},
		{
			ID:       "robust.disconnect",
			Title:    "A session keeps working after its client went away in the middle of an eval",
			Severity: check.Fail,
			Why:      "neorepl shares a session between its commands, so when one of them is killed in the middle of an eval, the session has to keep working for the next one, which otherwise waits forever.",
			Refs:     []check.Ref{issue("babashka/babashka", 2227), neoreplInterrupt},
			Snippets: []string{"sleep", "value"},
			Requires: []string{"interrupt.running"},
			Run: func(t *check.T) {
				v := t.Snippet("value")
				s := startSleep(t)
				s.c.Close()
				t.Settle()
				c := t.Connect()
				resp := t.Request(c, s.session.With(nrepl.Message{"op": "interrupt", "interrupt-id": s.id}))
				if resp.HasStatus("unknown-session") || resp.HasStatus("session-idle") || resp.HasStatus("error") {
					t.Skipf("the session can't be interrupted from another connection, which got status %v (see session.across-connections)", resp.Status())
				}
				if vs := t.Eval(c, v.Code, s.session).Values(); !slices.Equal(vs, []string{v.Value}) {
					t.Violatef("after its client went away, evaluating %q in the session gave values %q, want [%q]", v.Code, vs, v.Value)
				}
			},
		},
		{
			ID:       "robust.bad-frames",
			Title:    "Frames that aren't requests don't take the server down",
			Severity: check.Fail,
			Why:      "A server that goes down at something it can't read takes every client connected to it along, and users lose their REPLs and everything in them. Closing the connection that sent it is fine, as nREPL does.",
			Refs:     []check.Ref{nreplConnLoop},
			Snippets: []string{"value"},
			Requires: []string{"session.clone"},
			Run: func(t *check.T) {
				s := t.Snippet("value")
				// A list, a string, an integer, something that isn't bencode
				// and a dict whose string is cut short.
				for _, frame := range []string{"le", "5:proof", "i42e", "proof", "d2:op4:ev"} {
					if err := t.SendRaw([]byte(frame)); err != nil {
						t.Stopf("couldn't send %q: %v", frame, err)
					}
					t.Settle()
					c, err := t.TryConnect()
					if err != nil {
						t.Stopf("after a connection sent %q, the server took no more connections: %v", frame, err)
					}
					if vs := t.Eval(c, s.Code, t.Session(c)).Values(); !slices.Equal(vs, []string{s.Value}) {
						t.Stopf("after a connection sent %q, evaluating %q on another one gave values %q, want [%q]", frame, s.Code, vs, s.Value)
					}
				}
			},
		},
	}
}
