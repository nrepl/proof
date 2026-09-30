package checks

import (
	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

const bogusOp = "proof/no-such-op"

func opChecks() []*check.Check {
	return []*check.Check{
		{
			ID:       "op.unknown",
			Title:    "An unknown op gets unknown-op",
			Severity: check.Fail,
			Why:      "Calva recognises unsupported ops by the unknown-op status; without it the request looks like it succeeded.",
			Refs:     []check.Ref{calvaUnknownOp, specOptionalOps, nreplUnknownOp},
			Run: func(t *check.T) {
				c := t.Connect()
				resp := t.Request(c, nrepl.Message{"op": bogusOp})
				if !resp.HasStatus("unknown-op") {
					t.Violatef("reply status is %v, with no unknown-op", resp.Status())
				}
				if !resp.HasStatus("error") {
					t.Notef("no error flag next to unknown-op; nREPL sends one, but clients don't need it")
				}
			},
		},
		{
			ID:       "op.unknown-echo",
			Title:    "The unknown-op reply echoes the op",
			Severity: check.Warn,
			Why:      "Calva builds its \"server doesn't support this\" message from the op field in the reply.",
			Refs:     []check.Ref{calvaUnknownOp, nreplUnknownOp},
			Run: func(t *check.T) {
				c := t.Connect()
				resp := t.Request(c, nrepl.Message{"op": bogusOp})
				if resp.Str("op") != bogusOp {
					t.Violatef("no reply message carries op %q", bogusOp)
				}
			},
		},
	}
}
