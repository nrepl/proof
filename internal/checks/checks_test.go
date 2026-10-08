package checks

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/internal/check/checktest"
	"github.com/nrepl/proof/internal/profile"
	"github.com/nrepl/proof/nrepl"
)

// The fake server understands these made-up forms; see fakeServer.eval.
var fakeProfile = &profile.Profile{
	Name:         "fake",
	Timeout:      time.Second,
	Capabilities: map[string]bool{"namespaces": true, "clojure": true},
	Snippets: map[string]profile.Snippet{
		"value":         {Code: "value", Value: "3"},
		"stdout":        {Code: "stdout", Out: "proof"},
		"stderr":        {Code: "stderr", Err: "proof"},
		"throw":         {Code: "throw"},
		"define":        {Code: "define", Use: "use", Value: "42"},
		"session-state": {Code: "marker", Use: "*1", Value: ":proof-marker"},
		"multiple":      {Code: "1 2", Values: []string{"1", "2"}},
		"read-line":     {Code: "read", Value: `"proof"`, Eof: "nil"},
	},
}

func runFake(t *testing.T, q quirks) map[string]check.Result {
	t.Helper()
	return runFakeChecks(t, q, All(), WireRules()...)
}

func runFakeChecks(t *testing.T, q quirks, checks []*check.Check, rules ...*check.Rule) map[string]check.Result {
	t.Helper()
	env := &check.Env{Profile: fakeProfile, Addr: startFake(t, q), Settle: 20 * time.Millisecond}
	return checktest.ByID(check.Run(env, checks, rules))
}

func TestWellBehavedServerPassesEverything(t *testing.T) {
	checktest.Verdicts(t, runFake(t, quirks{}), nil)
}

// Each misbehaviour must produce exactly the listed verdicts, and every
// other check and rule must still pass.
func TestChecksCatchMisbehaviour(t *testing.T) {
	F, W, S := check.Failed, check.Warned, check.Skipped
	cases := []struct {
		name string
		q    quirks
		want map[string]check.Verdict
	}{
		{"ops as a list", quirks{opsList: true}, map[string]check.Verdict{"describe.ops-dict": F, "describe.required-ops": S,
			"stdin.need-input": S, "stdin.roundtrip": S, "stdin.eof": S, "cider.connect": F}},
		{"clone not advertised", quirks{noClone: true}, map[string]check.Verdict{"describe.required-ops": F}},
		{"no versions", quirks{noVersions: true}, map[string]check.Verdict{"describe.versions": W}},
		{"describe kills the connection", quirks{crashOnDescribe: true}, map[string]check.Verdict{
			"describe.reply": F, "describe.ops-dict": S, "describe.required-ops": S, "describe.versions": S,
			"stdin.need-input": S, "stdin.roundtrip": S, "stdin.eof": S, "cider.connect": F}},
		{"no unknown-op", quirks{noUnknownOp: true}, map[string]check.Verdict{"op.unknown": F, "op.unknown-echo": W}},
		{"no op echo", quirks{noOpEcho: true}, map[string]check.Verdict{"op.unknown-echo": W}},
		{"status is a string", quirks{statusString: true}, map[string]check.Verdict{
			"op.unknown": F, "op.unknown-echo": F, "wire.status-type": F}},
		{"no session-closed", quirks{noSessionClosed: true}, map[string]check.Verdict{"session.close": F}},
		// Only clients put dicts and lists in their requests.
		{"flat fields only", quirks{flatFields: true}, map[string]check.Verdict{
			"cider.connect": F, "cider.eval": F, "cider.repl": F}},
		{"any session accepted", quirks{acceptAnySession: true}, map[string]check.Verdict{"session.unknown": F, "session.closed": F}},
		{"shared session state", quirks{sharedState: true}, map[string]check.Verdict{"session.isolated": F}},
		{"sessions tied to sockets", quirks{socketSessions: true}, map[string]check.Verdict{"session.across-connections": W}},
		{"no ephemeral sessions", quirks{noEphemeral: true}, map[string]check.Verdict{"session.ephemeral": F}},
		{"last value only", quirks{lastValueOnly: true}, map[string]check.Verdict{"eval.multiple-forms": F}},
		{"stderr dropped", quirks{dropErr: true}, map[string]check.Verdict{"eval.stderr": F}},
		{"output after value", quirks{outAfterValue: true}, map[string]check.Verdict{"eval.stdout-order": W}},
		{"no eval-error", quirks{noEvalError: true}, map[string]check.Verdict{"eval.error-status": F}},
		{"no ex", quirks{noEx: true}, map[string]check.Verdict{"eval.error-report": W}},
		{"no no-code", quirks{noNoCode: true}, map[string]check.Verdict{"eval.no-code": W}},
		{"no ns", quirks{noNs: true}, map[string]check.Verdict{"eval.ns": W}},
		{"ns fallback", quirks{nsFallback: true}, map[string]check.Verdict{"eval.unknown-ns": F}},
		{"two dones", quirks{twoDones: true}, map[string]check.Verdict{"wire.one-done": W}},
		{"value after done", quirks{valueAfterDone: true}, map[string]check.Verdict{"wire.after-done": W, "wire.error-terminal": F}},
		{"out and value together", quirks{payloadTogether: true}, map[string]check.Verdict{"wire.one-payload": F}},
		{"output after eval-error", quirks{errorThenOut: true}, map[string]check.Verdict{"wire.error-terminal": F}},
		{"session not echoed", quirks{noSessionEcho: true}, map[string]check.Verdict{"wire.session-echo": F}},
		{"output without id", quirks{noID: true}, map[string]check.Verdict{
			"wire.id": W, "eval.stdout": F, "eval.stderr": F, "eval.error-report": W}},
		{"integer value", quirks{intValue: true}, map[string]check.Verdict{
			"wire.field-types": F, "eval.value": F, "eval.survives-error": F, "eval.multiple-forms": F,
			"session.ephemeral": F, "session.persistent": F, "cider.eval": F, "cider.repl": F}},
		{"unsorted keys", quirks{unsortedKeys: true}, map[string]check.Verdict{"wire.canonical": W}},
		{"invalid UTF-8", quirks{badUTF8: true}, map[string]check.Verdict{"wire.utf8": W, "eval.stdout": F}},
		{"no stdin op", quirks{noStdinOp: true}, map[string]check.Verdict{
			"stdin.need-input": S, "stdin.roundtrip": S, "stdin.eof": S}},
		{"never asks for input", quirks{noNeedInput: true}, map[string]check.Verdict{
			"stdin.need-input": F, "stdin.roundtrip": S, "stdin.eof": S}},
		{"need-input with done", quirks{needInputDone: true}, map[string]check.Verdict{
			"wire.need-input-alone": F, "wire.one-done": W, "wire.after-done": W,
			"stdin.roundtrip": F, "stdin.eof": W}},
		{"EOF is an error", quirks{eofError: true}, map[string]check.Verdict{"stdin.eof": W}},
		{"input never arrives", quirks{dropStdin: true}, map[string]check.Verdict{"stdin.roundtrip": F, "stdin.eof": W}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			checktest.Verdicts(t, runFake(t, c.q), c.want)
		})
	}
}

