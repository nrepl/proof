package check

import (
	"fmt"

	"github.com/nrepl/proof/nrepl"
)

// Traffic is one connection's transcript.
type Traffic struct {
	// Label says where the connection came from: the check that opened
	// it, or for a client's traffic, the connection's number.
	Label  string
	Events []nrepl.Event
}

// Rule is graded against every message exchanged during the run, rather
// than against a particular request. Wire rules catch problems wherever
// they happen to surface.
type Rule struct {
	ID       string
	Title    string
	Severity Severity
	Why      string
	Refs     []Ref
	// Inspect looks at one connection's events and reports each problem.
	Inspect func(events []nrepl.Event, report Reporter)
}

// Reporter takes a problem and an example of it. Identical problems are
// counted together, so the problem shouldn't include ids or other details
// that differ from message to message; those belong in the example.
type Reporter func(problem, example string)

const maxRuleDetails = 5

// Grade grades recorded traffic against each rule.
func Grade(rules []*Rule, traffic []Traffic) []Result {
	results := make([]Result, 0, len(rules))
	for _, r := range rules {
		results = append(results, r.grade(traffic))
	}
	return results
}

// grade collapses identical problems, since a misbehaving server tends to
// repeat the same mistake in every message.
func (r *Rule) grade(traffic []Traffic) Result {
	res := Result{ID: r.ID, Title: r.Title, Severity: r.Severity, Why: r.Why, Refs: r.Refs, Verdict: Pass}
	type problem struct {
		text, example, firstLabel string
		count                     int
	}
	var problems []*problem
	seen := map[string]*problem{}
	reporter := func(tr Traffic) Reporter {
		return func(text, example string) {
			if p, ok := seen[text]; ok {
				p.count++
				return
			}
			p := &problem{text: text, example: example, firstLabel: tr.Label, count: 1}
			seen[text] = p
			problems = append(problems, p)
		}
	}
	for _, tr := range traffic {
		r.Inspect(tr.Events, reporter(tr))
	}
	for i, p := range problems {
		if i == maxRuleDetails {
			res.Details = append(res.Details, fmt.Sprintf("... and %d more kinds of problem", len(problems)-maxRuleDetails))
			break
		}
		times := ""
		if p.count > 1 {
			times = fmt.Sprintf("%d times, first ", p.count)
		}
		d := fmt.Sprintf("%s (%sduring %s)", p.text, times, p.firstLabel)
		if p.example != "" {
			d += ": " + truncate(p.example, 200)
		}
		res.Details = append(res.Details, d)
	}
	if len(problems) > 0 {
		res.Verdict = r.Severity.verdict()
	}
	return res
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
