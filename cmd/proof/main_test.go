package main

import (
	"runtime/debug"
	"testing"
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
