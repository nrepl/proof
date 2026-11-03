package checks

import (
	"regexp"
	"slices"
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
	// The links in refs.go to the same clients, which is how a check knows
	// the clients it breaks.
	bases := map[string]string{"CIDER": ciderBase, "Calva": calvaSrc, "Conjure": conjureSrc, "vim-fireplace": fireplaceBase,
		"REPLy": strings.TrimSuffix(replyBase, "eval_modes/nrepl.clj"), "neorepl": neoreplBase}
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
				if (s.NewSession != "" || s.Snippet != "" || s.Values > 0 || len(s.Dicts) > 0) && s.Why == "" {
					t.Errorf("%s step %d: doesn't say what happens to the client without what it needs", c.ID, i+1)
				}
				if name, ok := s.Send["session"].(string); ok && strings.HasPrefix(name, "$") && !sessions[name[1:]] {
					t.Errorf("%s step %d: uses %s before a step gets it", c.ID, i+1, name)
				}
				if code, _ := s.Send["code"].(string); s.Values > len(forms(code)) {
					t.Errorf("%s step %d: wants %d values from %d forms", c.ID, i+1, s.Values, len(forms(code)))
				}
				if s.NewSession != "" {
					sessions[s.NewSession] = true
				}
			}
		}
	}
}

// A check breaks the clients whose code it links to, or all of them when
// it links to none, unless it's only a warning. Every client has checks
// of its own.
func TestChecksNameTheClientsTheyBreak(t *testing.T) {
	var all []string
	for _, p := range clientProfiles() {
		all = append(all, p.Name)
	}
	clients := map[string][]string{}
	var everyClient []string
	note := func(id string, severity check.Severity, refs []check.Ref) {
		clients[id] = ClientsOf(severity, refs)
		if slices.Equal(clients[id], all) {
			everyClient = append(everyClient, id)
		}
	}
	for _, c := range All() {
		note(c.ID, c.Severity, c.Refs)
	}
	for _, r := range WireRules() {
		note(r.ID, r.Severity, r.Refs)
	}
	for id, want := range map[string][]string{
		"cider.connect":     {"CIDER"},
		"eval.error-status": {"Calva", "CIDER", "REPLy"},
		// rebel-readline has no client profile.
		"wire.error-terminal": {"REPLy"},
		"describe.versions":   nil,
	} {
		if !slices.Equal(clients[id], want) {
			t.Errorf("%s breaks %v, not %v", id, clients[id], want)
		}
	}
	// A check gets here by linking to no client's code, which is also what
	// happens when it links to a client without a profile, so each one has
	// to be listed.
	want := []string{"session.persistent", "eval.value", "eval.stdout", "eval.stderr", "eval.survives-error",
		"eval.multiple-forms", "robust.bad-frames", "wire.bencode", "wire.dict"}
	if !slices.Equal(everyClient, want) {
		t.Errorf("the checks that break every client are %v, not %v", everyClient, want)
	}
	for _, p := range clientProfiles() {
		for _, c := range p.Checks {
			if !slices.Equal(clients[c.ID], []string{p.Name}) {
				t.Errorf("%s breaks %v, not just %s", c.ID, clients[c.ID], p.Name)
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

func TestValuesApart(t *testing.T) {
	cases := []struct {
		replies []nrepl.Message
		want    int
	}{
		{[]nrepl.Message{{"value": "1", "ns": "user"}, {"value": "2", "ns": "user"}}, 2},
		// An ns on its own ends the value before it.
		{[]nrepl.Message{{"value": "1"}, {"ns": "user"}, {"value": "2"}}, 2},
		{[]nrepl.Message{{"value": "1"}, {"value": "2"}}, 1},
		{[]nrepl.Message{{"ns": "user"}, {"status": []any{"done"}}}, 0},
	}
	for _, c := range cases {
		if got := valuesApart(nrepl.Response{Messages: c.replies}); got != c.want {
			t.Errorf("%v: got %d, want %d", c.replies, got, c.want)
		}
	}
}
