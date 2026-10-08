package serve

import (
	"fmt"
	"slices"
	"strings"
)

// Scenario is something proof serve can do differently from nREPL on
// request: what some real server does, or what the network can do to the
// replies. Each one is taken from the compatibility matrix or from a
// server's own replies, never made up.
type Scenario struct {
	Name  string
	Title string
	// Who says who does it.
	Who string
}

// behavior is the set of scenarios a server runs with.
type behavior struct {
	splitOutput, emptyMessages, lastValue, noErr, errorWithDone bool
	noOpEcho, noCloseOp, noInterrupt, stringVersions            bool
	noSessionClosed, sharedState, socketSessions, anySession    bool
	nsFallback, nsError, eofError                               bool
	unsortedKeys, byteWrites, batchedWrites, hangUp             bool
	stdin                                                       stdinMode
}

// stdinMode is what reading input does.
type stdinMode int

const (
	// askForInput is what nREPL does: ask with need-input, then read what
	// the stdin op sends.
	askForInput stdinMode = iota
	// The other two are for servers without a stdin op.
	endOfInput
	throwOnRead
)

// scenario is a Scenario, the behavior it sets, and the profiles of the
// servers that do it.
type scenario struct {
	Scenario
	set     func(*behavior)
	servers []string
}

var catalog = []scenario{
	{Scenario{"split-output", "Output comes one character per message",
		"nREPL splits long output, and Basilisp sends println's newline on its own"},
		func(b *behavior) { b.splitOutput = true }, []string{"basilisp"}},
	{Scenario{"empty-messages", "Replies to eval include messages with nothing but id and session", "jank"},
		func(b *behavior) { b.emptyMessages = true }, []string{"jank"}},
	{Scenario{"last-value", "Only the value of the last form is sent", "Basilisp, jank, dialtone"},
		func(b *behavior) { b.lastValue = true }, []string{"basilisp", "jank", "dialtone"}},
	{Scenario{"no-err", "What the code prints to stderr never reaches the client", "Basilisp, dialtone, repartee"},
		func(b *behavior) { b.noErr = true }, []string{"basilisp", "dialtone", "repartee"}},
	{Scenario{"error-with-done", "eval-error comes in the same message as done", "jank"},
		func(b *behavior) { b.errorWithDone = true }, []string{"jank"}},
	{Scenario{"no-op-echo", "Replies to unknown ops don't say which op it was", "ClojureCLR, Basilisp, jank, dialtone, repartee"},
		func(b *behavior) { b.noOpEcho = true }, []string{"clojure-clr", "basilisp", "jank", "dialtone", "repartee"}},
	{Scenario{"no-close-op", "describe doesn't list close, even though close works", "jank"},
		func(b *behavior) { b.noCloseOp = true }, []string{"jank"}},
	{Scenario{"no-interrupt", "There's no interrupt op", "ClojureCLR, Basilisp, jank"},
		func(b *behavior) { b.noInterrupt = true }, []string{"clojure-clr", "basilisp", "jank"}},
	{Scenario{"no-stdin", "There's no stdin op, and reading input gets nil right away",
		"ClojureCLR and Basilisp started in the background, as they read their own stdin, where Basilisp gets an empty string"},
		func(b *behavior) { b.stdin = endOfInput }, []string{"clojure-clr", "basilisp"}},
	{Scenario{"read-line-throws", "There's no stdin op, and reading input throws", "jank"},
		func(b *behavior) { b.stdin = throwOnRead }, []string{"jank"}},
	{Scenario{"string-versions", "versions.proof is a plain string rather than a dict",
		"Babashka for versions.babashka, ClojureCLR for versions.clojure.tools.nrepl"},
		func(b *behavior) { b.stringVersions = true }, []string{"babashka", "clojure-clr"}},
	{Scenario{"no-session-closed", "close replies with done alone, without session-closed", "Basilisp, jank, dialtone, repartee"},
		func(b *behavior) { b.noSessionClosed = true }, []string{"basilisp", "jank", "dialtone", "repartee"}},
	{Scenario{"shared-state", "Sessions on the same connection share *1, *e and the current namespace", "ClojureCLR, Basilisp, jank"},
		func(b *behavior) { b.sharedState = true }, []string{"clojure-clr", "basilisp", "jank"}},
	{Scenario{"socket-sessions", "A session only exists on the connection that cloned it", "ClojureCLR, Basilisp, jank"},
		func(b *behavior) { b.socketSessions = true }, []string{"clojure-clr", "basilisp", "jank"}},
	{Scenario{"any-session", "Requests for sessions that don't exist run in a new session", "ClojureCLR, Basilisp, jank"},
		func(b *behavior) { b.anySession = true }, []string{"clojure-clr", "basilisp", "jank"}},
	{Scenario{"ns-fallback", "An eval in a namespace that doesn't exist runs in the current one", "jank"},
		func(b *behavior) { b.nsFallback = true }, []string{"jank"}},
	{Scenario{"ns-error", "An eval in a namespace that doesn't exist fails without namespace-not-found", "Basilisp"},
		func(b *behavior) { b.nsError = true }, []string{"basilisp"}},
	{Scenario{"eof-error", "Reading past the end of input fails instead of returning nil", "nREPL 1.7.0"},
		func(b *behavior) { b.eofError = true }, []string{"clojure"}},
	{Scenario{"unsorted-keys", "The keys of reply dicts aren't sorted", "jank"},
		func(b *behavior) { b.unsortedKeys = true }, []string{"jank"}},
	{Scenario{"byte-writes", "Replies are written a byte at a time", "any server, as TCP can deliver a message in pieces"},
		func(b *behavior) { b.byteWrites = true }, nil},
	{Scenario{"batched-writes", "Replies are held back and written together until the eval waits or ends", "any server, as TCP can deliver several messages together"},
		func(b *behavior) { b.batchedWrites = true }, nil},
	{Scenario{"hang-up", "The server closes the connection instead of answering an eval", "any server that crashes"},
		func(b *behavior) { b.hangUp = true }, nil},
}