// When clone is broken, session.clone fails once and everything needing a
// session skips straight away rather than each waiting out the timeout.
func TestBrokenCloneSkipsSessionChecksQuickly(t *testing.T) {
	start := time.Now()
	results := runFake(t, quirks{cloneHangs: true})
	// One timeout for session.clone itself, and nothing more.
	if elapsed := time.Since(start); elapsed > 1800*time.Millisecond {
		t.Errorf("run took %s; checks needing clone should have been skipped", elapsed)
	}
	if v := results["session.clone"].Verdict; v != check.Failed {
		t.Errorf("session.clone: %s", v)
	}
	for _, id := range []string{"session.persistent", "session.isolated", "session.close", "eval.value", "eval.unknown-ns"} {
		r := results[id]
		if r.Verdict != check.Skipped || !strings.Contains(strings.Join(r.Details, " "), "session.clone") {
			t.Errorf("%s: got %s %v, want a skip pointing at session.clone", id, r.Verdict, r.Details)
		}
	}
	for _, id := range []string{"session.ephemeral", "session.unknown", "describe.reply"} {
		if v := results[id].Verdict; v != check.Pass {
			t.Errorf("%s doesn't need clone and should pass, got %s", id, v)
		}
	}
}

// nREPL goes on with the next form after one throws, so what the next form
// sends isn't late.
func TestOnlyTheFormThatThrewIsOver(t *testing.T) {
	// Only Clojure code can be told apart into forms.
	for code, want := range map[string]check.Verdict{`(/ 1 0) (println "proof")`: check.Pass, "(/ 1 0)": check.Failed,
		`raise "proof"`: check.Failed} {
		events := []nrepl.Event{
			{Dir: nrepl.Sent, Msg: nrepl.Message{"id": "1", "op": "eval", "code": code}},
			{Dir: nrepl.Received, Msg: nrepl.Message{"id": "1", "status": []any{"eval-error"}}},
			{Dir: nrepl.Received, Msg: nrepl.Message{"id": "1", "out": "proof"}},
			{Dir: nrepl.Received, Msg: nrepl.Message{"id": "1", "status": []any{"done"}}},
		}
		results := checktest.ByID(check.Grade(WireRules(), []check.Traffic{{Label: "test", Events: events}}))
		if got := results["wire.error-terminal"].Verdict; got != want {
			t.Errorf("%q: got %s, want %s", code, got, want)
		}
	}
}

func TestForms(t *testing.T) {
	cases := map[string][]string{
		"value":                       {"value"},
		"1 2\n":                       {"1", "2"},
		`(f "a ) b" [1 2]) {:a 1}`:    {`(f "a ) b" [1 2])`, "{:a 1}"},
		`(str "\"" ")") x`:            {`(str "\"" ")")`, "x"},
		"1, 2 ; (3 \"\n4":             {"1", "2", "4"},
		`(= c \() (a)(b) x;c` + "\ny": {`(= c \()`, "(a)", "(b)", "x", "y"},
		"":                            nil,
	}
	for code, want := range cases {
		if got := forms(code); !slices.Equal(got, want) {
			t.Errorf("forms(%q) = %q, want %q", code, got, want)
		}
	}
}
