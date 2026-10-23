// Package profile loads the TOML files that describe how to run and talk
// to a particular nREPL server.
package profile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/nrepl/proof/profiles"
)

// Profile describes one server implementation.
type Profile struct {
	Name     string `toml:"name"`
	Homepage string `toml:"homepage"`

	// Address of an already running server ("host:port"). When set, the
	// launch section is ignored.
	Address string `toml:"address"`
	Launch  Launch `toml:"launch"`

	// Timeout for a single request. Defaults to 10s.
	Timeout time.Duration `toml:"timeout"`

	// Capabilities are language features some checks depend on, such as
	// "namespaces". A check needing a capability the profile doesn't
	// declare is skipped.
	Capabilities map[string]bool `toml:"capabilities"`

	// Snippets are the bits of code the checks evaluate, written in the
	// server's language. A check needing a snippet the profile doesn't
	// provide is skipped.
	Snippets map[string]Snippet `toml:"snippets"`

	// ExpectedFailures maps check ids to the reason they're known to
	// fail. Expected failures don't fail the run, but one that starts
	// passing does, so the list can't quietly go stale.
	ExpectedFailures map[string]string `toml:"expected-failures"`

	// Dir is the directory the profile was loaded from.
	Dir string `toml:"-"`
}

// Launch says how to start the server.
type Launch struct {
	// Command and its arguments. Environment variables are expanded, and
	// a relative program path containing a slash is resolved against the
	// profile's directory.
	Command []string `toml:"command"`
	// PortPattern is a regular expression matched against the server's
	// output. Its first group (or a group named "port") is the port.
	PortPattern string `toml:"port-pattern"`
	// Host to connect to once the port is known. Defaults to localhost.
	Host           string        `toml:"host"`
	StartupTimeout time.Duration `toml:"startup-timeout"`
	// Env holds extra environment variables for the server process.
	Env map[string]string `toml:"env"`
}

// Snippet is a piece of code plus what evaluating it should produce.
// Which fields matter depends on the check using it.
type Snippet struct {
	Code   string   `toml:"code"`
	Value  string   `toml:"value"`
	Values []string `toml:"values"`
	Out    string   `toml:"out"`
	Err    string   `toml:"err"`
	// Use is code that refers to what Code defined.
	Use string `toml:"use"`
	// Eof is the value Code returns when stdin is at end of input.
	Eof string `toml:"eof"`
}

// Load reads and validates a profile.
func Load(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return parse(path, data, filepath.Dir(abs))
}

// Find loads the bundled profile with the given name (e.g. babashka), or
// the profile at path when it's not one of their names.
func Find(path string) (*Profile, error) {
	if !slices.Contains(Bundled(), path) {
		p, err := Load(path)
		if errors.Is(err, fs.ErrNotExist) && filepath.Base(path) == path && filepath.Ext(path) == "" {
			return nil, fmt.Errorf("there's no file named %s and no bundled profile with that name (the bundled ones are %s)",
				path, strings.Join(Bundled(), ", "))
		}
		return p, err
	}
	data, err := fs.ReadFile(profiles.FS, path+".toml")
	if err != nil {
		return nil, err
	}
	// The bundled profiles start programs from the PATH, so they don't
	// need a directory.
	return parse(path, data, "")
}

// Bundled returns the names of the profiles that come with proof.
func Bundled() []string {
	entries, err := fs.ReadDir(profiles.FS, ".")
	if err != nil {
		panic(err) // the profiles are embedded at build time
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = strings.TrimSuffix(e.Name(), ".toml")
	}
	slices.Sort(names)
	return names
}

// parse decodes and validates the profile in data, which was loaded from
// dir. name says where it came from in errors.
func parse(name string, data []byte, dir string) (*Profile, error) {
	var p Profile
	md, err := toml.Decode(string(data), &p)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return nil, fmt.Errorf("%s: unknown keys %v", name, undecoded)
	}
	p.Dir = dir
	p.setDefaults()
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return &p, nil
}

func (p *Profile) setDefaults() {
	if p.Timeout == 0 {
		p.Timeout = 10 * time.Second
	}
	if p.Launch.Host == "" {
		p.Launch.Host = "localhost"
	}
	if p.Launch.StartupTimeout == 0 {
		p.Launch.StartupTimeout = 60 * time.Second
	}
}

func (p *Profile) validate() error {
	var errs []error
	if p.Name == "" {
		errs = append(errs, errors.New("name is required"))
	}
	if p.Address == "" {
		if len(p.Launch.Command) == 0 {
			errs = append(errs, errors.New("either address or launch.command is required"))
		}
		if p.Launch.PortPattern == "" {
			errs = append(errs, errors.New("launch.port-pattern is required"))
		}
	}
	for name, s := range p.Snippets {
		if s.Code == "" {
			errs = append(errs, fmt.Errorf("snippets.%s: code is required", name))
		}
	}
	return errors.Join(errs...)
}

// Command returns the launch command with variables expanded and the
// program path resolved.
func (p *Profile) Command() []string {
	cmd := make([]string, len(p.Launch.Command))
	for i, arg := range p.Launch.Command {
		cmd[i] = os.ExpandEnv(arg)
	}
	if len(cmd) > 0 && !filepath.IsAbs(cmd[0]) && filepath.Base(cmd[0]) != cmd[0] {
		cmd[0] = filepath.Join(p.Dir, cmd[0])
	}
	return cmd
}
