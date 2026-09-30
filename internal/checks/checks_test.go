package checks

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/internal/profile"
)

// The fake server understands these made-up forms; see fakeServer.eval.
var fakeProfile = &profile.Profile{
	Name:         "fake",
	Timeout:      time.Second,
	Capabilities: map[string]bool{"namespaces": true},
	Snippets: map[string]profile.Snippet{
		"value":         {Code: "value", Value: "3"},
		"stdout":        {Code: "stdout", Out: "proof"},
		"stderr":        {Code: "stderr", Err: "proof"},
		"throw":         {Code: "throw"},
		"define":        {Code: "define", Use: "use", Value: "42"},
		"session-state": {Code: "marker", Use: "*1", Value: ":proof-marker"},
		"multiple":      {Code: "1 2", Values: []string{"1", "2"}},
	},
}

func runFake(t *testing.T, q quirks) map[string]check.Result {
	t.Helper()
	env := &check.Env{Profile: fakeProfile, Addr: startFake(t, q), Settle: 20 * time.Millisecond}
	results := map[string]check.Result{}
	for _, r := range check.Run(env, All(), WireRules()) {
		results[r.ID] = r
	}
	return results
}

func TestWellBehavedServerPassesEverything(t *testing.T) {
	for id, r := range runFake(t, quirks{}) {
		if r.Verdict != check.Pass {
			t.Errorf("%s: %s %v", id, r.Verdict, r.Details)
		}
	}
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
		{"ops as a list", quirks{opsList: true}, map[string]check.Verdict{"describe.ops-dict": F, "describe.required-ops": S}},
		{"clone not advertised", quirks{noClone: true}, map[string]check.Verdict{"describe.required-ops": F}},
		{"no versions", quirks{noVersions: true}, map[string]check.Verdict{"describe.versions": W}},
		{"describe kills the connection", quirks{crashOnDescribe: true}, map[string]check.Verdict{
			"describe.reply": F, "describe.ops-dict": S, "describe.required-ops": S, "describe.versions": S}},
		{"no unknown-op", quirks{noUnknownOp: true}, map[string]check.Verdict{"op.unknown": F, "op.unknown-echo": W}},
		{"no op echo", quirks{noOpEcho: true}, map[string]check.Verdict{"op.unknown-echo": W}},
		{"status is a string", quirks{statusString: true}, map[string]check.Verdict{
			"op.unknown": F, "op.unknown-echo": F, "wire.status-type": F}},
		{"no session-closed", quirks{noSessionClosed: true}, map[string]check.Verdict{"session.close": F}},
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
			"session.ephemeral": F, "session.persistent": F}},
		{"unsorted keys", quirks{unsortedKeys: true}, map[string]check.Verdict{"wire.canonical": W}},
		{"invalid UTF-8", quirks{badUTF8: true}, map[string]check.Verdict{"wire.utf8": W, "eval.stdout": F}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			results := runFake(t, c.q)
			var ids []string
			for id := range results {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				r := results[id]
				want, listed := c.want[id]
				if !listed {
					want = check.Pass
				}
				if r.Verdict != want {
					t.Errorf("%s: got %s, want %s %v", id, r.Verdict, want, r.Details)
				}
			}
			for id := range c.want {
				if _, ok := results[id]; !ok {
					t.Errorf("%s: no such check", id)
				}
			}
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
