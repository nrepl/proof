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