// Scenarios lists every scenario.
func Scenarios() []Scenario {
	s := make([]Scenario, len(catalog))
	for i, c := range catalog {
		s[i] = c.Scenario
	}
	return s
}

// Preset is a real server as the scenarios that make proof serve behave
// like it. Name is the name of the server's profile.
type Preset struct {
	Name      string
	Scenarios []string
}

// servers are the profiles of the servers with scenarios.
var servers = []string{"clojure", "babashka", "clojure-clr", "basilisp", "jank", "dialtone", "repartee"}

// Presets lists every server proof serve can behave like.
func Presets() []Preset {
	presets := make([]Preset, len(servers))
	for i, name := range servers {
		presets[i].Name = name
		for _, c := range catalog {
			if slices.Contains(c.servers, name) {
				presets[i].Scenarios = append(presets[i].Scenarios, c.Name)
			}
		}
	}
	return presets
}

// Like returns the scenarios of the server whose profile has the given
// name, followed by the extra ones.
func Like(name string, extra []string) ([]string, error) {
	presets := Presets()
	i := slices.IndexFunc(presets, func(p Preset) bool { return p.Name == name })
	if i < 0 {
		return nil, fmt.Errorf("proof serve can't behave like %q (proof list shows the servers it can)", name)
	}
	return append(presets[i].Scenarios, extra...), nil
}

// conflicts are scenarios that can't be combined.
var conflicts = [][2]string{{"ns-fallback", "ns-error"}, {"byte-writes", "batched-writes"}, {"no-stdin", "read-line-throws"}}

func behave(names []string) (behavior, error) {
	var b behavior
	chosen := map[string]bool{}
	for _, name := range names {
		i := slices.IndexFunc(catalog, func(c scenario) bool { return c.Name == name })
		if i < 0 {
			return b, fmt.Errorf("unknown scenario %q (proof list shows them all)", name)
		}
		catalog[i].set(&b)
		chosen[name] = true
	}
	for _, pair := range conflicts {
		if chosen[pair[0]] && chosen[pair[1]] {
			return b, fmt.Errorf("scenarios %s don't go together", strings.Join(pair[:], " and "))
		}
	}
	return b, nil
}
