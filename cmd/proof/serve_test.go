package main

import (
	"strings"
	"testing"
)

func TestServeExitCodes(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		frames []string
		code   int
		output string
	}{
		{"well-behaved client", nil, []string{"d2:id1:14:code7:(+ 1 2)2:op4:evale"}, 0, "client traffic to proof serve"},
		{"with scenarios", []string{"last-value", "no-err"}, []string{"d2:id1:12:op8:describee"}, 0,
			"client traffic to proof serve (last-value, no-err)"},
		{"request without an id", nil, []string{"d2:op8:describee"}, 1, "a request without an id"},
		{"no client", nil, nil, 3, "nothing to check"},
		{"unknown scenario", []string{"no-such-scenario"}, nil, 2, `unknown scenario "no-such-scenario"`},
		{"like a server", []string{"-like", "jank", "byte-writes"}, []string{"d2:id1:12:op8:describee"}, 0,
			"client traffic to proof serve (like jank, byte-writes)"},
		{"like an unknown server", []string{"-like", "no-such-server"}, nil, 2, `can't behave like "no-such-server"`},
		{"like a server, by its profile", []string{"-like", "profiles/jank.toml"}, []string{"d2:id1:12:op8:describee"}, 0,
			"client traffic to proof serve (like jank)"},
		{"like a server, but not quite", []string{"-like", "jank", "ns-error"}, nil, 2,
			"scenarios ns-fallback and ns-error don't go together"},
		{"flag after the scenarios", []string{"byte-writes", "-like", "jank"}, nil, 2, "-like comes after a scenario, but flags have to go first"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := runWithClient(t, serveUntil, c.frames, c.args...)
			if code != c.code {
				t.Errorf("exit status %d, want %d\nstdout:\n%s\nstderr:\n%s", code, c.code, stdout, stderr)
			}
			if !strings.Contains(stdout+stderr, c.output) {
				t.Errorf("output doesn't mention %q\nstdout:\n%s\nstderr:\n%s", c.output, stdout, stderr)
			}
		})
	}
}
