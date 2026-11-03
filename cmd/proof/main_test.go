package main

import (
	"runtime/debug"
	"testing"
	"time"

	"github.com/nrepl/proof/internal/profile"
	"github.com/nrepl/proof/internal/serve"
)

func TestVersion(t *testing.T) {
	built := func(v string) *debug.BuildInfo { return &debug.BuildInfo{Main: debug.Module{Version: v}} }
	cases := []struct {
		release string
		info    *debug.BuildInfo
		want    string
	}{
		{"0.1.0", built("v0.2.0"), "0.1.0"},
		{"", built("v0.2.0"), "0.2.0"},
		{"", built("v0.0.0-20261023130420-411883581cfd"), "0.0.0-20261023130420-411883581cfd"},
		{"", built("(devel)"), "0.0.0-dev"},
		{"", nil, "0.0.0-dev"},
	}
	for _, c := range cases {
		if got := versionOf(c.release, c.info); got != c.want {
			t.Errorf("versionOf(%q, %v) = %q, want %q", c.release, c.info, got, c.want)
		}
	}
}

func TestServerVersion(t *testing.T) {
	srv, err := serve.Listen("127.0.0.1:0", "0.1.0-dev", nil)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	t.Cleanup(func() { srv.Stop(0) })
	cases := []struct {
		code, want string
	}{
		{`"1.2.3"`, "1.2.3"},
		{`"1.2.3\nSNAPSHOT"`, "1.2.3 SNAPSHOT"},
		{"(+ 1 2)", "3"},
		{"", ""},
	}
	for _, c := range cases {
		p := &profile.Profile{Timeout: time.Second}
		if c.code != "" {
			p.Snippets = map[string]profile.Snippet{"version": {Code: c.code}}
		}
		got, err := serverVersion(p, srv.Addr())
		if err != nil || got != c.want {
			t.Errorf("version snippet %q gave %q (%v), want %q", c.code, got, err, c.want)
		}
	}
	p := &profile.Profile{Timeout: time.Second, Snippets: map[string]profile.Snippet{"version": {Code: "(throw (ex-info \"no\" {}))"}}}
	if _, err := serverVersion(p, srv.Addr()); err == nil {
		t.Error("expected an error for a snippet that throws")
	}
}
