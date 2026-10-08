package checks

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

// A recording is a client profile proof can read, whose checks send the
// same requests, in sessions of their own.
func TestRecordingsAreClientProfiles(t *testing.T) {
	sent := func(m nrepl.Message) nrepl.Event { return nrepl.Event{Dir: nrepl.Sent, Msg: m} }
	got := func(m nrepl.Message) nrepl.Event { return nrepl.Event{Dir: nrepl.Received, Msg: m} }
	done := []any{"done"}
	eval := nrepl.Message{"id": "4", "op": "eval", "session": "4f1c", "code": "value",
		"file": "a \"file\"\nwith\ta \x01 and \xff", "line": int64(3), "\xfe": "", "nrepl.middleware.print/stream?": []any{},
		"nrepl.middleware.print/options": map[string]any{"right-margin": int64(70), "": []any{"x", int64(1)}}}
	traffic := []check.Traffic{
		{Label: "connection 1", Events: []nrepl.Event{
			sent(nrepl.Message{"id": "1", "op": "clone", "client-name": "Test"}),
			got(nrepl.Message{"id": "1", "new-session": "4f1c", "status": done}),
			// The same session again is a new one for proof.
			sent(nrepl.Message{"id": "2", "op": "clone"}),
			got(nrepl.Message{"id": "2", "new-session": "4f1c", "status": done}),
			sent(nrepl.Message{"id": "3", "op": "describe", "session": "4f1c"}),
			sent(eval),
			got(nrepl.Message{"id": "3", "ops": map[string]any{}, "status": done}),
			got(nrepl.Message{"id": "4", "value": "3", "session": "4f1c"}),
			{Dir: nrepl.Sent, Closed: true},
		}},
		// Frames that aren't dicts aren't requests.
		{Label: "connection 2", Events: []nrepl.Event{{Dir: nrepl.Sent, Data: []any{}}}},
		// Sessions outlive connections, and only sessions stand for them.
		{Label: "connection 3", Events: []nrepl.Event{
			sent(nrepl.Message{"id": "1", "op": "eval", "session": "4f1c", "code": "$s1"}),
		}},
	}
	recorded := RecordedProfile(traffic)
	p, err := readClientProfile(fstest.MapFS{"test.toml": {Data: []byte(recorded)}}, "test.toml")
	if err != nil {
		t.Fatalf("%v\n%s", err, recorded)
	}
	if !strings.Contains(recorded, "\nsend = { op = \"clone\", client-name = \"Test\" }\n") {
		t.Errorf("the clone isn't on one line, starting with the op:\n%s", recorded)
	}
	if p.Name != "Test" || len(p.Checks) != 2 || p.Checks[0].ID != "connection-1" || p.Checks[1].ID != "connection-3" {
		t.Fatalf("got %+v from\n%s", p, recorded)
	}
	wantEval := map[string]any{"op": "eval", "session": "$s2", "code": "value", "file": "a \"file\"\nwith\ta \x01 and \ufffd",
		"line": int64(3), "\ufffd": "", "nrepl.middleware.print/stream?": []any{},
		"nrepl.middleware.print/options": map[string]any{"right-margin": int64(70), "": []any{"x", int64(1)}}}
	want := [][]clientStep{{
		{Send: request{"op": "clone", "client-name": "Test"}, NewSession: "s1"},
		{Send: request{"op": "clone"}, NewSession: "s2"},
		{Send: request{"op": "describe", "session": "$s2"}},
		{Send: request(wantEval)},
	}, {
		{Send: request{"op": "eval", "session": "$s2", "code": "$s1"}},
	}}
	for i, c := range p.Checks {
		if !reflect.DeepEqual(c.Steps, want[i]) {
			t.Errorf("%s: got steps\n%#v\nwant\n%#v\nfrom\n%s", c.ID, c.Steps, want[i], recorded)
		}
	}
	c := p.Checks[0]
	r := runFakeChecks(t, quirks{}, []*check.Check{{ID: c.ID, Title: c.Title, Run: c.replay}})[c.ID]
	if r.Verdict != check.Pass {
		t.Errorf("replaying it got %s %v", r.Verdict, r.Details)
	}
}
