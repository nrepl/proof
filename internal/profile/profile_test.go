package profile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShippedProfilesLoad(t *testing.T) {
	names := Bundled()
	if len(names) == 0 {
		t.Fatal("no profiles found")
	}
	for _, name := range names {
		p, err := Find(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if p.Launch.StartupTimeout < time.Second {
			t.Errorf("%s: startup-timeout parsed as %s", name, p.Launch.StartupTimeout)
		}
		if _, ok := p.Snippets["value"]; !ok {
			t.Errorf("%s: no value snippet", name)
		}
		// A bundled profile has no directory to find a program in.
		if strings.ContainsAny(p.Launch.Command[0], `/\`) {
			t.Errorf("%s: %s isn't a program on the PATH", name, p.Launch.Command[0])
		}
	}
}

func TestFindingProfiles(t *testing.T) {
	p, err := Find("babashka")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Babashka" {
		t.Errorf("babashka found %q", p.Name)
	}
	if _, err := Find("../../profiles/jank.toml"); err != nil {
		t.Errorf("a path to a profile: %v", err)
	}
	if _, err := Find("nope"); err == nil || !strings.Contains(err.Error(), "babashka, basilisp") {
		t.Errorf("an unknown name should list the bundled profiles, got %v", err)
	}
	// A path is never taken for a name.
	for _, path := range []string{"babashka.toml", "elsewhere/babashka"} {
		if _, err := Find(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: got %v, want a missing file", path, err)
		}
	}
	// A name is never taken for a path, e.g. a checkout of the server.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "babashka"), 0o755); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	if _, err := Find("babashka"); err != nil {
		t.Errorf("babashka next to a directory with that name: %v", err)
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
