package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShippedProfilesLoad(t *testing.T) {
	paths, err := filepath.Glob("../../profiles/*.toml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no profiles found (%v)", err)
	}
	for _, path := range paths {
		p, err := Load(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if p.Launch.StartupTimeout < time.Second {
			t.Errorf("%s: startup-timeout parsed as %s", path, p.Launch.StartupTimeout)
		}
		if _, ok := p.Snippets["value"]; !ok {
			t.Errorf("%s: no value snippet", path)
		}
	}
}

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "p.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaults(t *testing.T) {
	p, err := Load(write(t, `name = "x"
address = "localhost:1"`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Timeout != 10*time.Second || p.Launch.Host != "localhost" {
		t.Errorf("defaults not applied: %+v", p)
	}
}

func TestRejectsBadProfiles(t *testing.T) {
	cases := map[string]string{
		"missing name":         `address = "localhost:1"`,
		"nothing to run":       `name = "x"`,
		"no port pattern":      "name = \"x\"\n[launch]\ncommand = [\"x\"]",
		"unknown key":          "name = \"x\"\naddress = \"localhost:1\"\ntimout = \"1s\"",
		"snippet without code": "name = \"x\"\naddress = \"localhost:1\"\n[snippets.value]\nvalue = \"3\"",
	}
	for name, content := range cases {
		if _, err := Load(write(t, content)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestCommandResolution(t *testing.T) {
	t.Setenv("PROOF_TEST_DIR", "/opt/x")
	p := &Profile{Dir: "/profiles", Launch: Launch{Command: []string{"bin/server", "--root", "$PROOF_TEST_DIR"}}}
	got := strings.Join(p.Command(), " ")
	if got != "/profiles/bin/server --root /opt/x" {
		t.Errorf("got %q", got)
	}
	p.Launch.Command = []string{"bb", "nrepl-server"}
	if got := p.Command()[0]; got != "bb" {
		t.Errorf("a bare program name should be left for PATH lookup, got %q", got)
	}
}
