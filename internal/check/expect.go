package check

import (
	"fmt"
	"sort"
)

// Outcome sums up a run for the exit status.
type Outcome struct {
	// Unexpected are failures the profile doesn't list.
	Unexpected []string
	// Stale are listed failures that didn't fail. A baseline that's
	// allowed to go stale soon hides regressions, so these count against
	// the run too.
	Stale []string
	// Errors are checks the harness couldn't run.
	Errors []string
}

// OK reports whether the run should exit successfully.
func (o Outcome) OK() bool {
	return len(o.Unexpected) == 0 && len(o.Stale) == 0 && len(o.Errors) == 0
}

// Apply marks the failures the profile expects and works out the outcome.
// expected maps check ids to the reason they're expected to fail. Results
// that were skipped don't make an entry stale, since the check didn't run.
//
// Staleness is only judged on complete runs: when only some checks ran,
// the wire rules saw only part of the traffic, so a rule passing says
// nothing about whether its failure is fixed.
func Apply(results []Result, expected map[string]string, complete bool) Outcome {
	var o Outcome
	ran := map[string]Verdict{}
	for i := range results {
		r := &results[i]
		ran[r.ID] = r.Verdict
		switch r.Verdict {
		case Failed:
			if reason, ok := expected[r.ID]; ok {
				r.Expected = reason
			} else {
				o.Unexpected = append(o.Unexpected, r.ID)
			}
		case Errored:
			o.Errors = append(o.Errors, r.ID)
		}
	}
	if complete {
		for id := range expected {
			if v := ran[id]; v == Pass || v == Warned {
				o.Stale = append(o.Stale, id)
			}
		}
	}
	sort.Strings(o.Stale)
	return o
}

// UnknownIDs returns the ids in expected that match no check or rule.
func UnknownIDs(expected map[string]string, known []string) error {
	valid := map[string]bool{}
	for _, id := range known {
		valid[id] = true
	}
	var unknown []string
	for id := range expected {
		if !valid[id] {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("expected-failures lists unknown checks %v", unknown)
}
