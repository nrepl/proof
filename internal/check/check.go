// Package check defines compatibility checks and runs them against a
// server.
//
// Grading follows one rule. A check fails when a named client breaks, or
// users lose data, if the server behaves otherwise, and it cites the
// client code that depends on the behaviour. It warns when the server
// differs from the reference implementation or the draft spec but no
// known client depends on it. Anything implementation-defined is reported
// as a note and never affects the verdict.
package check

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nrepl/proof/internal/profile"
	"github.com/nrepl/proof/nrepl"
)

// Severity is what a violated rule costs.
type Severity int

const (
	Warn Severity = iota
	Fail
)

func (s Severity) String() string {
	if s == Fail {
		return "fail"
	}
	return "warn"
}

// verdict is what breaking a rule of this severity earns.
func (s Severity) verdict() Verdict {
	if s == Fail {
		return Failed
	}
	return Warned
}

// Ref points at the spec, reference implementation, client code or issue
// a rule comes from.
type Ref struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Check is one rule exercised by sending requests.
type Check struct {
	ID    string
	Title string
	// Severity applies when the rule is violated. A request that never
	// gets "done" is always a failure, whatever the severity.
	Severity Severity
	// Why says who breaks, in a sentence.
	Why  string
	Refs []Ref
	// Snippets and Needs name the profile snippets and capabilities the
	// check requires; without them it's skipped.
	Snippets []string
	Needs    []string
	// Requires names checks this one builds on. When one of them ran
	// earlier and didn't pass (or warn), this check is skipped, since its
	// result would only repeat that failure.
	Requires []string
	Run      func(t *T)
}

// Verdict is the outcome of a check or wire rule.
type Verdict string

const (
	Pass    Verdict = "pass"
	Warned  Verdict = "warn"
	Failed  Verdict = "fail"
	Skipped Verdict = "skip"
	// Errored means the harness couldn't run the check (e.g. it couldn't
	// connect). It says nothing about the server's behaviour.
	Errored Verdict = "error"
)

// Env is what checks run against.
type Env struct {
	Profile *profile.Profile
	Addr    string
	// Settle is how long a connection must stay quiet after a check before
	// it's closed, so late messages get graded. Defaults to 100ms.
	Settle time.Duration
}

// T is handed to a running check.
type T struct {
	env   *Env
	check *Check

	conns      []*nrepl.Conn
	violations []string
	notes      []string
	hung       bool
	skip       string
	harnessErr string
}

// exit stops the check's goroutine; the runner reads the outcome off t.
func (t *T) exit() {
	runtime.Goexit()
}

// Snippet returns one of the profile snippets the check declared. The
// runner has already skipped the check if the profile lacks any of them.
func (t *T) Snippet(name string) profile.Snippet {
	if !slices.Contains(t.check.Snippets, name) {
		panic(fmt.Sprintf("check %s uses snippet %q without declaring it", t.check.ID, name))
	}
	return t.env.Profile.Snippets[name]
}

// Connect opens a new connection, stopping the check if it can't.
func (t *T) Connect() *nrepl.Conn {
	c, err := nrepl.Dial(t.env.Addr, 5*time.Second)
	if err != nil {
		t.harnessErr = fmt.Sprintf("couldn't connect to %s: %v", t.env.Addr, err)
		t.exit()
	}
	t.conns = append(t.conns, c)
	return c
}

// Request sends m and waits for "done". A request that never finishes
// fails the check outright, since every client waits for "done".
func (t *T) Request(c *nrepl.Conn, m nrepl.Message) nrepl.Response {
	resp, err := c.Request(m, t.env.Profile.Timeout)
	switch {
	case err == nil:
	case errors.Is(err, nrepl.ErrTimeout):
		t.hangf("%s request got no reply with \"done\" within %s", m.Str("op"), t.env.Profile.Timeout)
	default:
		t.hangf("%s request never finished: %v", m.Str("op"), err)
	}
	return resp
}

// Send writes a request without waiting for its reply, returning its id.
// Pair it with Collect, for requests that need other requests sent while
// they're still running (stdin, interrupt).
func (t *T) Send(c *nrepl.Conn, m nrepl.Message) string {
	id, err := c.Send(m)
	if err != nil {
		t.hangf("%s request couldn't be sent: %v", m.Str("op"), err)
	}
	return id
}

// Collect waits for the "done" of a request sent with Send.
func (t *T) Collect(c *nrepl.Conn, id string) nrepl.Response {
	resp, err := c.Collect(id, t.env.Profile.Timeout)
	if err != nil {
		t.hangf("request %s never finished: %v", id, err)
	}
	return resp
}

// Await waits for a message matching pred, failing the check if none
// arrives in time. what describes the message for the report.
func (t *T) Await(c *nrepl.Conn, what string, pred func(nrepl.Message) bool) nrepl.Message {
	m, err := c.WaitFor(pred, t.env.Profile.Timeout)
	if err != nil {
		t.hangf("no %s within %s: %v", what, t.env.Profile.Timeout, err)
	}
	return m
}

// Eval evaluates code, with optional extra request fields.
func (t *T) Eval(c *nrepl.Conn, code string, extra nrepl.Message) nrepl.Response {
	return t.Request(c, nrepl.Message{"op": "eval", "code": code}.With(extra))
}

