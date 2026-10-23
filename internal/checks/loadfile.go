package checks

import (
	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/internal/profile"
	"github.com/nrepl/proof/nrepl"
)

func loadFileChecks() []*check.Check {
	return []*check.Check{
		{
			ID:       "load-file.value",
			Title:    "load-file returns the value of the last form",
			Severity: check.Fail,
			Why:      "CIDER marks a buffer as loaded and shows the result only when it gets a value, and Calva says there were no results without one.",
			Refs:     []check.Ref{ciderLoadFile, calvaLoadFile, nreplLoadFile},
			Snippets: []string{"value"},
			Requires: []string{"describe.reply", "describe.ops-dict", "session.clone"},
			Run: func(t *check.T) {
				s, resp := loadSnippet(t, "value")
				if vs := resp.Values(); len(vs) == 0 || vs[len(vs)-1] != s.Value {
					t.Violatef("loading a file with %q gave values %q, want %q last", s.Code, vs, s.Value)
				}
			},
		},
		{
			ID:       "load-file.error",
			Title:    "A load-file that throws says so in err, ex and eval-error",
			Severity: check.Fail,
			Why:      "Clients tell a failed load apart in different ways: CIDER by eval-error, Calva only by ex (and it reports the load as a success without it) and Conjure only shows err.",
			Refs:     []check.Ref{ciderLoadFileError, calvaLoadFileError, conjureLoadFile, nreplLoadFile},
			Snippets: []string{"throw"},
			Requires: []string{"describe.reply", "describe.ops-dict", "session.clone"},
			Run: func(t *check.T) {
				s, resp := loadSnippet(t, "throw")
				if !resp.HasStatus("eval-error") {
					t.Violatef("loading a file with %q gave status %v, without eval-error", s.Code, resp.Status())
				}
				if resp.Str("ex") == "" {
					t.Violatef("loading a file with %q gave no ex", s.Code)
				}
				if resp.Err() == "" {
					t.Violatef("loading a file with %q gave no err", s.Code)
				}
			},
		},
	}
}

// loadSnippet loads a file with a snippet's code in it in a new session,
// with the fields CIDER, Calva and Conjure all send. It skips the check
// unless the server has load-file.
func loadSnippet(t *check.T, name string) (profile.Snippet, nrepl.Response) {
	requireOp(t, "load-file")
	s := t.Snippet(name)
	c := t.Connect()
	session := t.Session(c)
	return s, t.Request(c, session.With(nrepl.Message{"op": "load-file", "file": s.Code,
		"file-path": "/home/user/project/proof", "file-name": "proof"}))
}
