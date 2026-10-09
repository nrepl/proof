package serve

import (
	"fmt"
	"slices"
	"strings"
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

func loadProfile(t *testing.T, name string) *profile.Profile {
	t.Helper()
	p, err := profile.Load("../../profiles/" + name + ".toml")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// runChecks runs proof's server checks against proof serve, with the
// profile for nREPL itself.
func runChecks(t *testing.T, scenarios ...string) map[string]check.Result {
	t.Helper()
	return runChecksAs(t, "clojure", scenarios...)
}

// runChecksAs runs them with the snippets of nREPL's profile and the
// capabilities of server's, so the checks for other languages skip.
func runChecksAs(t *testing.T, server string, scenarios ...string) map[string]check.Result {
	t.Helper()
	p := loadProfile(t, "clojure")
	p.Capabilities = loadProfile(t, server).Capabilities
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
		// None of these servers has Java interop, but vim-fireplace would
		// run into it.
		{[]string{"last-value"}, map[string]check.Verdict{"eval.multiple-forms": F, "fireplace.connect": F}},
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
	}
	for _, c := range cases {
		t.Run(fmt.Sprint(c.scenarios), func(t *testing.T) {
			t.Parallel()
			checktest.Verdicts(t, runChecks(t, c.scenarios...), c.want)
		})
	}
}

// A server's scenarios together get its column of the compatibility
// matrix. The columns leave out eval.no-code, as no client sends an eval
// without code.
func TestPresetsGetTheColumnsOfTheirServers(t *testing.T) {
	F, W, S := check.Failed, check.Warned, check.Skipped
	noJava := map[string]check.Verdict{"fireplace.connect": S, "fireplace.eval": S}
	noLanguage := map[string]check.Verdict{"eval.ns": S, "eval.unknown-ns": S, "cider.eval": S, "calva.eval": S,
		"conjure.eval": S}
	columns := map[string]map[string]check.Verdict{
		"clojure":  nil,
		"babashka": nil,
		"clojure-clr": checktest.Merged(noJava, map[string]check.Verdict{"op.unknown-echo": W, "session.across-connections": W,
			"session.isolated": F, "session.unknown": F, "session.closed": F, "stdin.need-input": S,
			"stdin.roundtrip": S, "stdin.eof": S}),
		"basilisp": checktest.Merged(noJava, map[string]check.Verdict{"op.unknown-echo": W, "session.across-connections": W,
			"session.isolated": F, "session.close": F, "session.unknown": F, "session.closed": F, "eval.stderr": F,
			"eval.multiple-forms": F, "eval.unknown-ns": F, "stdin.need-input": S, "stdin.roundtrip": S, "stdin.eof": S}),
		"jank": checktest.Merged(noJava, map[string]check.Verdict{"describe.required-ops": F, "op.unknown-echo": W,
			"session.across-connections": W, "session.isolated": F, "session.close": F, "session.unknown": F,
			"session.closed": F, "eval.multiple-forms": F, "eval.unknown-ns": F, "stdin.need-input": S,
			"stdin.roundtrip": S, "stdin.eof": S, "wire.canonical": W}),
		"dialtone": checktest.Merged(noJava, noLanguage, map[string]check.Verdict{"op.unknown-echo": W, "session.close": F,
			"eval.stderr": F, "eval.multiple-forms": F}),
		"repartee": checktest.Merged(noJava, noLanguage, map[string]check.Verdict{"op.unknown-echo": W, "session.close": F,
			"eval.stderr": F}),
	}
	for name, want := range columns {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			scenarios, err := Like(name, nil)
			if err != nil {
				t.Fatal(err)
			}
			checktest.Verdicts(t, runChecksAs(t, name, scenarios...), want)
		})
	}
	for _, p := range Presets() {
		if _, ok := columns[p.Name]; !ok {
			t.Errorf("no column for %s", p.Name)
		}
	}
}

// Each server is named by its profile, and the scenarios it has say they're
// what it does.
func TestPresetsMatchTheScenarios(t *testing.T) {
	for _, p := range Presets() {
		prof, err := profile.Load("../../profiles/" + p.Name + ".toml")
		if err != nil {
			t.Errorf("%s isn't the name of a profile: %v", p.Name, err)
			continue
		}
		// e.g. "ClojureCLR (clr.tools.nrepl 0.1.2-alpha2)" says ClojureCLR,
		// but the profile of nREPL itself is named after Clojure.
		server, _, _ := strings.Cut(prof.Name, " (")
		if p.Name == "clojure" {
			server = "nREPL 1.8.0"
		}
		for _, s := range Scenarios() {
			if slices.Contains(p.Scenarios, s.Name) && !strings.Contains(s.Who, server) {
				t.Errorf("%s has %s, which doesn't say %s does it", p.Name, s.Name, server)
			}
		}
	}
}
