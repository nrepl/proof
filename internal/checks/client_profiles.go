package checks

import (
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

// Client profiles live in clients/, one per client. Each of their checks
// sends the requests a client sends in some situation (e.g. while
// connecting), the way the client sends them, and fails at the first
// reply the client couldn't use.
//
//go:embed clients/*.toml
var clientFiles embed.FS

type clientProfile struct {
	Name string `toml:"name"`
	// Code is where links to the client's code start, at the commit the
	// profile was written against.
	Code   string        `toml:"code"`
	Checks []clientCheck `toml:"checks"`
}

type clientCheck struct {
	ID    string `toml:"id"`
	Title string `toml:"title"`
	Why   string `toml:"why"`
	// Needs are the capabilities the server's profile has to declare,
	// e.g. "clojure" for checks that send Clojure code.
	Needs []string     `toml:"needs"`
	Steps []clientStep `toml:"steps"`
}

// clientStep is a request and what the client needs from its reply,
// besides done.
type clientStep struct {
	// Send is the request. A session starting with $ stands for the one
	// an earlier step got.
	Send request `toml:"send"`
	// Snippet names a snippet of the server's profile whose code goes in
	// the request, and whose value the reply has to have. It stands for
	// the user's code.
	Snippet string `toml:"snippet"`
	// NewSession names the session the reply has to hand back.
	NewSession string `toml:"new-session"`
	// Values is how many values the reply has to have at least, told apart
	// the way some clients do it: by the ns that comes with each one, or
	// after it.
	Values int `toml:"values"`
	// Dicts are fields that have to be dicts if the reply has them, each
	// a path of keys (e.g. ["versions", "clojure"]). An empty list will do
	// too, as clients can't tell the two apart.
	Dicts [][]string `toml:"dicts"`
	// Why says what happens in the client when the reply doesn't have
	// what the step needs, and Refs point at the code that needs it.
	Why  string   `toml:"why"`
	Refs []string `toml:"refs"`
}

// request is a request as it is, whatever fields the client puts in it.
type request map[string]any

func (r *request) UnmarshalTOML(data any) error {
	m, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("a request has to be a table, not %T", data)
	}
	*r = m
	return nil
}

// clientProfiles reads the client profiles, which are part of proof, so a
// broken one is a bug the tests catch.
var clientProfiles = sync.OnceValue(func() []clientProfile {
	files, err := clientFiles.ReadDir("clients")
	if err != nil {
		panic(err)
	}
	var profiles []clientProfile
	for _, f := range files {
		p, err := readClientProfile(clientFiles, "clients/"+f.Name())
		if err != nil {
			panic(err)
		}
		profiles = append(profiles, p)
	}
	return profiles
})

func readClientProfile(fsys fs.FS, path string) (clientProfile, error) {
	var p clientProfile
	md, err := toml.DecodeFS(fsys, path, &p)
	if err != nil {
		return p, fmt.Errorf("client profile %s: %v", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return p, fmt.Errorf("client profile %s: unknown keys %v", path, undecoded)
	}
	return p, nil
}

func clientProfileChecks() []*check.Check {
	var checks []*check.Check
	for _, p := range clientProfiles() {
		for _, c := range p.Checks {
			cc := &check.Check{ID: c.ID, Title: c.Title, Severity: check.Fail, Why: c.Why, Needs: c.Needs, Run: c.replay}
			for _, s := range c.Steps {
				for _, path := range s.Refs {
					if r := ref(p.Name+" "+path, p.Code+path); !slices.Contains(cc.Refs, r) {
						cc.Refs = append(cc.Refs, r)
					}
				}
				if s.Snippet != "" && !slices.Contains(cc.Snippets, s.Snippet) {
					cc.Snippets = append(cc.Snippets, s.Snippet)
				}
				// Like every check that clones, so a broken clone doesn't
				// make each of them wait for it. Other failures (e.g. of
				// describe) still show, as they're what the client runs into.
				if s.NewSession != "" {
					cc.Requires = []string{"session.clone"}
				}
			}
			checks = append(checks, cc)
		}
	}
	return checks
}

func (c clientCheck) replay(t *check.T) {
	conn := t.Connect()
	sessions := map[string]string{}
	for i, s := range c.Steps {
		req := nrepl.Message{}
		for k, v := range s.Send {
			req[k] = v
		}
		if name, ok := req["session"].(string); ok && strings.HasPrefix(name, "$") {
			req["session"] = sessions[name[1:]]
		}
		var want string
		if s.Snippet != "" {
			sn := t.Snippet(s.Snippet)
			req["code"], want = sn.Code, sn.Value
		}
		step := fmt.Sprintf("step %d (%s)", i+1, req.Str("op"))
		resp := t.Request(conn, req)
		if resp.HasStatus("error") || resp.HasStatus("eval-error") {
			t.Notef("%s got status %v", step, resp.Status())
		}
		if s.NewSession != "" {
			id := resp.Str("new-session")
			if id == "" {
				t.Stopf("%s got no new-session, so %s", step, s.Why)
			}
			sessions[s.NewSession] = id
		}
		if s.Snippet != "" {
			switch got := strings.Join(resp.Values(), ""); got {
			case want:
			case "":
				t.Stopf("%s gave no value, so %s", step, s.Why)
			default:
				t.Stopf("%s gave the value %q instead of %q, so %s", step, got, want, s.Why)
			}
		}
		if n := valuesApart(resp); n < s.Values {
			t.Stopf("%s gave %d values that can be told apart by their ns, not %d, so %s", step, n, s.Values, s.Why)
		}
		for _, path := range s.Dicts {
			if v := nonDictField(resp, path); v != nil {
				t.Stopf("%s has %s that is %s, not a dict, so %s", step, strings.Join(path, "."), typeName(v), s.Why)
			}
		}
	}
}

// valuesApart counts the values of a reply the way clients that tell
// them apart by ns do, where the parts of a value up to the next ns are
// one value.
func valuesApart(resp nrepl.Response) int {
	n, open := 0, false
	for _, m := range resp.Messages {
		if v, _ := m["value"].(string); v != "" {
			open = true
		}
		if m.Has("ns") && open {
			n, open = n+1, false
		}
	}
	if open {
		n++
	}
	return n
}

// nonDictField returns the field at path in a reply (e.g. versions, then
// clojure) unless it's a dict, an empty list or missing.
func nonDictField(resp nrepl.Response, path []string) any {
	v := resp.Get(path[0])
	for _, key := range path[1:] {
		d, ok := v.(map[string]any)
		if !ok {
			// The parent's problem, if any.
			return nil
		}
		v = d[key]
	}
	switch v := v.(type) {
	case nil, map[string]any:
		return nil
	case []any:
		if len(v) == 0 {
			return nil
		}
	}
	return v
}