// Session clones a session and returns it as request fields, ready to
// pass to Eval or merge into a request with With. If clone doesn't hand
// back a session, the check is skipped; session.clone reports that.
func (t *T) Session(c *nrepl.Conn) nrepl.Message {
	s := t.Request(c, nrepl.Message{"op": "clone"}).Str("new-session")
	if s == "" {
		t.Skipf("needs a working clone (see session.clone)")
	}
	return nrepl.Message{"session": s}
}

func (t *T) hangf(format string, args ...any) {
	t.hung = true
	t.violations = append(t.violations, fmt.Sprintf(format, args...))
	t.exit()
}

// Violatef records that the rule was broken and carries on.
func (t *T) Violatef(format string, args ...any) {
	t.violations = append(t.violations, fmt.Sprintf(format, args...))
}

// Stopf records that the rule was broken and stops the check.
func (t *T) Stopf(format string, args ...any) {
	t.Violatef(format, args...)
	t.exit()
}

// Notef records something worth knowing that doesn't affect the verdict.
func (t *T) Notef(format string, args ...any) {
	t.notes = append(t.notes, fmt.Sprintf(format, args...))
}

// Skipf stops the check without a verdict on the server.
func (t *T) Skipf(format string, args ...any) {
	t.skip = fmt.Sprintf(format, args...)
	t.exit()
}

// Result is the outcome of one check or wire rule.
type Result struct {
	ID       string
	Title    string
	Severity Severity
	Why      string
	Refs     []Ref
	Verdict  Verdict
	// Details explain a non-passing verdict.
	Details []string
	Notes   []string
	// Transcripts of the connections the check opened, kept for
	// non-passing checks.
	Transcripts [][]nrepl.Event
	Duration    time.Duration
	// Expected holds the reason from the profile's expected failures when
	// the check failed as expected.
	Expected string
}

// Run runs checks one at a time, then grades the wire traffic they
// produced against the wire rules.
func Run(env *Env, checks []*Check, rules []*Rule) []Result {
	settle := env.Settle
	if settle == 0 {
		settle = 100 * time.Millisecond
	}
	results := make([]Result, len(checks))
	conns := make([][]*nrepl.Conn, len(checks))
	verdicts := map[string]Verdict{}
	// Connections wait for late messages (a second "done", output after
	// "done") in the background, so the next check doesn't wait on them.
	var settling sync.WaitGroup
	for i, c := range checks {
		results[i], conns[i] = runOne(env, c, verdicts)
		verdicts[c.ID] = results[i].Verdict
		for _, conn := range conns[i] {
			settling.Add(1)
			go func(conn *nrepl.Conn) {
				defer settling.Done()
				conn.Settle(settle, 10*settle)
				conn.Close()
			}(conn)
		}
	}
	settling.Wait()

	var traffic []Traffic
	for i, c := range checks {
		var transcripts [][]nrepl.Event
		for _, conn := range conns[i] {
			tr := conn.Transcript()
			transcripts = append(transcripts, tr)
			traffic = append(traffic, Traffic{Check: c.ID, Events: tr})
		}
		if v := results[i].Verdict; v != Pass && v != Skipped {
			results[i].Transcripts = transcripts
		}
	}
	for _, rule := range rules {
		results = append(results, rule.grade(traffic))
	}
	return results
}

func runOne(env *Env, c *Check, earlier map[string]Verdict) (Result, []*nrepl.Conn) {
	res := Result{ID: c.ID, Title: c.Title, Severity: c.Severity, Why: c.Why, Refs: c.Refs}
	if missing := missingRequirements(env.Profile, c, earlier); missing != "" {
		res.Verdict = Skipped
		res.Details = []string{missing}
		return res, nil
	}
	t := &T{env: env, check: c}
	start := time.Now()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		c.Run(t)
	}()
	if p := <-done; p != nil {
		t.harnessErr = fmt.Sprintf("check panicked: %v", p)
	}
	res.Duration = time.Since(start)

	res.Notes = t.notes
	switch {
	case t.harnessErr != "":
		res.Verdict = Errored
		res.Details = []string{t.harnessErr}
	case t.hung:
		res.Verdict = Failed
		res.Details = t.violations
	case len(t.violations) > 0:
		res.Verdict = c.Severity.verdict()
		res.Details = t.violations
	case t.skip != "":
		res.Verdict = Skipped
		res.Details = []string{t.skip}
	default:
		res.Verdict = Pass
	}
	return res, t.conns
}

func missingRequirements(p *profile.Profile, c *Check, earlier map[string]Verdict) string {
	for _, id := range c.Requires {
		if v, ran := earlier[id]; ran && v != Pass && v != Warned {
			if v == Skipped {
				return fmt.Sprintf("needs %s, which was skipped", id)
			}
			return fmt.Sprintf("needs %s, which didn't pass", id)
		}
	}
	var missing []string
	for _, s := range c.Snippets {
		if _, ok := p.Snippets[s]; !ok {
			missing = append(missing, fmt.Sprintf("snippet %q", s))
		}
	}
	for _, n := range c.Needs {
		if !p.Capabilities[n] {
			missing = append(missing, fmt.Sprintf("capability %q", n))
		}
	}
	if len(missing) == 0 {
		return ""
	}
	sort.Strings(missing)
	return "profile doesn't declare " + strings.Join(missing, ", ")
}
