package checks

import (
	"regexp"
	"strings"
	"testing"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

// The client profiles are part of proof, so they have to make sense
// before they ever reach a server.
func TestClientProfiles(t *testing.T) {
	ids := map[string]bool{}
	for _, c := range All() {
		if ids[c.ID] {
			t.Errorf("two checks are called %s", c.ID)
		}
		ids[c.ID] = true
	}
	pinned := regexp.MustCompile(`^https://github\.com/[^/]+/[^/]+/blob/[0-9a-f]{40}/`)
	// The links in refs.go to the same clients.
	bases := map[string]string{"CIDER": ciderBase}
	for _, p := range clientProfiles() {
		if p.Name == "" || !pinned.MatchString(p.Code) {
			t.Errorf("%q needs a name and links pinned to a commit, got %q", p.Name, p.Code)
		}
		if base, ok := bases[p.Name]; ok && p.Code != base {
			t.Errorf("%s links to %s, but refs.go to %s", p.Name, p.Code, base)
		}
		for _, c := range p.Checks {
			if c.ID == "" || c.Title == "" || c.Why == "" || len(c.Steps) == 0 {
				t.Errorf("%s: a check needs an id, a title, a why and steps", p.Name)
			}
			sessions := map[string]bool{}
			for i, s := range c.Steps {
				if s.Send["op"] == nil || len(s.Refs) == 0 {
					t.Errorf("%s step %d: needs an op and a link to the client's code", c.ID, i+1)
				}
				if (s.NewSession != "" || s.Snippet != "" || len(s.Dicts) > 0) && s.Why == "" {
					t.Errorf("%s step %d: doesn't say what happens to the client without what it needs", c.ID, i+1)
				}
				for _, v := range s.Send {
					if name, ok := v.(string); ok && strings.HasPrefix(name, "$") && !sessions[name[1:]] {
						t.Errorf("%s step %d: uses %s before a step gets it", c.ID, i+1, name)
					}
				}
				if s.NewSession != "" {
					sessions[s.NewSession] = true
				}
			}
		}
	}
}

// Replies the client can live with still get a note when they say
// something went wrong.
func TestClientChecksNoteErrors(t *testing.T) {
	c := clientCheck{ID: "test.errors", Title: "test", Why: "test", Steps: []clientStep{
		{Send: map[string]any{"op": "clone"}, NewSession: "s", Why: "nothing works"},
		{Send: map[string]any{"op": "eval", "code": "throw", "session": "$s"}},
	}}
	r := runFakeChecks(t, quirks{}, []*check.Check{{ID: c.ID, Title: c.Title, Run: c.replay}})[c.ID]
	if r.Verdict != check.Pass || len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "step 2 (eval) got status [eval-error done]") {
		t.Errorf("got %s with notes %q", r.Verdict, r.Notes)
	}
}

func TestNonDictField(t *testing.T) {
	cases := []struct {
		path  []string
		reply nrepl.Message
		bad   bool
	}{
		{[]string{"versions"}, nrepl.Message{"versions": map[string]any{}}, false},
		{[]string{"versions"}, nrepl.Message{}, false},
		// Clients decode an empty list and an empty dict the same way.
		{[]string{"aux"}, nrepl.Message{"aux": []any{}}, false},
		{[]string{"aux"}, nrepl.Message{"aux": []any{"current-ns"}}, true},
		{[]string{"versions", "clojure"}, nrepl.Message{"versions": map[string]any{"clojure": map[string]any{}}}, false},
		{[]string{"versions", "clojure"}, nrepl.Message{"versions": map[string]any{"clojure": "1.12.6"}}, true},
		{[]string{"versions", "clojure"}, nrepl.Message{"versions": map[string]any{}}, false},
		// That's for versions itself to report.
		{[]string{"versions", "clojure"}, nrepl.Message{"versions": "1.12.6"}, false},
	}
	for _, c := range cases {
		if got := nonDictField(nrepl.Response{Messages: []nrepl.Message{c.reply}}, c.path); (got != nil) != c.bad {
			t.Errorf("%v in %v: got %v", c.path, c.reply, got)
		}
	}
}
