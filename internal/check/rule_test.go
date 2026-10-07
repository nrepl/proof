package check

import (
	"strings"
	"testing"

	"github.com/nrepl/proof/nrepl"
)

func TestGradeCollapsesRepeatedProblems(t *testing.T) {
	rule := &Rule{ID: "wire.x", Severity: Fail, Inspect: func(events []nrepl.Event, report Reporter) {
		for range events {
			report("value is an integer", "{value 3}")
		}
		report("status is a string", "")
	}}
	traffic := []Traffic{
		{Label: "a", Events: make([]nrepl.Event, 2)},
		{Label: "b", Events: make([]nrepl.Event, 1)},
	}
	res := rule.grade(traffic)
	if res.Verdict != Failed {
		t.Errorf("verdict %s", res.Verdict)
	}
	want := []string{
		"value is an integer (3 times, first during a): {value 3}",
		"status is a string (2 times, first during a)",
	}
	if strings.Join(res.Details, "\n") != strings.Join(want, "\n") {
		t.Errorf("details:\n%s\nwant:\n%s", strings.Join(res.Details, "\n"), strings.Join(want, "\n"))
	}
}

func TestGradePassesCleanTraffic(t *testing.T) {
	rule := &Rule{ID: "wire.x", Severity: Fail, Inspect: func([]nrepl.Event, Reporter) {}}
	if res := rule.grade([]Traffic{{Label: "a"}}); res.Verdict != Pass || len(res.Details) != 0 {
		t.Errorf("got %s %v", res.Verdict, res.Details)
	}
}
