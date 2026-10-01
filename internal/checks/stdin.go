package checks

import (
	"slices"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

func stdinChecks() []*check.Check {
	return []*check.Check{
		{
			ID:       "stdin.need-input",
			Title:    "Code reading stdin makes the server ask for input",
			Severity: check.Fail,
			Why:      "CIDER and Calva only prompt for input when they see need-input; without it, code reading stdin just hangs.",
			Refs:     []check.Ref{ciderNeedInput, calvaNeedInput, specStdin},
			Snippets: []string{"read-line"},
			Requires: []string{"describe.reply", "describe.ops-dict", "session.clone"},
			Run: func(t *check.T) {
				requireOp(t, "stdin")
				c, session, id := startRead(t)
				t.Await(c, "need-input for the eval", needInput(id))
				t.Request(c, session.With(nrepl.Message{"op": "stdin", "stdin": "proof\n"}))
				t.Collect(c, id)
			},
		},
		{
			ID:       "stdin.roundtrip",
			Title:    "Input sent with stdin reaches the code reading it",
			Severity: check.Fail,
			Why:      "Users answer prompts through the stdin op; if the input never arrives, programs that read input can't be used from the REPL.",
			Refs:     []check.Ref{specStdin, ciderNeedInput},
			Snippets: []string{"read-line"},
			Requires: []string{"stdin.need-input"},
			Run: func(t *check.T) {
				requireOp(t, "stdin")
				s := t.Snippet("read-line")
				c, session, id := startRead(t)
				t.Await(c, "need-input for the eval", needInput(id))
				t.Request(c, session.With(nrepl.Message{"op": "stdin", "stdin": "proof\n"}))
				resp := t.Collect(c, id)
				if vs := resp.Values(); !slices.Equal(vs, []string{s.Value}) {
					t.Violatef("after sending \"proof\\n\", evaluating %q gave %q (status %v), want [%q]", s.Code, vs, resp.Status(), s.Value)
				}
			},
		},
		{
			ID:       "stdin.eof",
			Title:    "An empty stdin means end of input",
			Severity: check.Warn,
			Why:      "nREPL treats an empty stdin as end of input, so code reading until EOF finishes instead of failing or waiting forever.",
			Refs:     []check.Ref{nreplStdinEOF, specStdin},
			Snippets: []string{"read-line"},
			Requires: []string{"stdin.need-input"},
			Run: func(t *check.T) {
				requireOp(t, "stdin")
				s := t.Snippet("read-line")
				if s.Eof == "" {
					t.Skipf("the read-line snippet has no eof value")
				}
				c, session, id := startRead(t)
				t.Await(c, "need-input for the eval", needInput(id))
				t.Request(c, session.With(nrepl.Message{"op": "stdin", "stdin": ""}))
				resp := t.Collect(c, id)
				if vs := resp.Values(); !slices.Equal(vs, []string{s.Eof}) {
					t.Violatef("after an empty stdin, evaluating %q gave %q (status %v, err %q), want [%q]",
						s.Code, vs, resp.Status(), resp.Err(), s.Eof)
				}
			},
		},
	}
}

// startRead evaluates the read-line snippet in a fresh session without
// waiting for it, since it blocks until it gets input.
func startRead(t *check.T) (*nrepl.Conn, nrepl.Message, string) {
	c := t.Connect()
	session := t.Session(c)
	id := t.Send(c, session.With(nrepl.Message{"op": "eval", "code": t.Snippet("read-line").Code}))
	return c, session, id
}

func needInput(id string) func(nrepl.Message) bool {
	return func(m nrepl.Message) bool { return m.Str("id") == id && m.HasStatus("need-input") }
}

// requireOp skips the check unless describe lists op.
func requireOp(t *check.T, op string) {
	ops, ok := describe(t).Get("ops").(map[string]any)
	if !ok {
		t.Skipf("can't tell whether the server supports %s (see describe.ops-dict)", op)
	}
	if _, ok := ops[op]; !ok {
		t.Skipf("the server doesn't advertise %s", op)
	}
}
