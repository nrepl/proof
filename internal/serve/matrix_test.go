package serve

import (
	"fmt"
	"testing"
	"time"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/internal/check/checktest"
	"github.com/nrepl/proof/internal/checks"
	"github.com/nrepl/proof/internal/profile"
)

// serveFor starts a server with the given scenarios, stopped when the test
// ends.
func serveFor(t *testing.T, scenarios ...string) *Server {
	t.Helper()
	s, err := Listen("127.0.0.1:0", "0.1.0-dev", scenarios)
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	t.Cleanup(func() { s.Stop(0) })
	return s
}

// runChecks runs proof's server checks against proof serve, with the
// snippets of the profile for nREPL itself.
func runChecks(t *testing.T, scenarios ...string) map[string]check.Result {
	t.Helper()
	p, err := profile.Load("../../profiles/clojure.toml")
	if err != nil {
		t.Fatal(err)
	}
	p.Timeout = time.Second
	env := &check.Env{Profile: p, Addr: serveFor(t, scenarios...).Addr(), Settle: 20 * time.Millisecond}
	return checktest.ByID(check.Run(env, checks.All(), checks.WireRules()))
}

func TestPassesEveryServerCheck(t *testing.T) {
	checktest.Verdicts(t, runChecks(t), nil)
}

// A scenario has to do what the servers it names do, so it gets the
// verdicts they get in the compatibility matrix.
func TestScenariosGetTheVerdictsOfTheirServers(t *testing.T) {
	F, W, S := check.Failed, check.Warned, check.Skipped
	noStdin := map[string]check.Verdict{"stdin.need-input": S, "stdin.roundtrip": S, "stdin.eof": S}
	cases := []struct {
		scenarios []string
		want      map[string]check.Verdict
	}{
		{[]string{"split-output"}, nil},
		{[]string{"empty-messages"}, nil},
		{[]string{"last-value"}, map[string]check.Verdict{"eval.multiple-forms": F}},
		{[]string{"no-err"}, map[string]check.Verdict{"eval.stderr": F}},
		{[]string{"error-with-done"}, nil},
		{[]string{"no-op-echo"}, map[string]check.Verdict{"op.unknown-echo": W}},
		{[]string{"no-close-op"}, map[string]check.Verdict{"describe.required-ops": F}},
		{[]string{"no-interrupt"}, nil},
		{[]string{"no-stdin"}, noStdin},
		{[]string{"read-line-throws"}, noStdin},
		{[]string{"string-versions"}, nil},
		{[]string{"no-session-closed"}, map[string]check.Verdict{"session.close": F}},
		{[]string{"shared-state"}, map[string]check.Verdict{"session.isolated": F}},
		{[]string{"socket-sessions"}, map[string]check.Verdict{"session.across-connections": W}},
		{[]string{"any-session"}, map[string]check.Verdict{"session.unknown": F, "session.closed": F}},
		{[]string{"ns-fallback"}, map[string]check.Verdict{"eval.unknown-ns": F}},
		{[]string{"ns-error"}, map[string]check.Verdict{"eval.unknown-ns": F}},
		{[]string{"eof-error"}, map[string]check.Verdict{"stdin.eof": W}},
		{[]string{"unsorted-keys"}, map[string]check.Verdict{"wire.canonical": W}},
		{[]string{"byte-writes"}, nil},
		{[]string{"batched-writes"}, nil},
		// The columns of whole servers, except for eval.no-code, which no
		// scenario covers as no client sends an eval without code.
		{[]string{"no-op-echo", "socket-sessions", "shared-state", "no-session-closed", "any-session", "no-err",
			"last-value", "ns-error", "no-stdin", "no-interrupt", "split-output"}, // Basilisp
			map[string]check.Verdict{"op.unknown-echo": W, "session.across-connections": W, "session.isolated": F,
				"session.close": F, "session.unknown": F, "session.closed": F, "eval.stderr": F,
				"eval.multiple-forms": F, "eval.unknown-ns": F, "stdin.need-input": S, "stdin.roundtrip": S, "stdin.eof": S}},
		{[]string{"no-close-op", "no-op-echo", "socket-sessions", "shared-state", "no-session-closed", "any-session",
			"last-value", "ns-fallback", "read-line-throws", "no-interrupt", "unsorted-keys", "empty-messages", "error-with-done"}, // jank
			map[string]check.Verdict{"describe.required-ops": F, "op.unknown-echo": W, "session.across-connections": W,
				"session.isolated": F, "session.close": F, "session.unknown": F, "session.closed": F,
				"eval.multiple-forms": F, "eval.unknown-ns": F, "stdin.need-input": S, "stdin.roundtrip": S,
				"stdin.eof": S, "wire.canonical": W}},
	}
	for _, c := range cases {
		t.Run(fmt.Sprint(c.scenarios), func(t *testing.T) {
			t.Parallel()
			checktest.Verdicts(t, runChecks(t, c.scenarios...), c.want)
		})
	}
}
