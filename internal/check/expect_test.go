package check

import (
	"slices"
	"testing"
)

func TestApply(t *testing.T) {
	results := []Result{
		{ID: "a", Verdict: Failed},
		{ID: "b", Verdict: Failed},
		{ID: "c", Verdict: Pass},
		{ID: "d", Verdict: Skipped},
		{ID: "e", Verdict: Errored},
		{ID: "f", Verdict: Warned},
	}
	expected := map[string]string{"a": "known bug", "c": "fixed now", "d": "can't run", "f": "only warns now"}
	o := Apply(results, expected, true)
	if results[0].Expected != "known bug" || results[1].Expected != "" {
		t.Errorf("expected reasons not recorded: %+v", results[:2])
	}
	if !slices.Equal(o.Unexpected, []string{"b"}) {
		t.Errorf("unexpected: %v", o.Unexpected)
	}
	if !slices.Equal(o.Stale, []string{"c", "f"}) {
		t.Errorf("stale: %v", o.Stale)
	}
	if !slices.Equal(o.Errors, []string{"e"}) {
		t.Errorf("errors: %v", o.Errors)
	}
	if o.OK() {
		t.Error("outcome should not be OK")
	}
	if !Apply([]Result{{ID: "a", Verdict: Failed}}, map[string]string{"a": "x"}, true).OK() {
		t.Error("an expected failure alone should be OK")
	}
	if partial := Apply([]Result{{ID: "c", Verdict: Pass}}, expected, false); len(partial.Stale) != 0 {
		t.Errorf("a partial run shouldn't judge staleness, got %v", partial.Stale)
	}
}

func TestUnknownIDs(t *testing.T) {
	if err := UnknownIDs(map[string]string{"a": ""}, []string{"a", "b"}); err != nil {
		t.Error(err)
	}
	if err := UnknownIDs(map[string]string{"a": "", "zz": ""}, []string{"a"}); err == nil {
		t.Error("expected an error for zz")
	}
}
