package checks

import (
	"slices"
	"strings"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

func interruptChecks() []*check.Check {
	return []*check.Check{
		{
			ID:       "interrupt.running",
			Title:    "interrupt stops a running eval",
			Severity: check.Fail,
			Why:      "An interrupted eval has to end with done: CIDER shows the REPL's next prompt and stops its spinner only then, and Calva's result for it never settles without it.",
			Refs:     []check.Ref{ciderReplPrompt, ciderSpinner, calvaInterrupted, specInterrupt, nreplInterrupt},
			Snippets: []string{"sleep"},
			Requires: []string{"describe.reply", "describe.ops-dict", "session.clone"},
			Run: func(t *check.T) {
				s := startSleep(t)
				defer s.stop()
				s.interrupt(t, s.id)
			},
		},
		{
			ID:       "interrupt.no-id",
			Title:    "An interrupt without interrupt-id stops what's running",
			Severity: check.Fail,
			Why:      "Conjure never sends interrupt-id, so with a server that needs one the eval goes on, while Conjure says it was interrupted, and every later eval in the session waits for it.",
			Refs:     []check.Ref{conjureInterrupt, specInterrupt, nreplInterrupt},
			Snippets: []string{"sleep"},
			Requires: []string{"interrupt.running"},
			Run: func(t *check.T) {
				s := startSleep(t)
				defer s.stop()
				s.interrupt(t, "")
			},
		},
		{
			ID:       "interrupt.status",
			Title:    "An interrupted eval says it was interrupted",
			Severity: check.Warn,
			Why:      "CIDER tells the user an eval was interrupted when its done comes with interrupted; without it the eval just ends without a result.",
			Refs:     []check.Ref{ciderInterrupted, specInterrupt, nreplInterrupt},
			Snippets: []string{"sleep"},
			Requires: []string{"interrupt.running"},
			Run: func(t *check.T) {
				s := startSleep(t)
				defer s.stop()
				if resp := s.interrupt(t, s.id); !resp.HasStatus("interrupted") {
					t.Violatef("the interrupted eval ended with status %v, without interrupted", resp.Status())
				}
			},
		},
		{
			ID:       "interrupt.survives",
			Title:    "A session keeps working after an interrupt",
			Severity: check.Fail,
			Why:      "Clients go on evaluating in the session they interrupted (vim-fireplace blocks Vim until each eval is done), so a session that stops answering takes the REPL with it.",
			Refs:     []check.Ref{fireplaceInterrupt, nreplInterrupt},
			Snippets: []string{"sleep", "value"},
			Requires: []string{"interrupt.running"},
			Run: func(t *check.T) {
				v := t.Snippet("value")
				s := startSleep(t)
				defer s.stop()
				s.interrupt(t, s.id)
				if vs := t.Eval(s.c, v.Code, s.session).Values(); !slices.Equal(vs, []string{v.Value}) {
					t.Violatef("after the interrupt, evaluating %q gave values %q, want [%q]", v.Code, vs, v.Value)
				}
			},
		},
		{
			ID:       "interrupt.queued",
			Title:    "An eval waiting for an interrupted one still runs",
			Severity: check.Fail,
			Why:      "CIDER sends evals while another one is running in the session, and keeps the spinner of each going until its done; an eval that's dropped along with the interrupted one never gets it.",
			Refs:     []check.Ref{ciderSpinner, ciderInterrupt, issue("nrepl/nrepl", 503)},
			Snippets: []string{"sleep", "value"},
			Requires: []string{"interrupt.running"},
			Run: func(t *check.T) {
				v := t.Snippet("value")
				s := startSleep(t)
				defer s.stop()
				next := t.Send(s.c, s.session.With(nrepl.Message{"op": "eval", "code": v.Code}))
				s.interrupt(t, s.id)
				// A session runs its evals in order, so once an eval sent
				// after the interrupt is done, the waiting one has run too,
				// unless it was dropped.
				t.Eval(s.c, v.Code, s.session)
				resp, err := s.c.Collect(next, 0)
				if err != nil {
					t.Stopf("the eval that was waiting never got its done, though an eval sent after the interrupt did")
				}
				if vs := resp.Values(); !slices.Equal(vs, []string{v.Value}) {
					t.Violatef("evaluating %q after the interrupted eval gave values %q, want [%q]", v.Code, vs, v.Value)
				}
			},
		},
		{
			ID:       "interrupt.other-connection",
			Title:    "An interrupted eval ends on its own connection",
			Severity: check.Fail,
			Why:      "neorepl interrupt stops an eval that another neorepl process started, and that process waits for the eval's done on its own connection forever if it goes anywhere else.",
			Refs:     []check.Ref{neoreplInterrupt, issue("nrepl/nrepl", 498)},
			Snippets: []string{"sleep"},
			Requires: []string{"interrupt.running"},
			Run: func(t *check.T) {
				s := startSleep(t)
				defer s.stop()
				other := t.Connect()
				resp := t.Request(other, s.session.With(nrepl.Message{"op": "interrupt", "interrupt-id": s.id}))
				if resp.HasStatus("unknown-session") || resp.HasStatus("session-idle") || resp.HasStatus("error") {
					t.Skipf("the session can't be interrupted from another connection, which got status %v (see session.across-connections)", resp.Status())
				}
				// The eval's done comes before the interrupt's, wherever it goes.
				if _, err := other.Collect(s.id, 0); err == nil {
					t.Stopf("the interrupted eval's done went to the connection the interrupt came from")
				}
				s.await(t)
			},
		},
		{
			ID:       "interrupt.idle",
			Title:    "Interrupting an idle session gets session-idle",
			Severity: check.Warn,
			Why:      "That's how nREPL says nothing was running. CIDER and Calva interrupt every eval they still know about, so they get it a lot, but they don't need it.",
			Refs:     []check.Ref{nreplInterrupt, ciderInterrupt},
			Requires: []string{"describe.reply", "describe.ops-dict", "session.clone"},
			Run: func(t *check.T) {
				requireOp(t, "interrupt")
				c := t.Connect()
				session := t.Session(c)
				if resp := t.Request(c, session.With(nrepl.Message{"op": "interrupt"})); !resp.HasStatus("session-idle") {
					t.Violatef("interrupting an idle session gave status %v, without session-idle", resp.Status())
				}
			},
		},
		{
			ID:       "interrupt.id-mismatch",
			Title:    "An interrupt for another eval gets interrupt-id-mismatch",
			Severity: check.Warn,
			Why:      "An interrupt with the id of another eval mustn't stop the one that's running, and nREPL says so with interrupt-id-mismatch. No client looks at it, though.",
			Refs:     []check.Ref{nreplInterrupt},
			Snippets: []string{"sleep"},
			Requires: []string{"interrupt.running"},
			Run: func(t *check.T) {
				s := startSleep(t)
				defer s.stop()
				resp := t.Request(s.c, s.session.With(nrepl.Message{"op": "interrupt", "interrupt-id": "proof-no-such-eval"}))
				if !resp.HasStatus("interrupt-id-mismatch") {
					t.Violatef("interrupting an eval with another id gave status %v, without interrupt-id-mismatch", resp.Status())
				}
				s.interrupt(t, s.id)
			},
		},
	}
}

// sleep is the sleep snippet running in a session of its own.
type sleep struct {
	c       *nrepl.Conn
	session nrepl.Message
	id      string
	over    bool
}

// startSleep starts the sleep snippet and waits until it's running, i.e.
// until it has printed its output. It skips the check unless the server
// has interrupt.
func startSleep(t *check.T) *sleep {
	requireOp(t, "interrupt")
	c, session, id := startEval(t, "sleep")
	want := t.Snippet("sleep").Out
	var out string
	m := t.Await(c, "output from the sleep snippet", func(m nrepl.Message) bool {
		// Output without an id is wire.id's business.
		if m.Str("id") == id || !m.Has("id") {
			out += m.Str("out")
		}
		return strings.Contains(out, want) || m.Str("id") == id && m.HasStatus("done")
	})
	if !strings.Contains(out, want) {
		t.Skipf("the sleep snippet ended before it printed %q (status %v), so there was nothing to interrupt", want, m.Status())
	}
	return &sleep{c: c, session: session, id: id}
}

// interrupt sends an interrupt for the given eval id ("" for none) and
// returns what the sleep got once it's done.
func (s *sleep) interrupt(t *check.T, id string) nrepl.Response {
	req := s.session.With(nrepl.Message{"op": "interrupt"})
	if id != "" {
		req["interrupt-id"] = id
	}
	t.Request(s.c, req)
	return s.await(t)
}

// await waits for the sleep's done.
func (s *sleep) await(t *check.T) nrepl.Response {
	resp := t.Collect(s.c, s.id)
	s.over = true
	return resp
}

// stop interrupts the sleep if the check ended without stopping it, so
// the server isn't left running it.
func (s *sleep) stop() {
	if !s.over {
		s.c.Send(s.session.With(nrepl.Message{"op": "interrupt", "interrupt-id": s.id}))
	}
}
