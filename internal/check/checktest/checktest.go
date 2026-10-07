// Package checktest helps test the checks and rules, and the servers they
// check.
package checktest

import (
	"sort"
	"testing"

	"github.com/nrepl/proof/internal/check"
)

// ByID maps results to the ids of their checks or rules.
func ByID(results []check.Result) map[string]check.Result {
	byID := make(map[string]check.Result, len(results))
	for _, r := range results {
		byID[r.ID] = r
	}
	return byID
}

// Verdicts makes sure each check or rule in want got the listed verdict,
// and that everything else passed.
func Verdicts(t testing.TB, results map[string]check.Result, want map[string]check.Verdict) {
	t.Helper()
	var ids []string
	for id := range results {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := results[id]
		w, listed := want[id]
		if !listed {
			w = check.Pass
		}
		if r.Verdict != w {
			t.Errorf("%s: got %s, want %s %v", id, r.Verdict, w, r.Details)
		}
	}
	for id := range want {
		if _, ok := results[id]; !ok {
			t.Errorf("%s: no such check or rule", id)
		}
	}
}
