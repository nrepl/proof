// Package report renders check results for people and for machines.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

// Run is everything known about one run.
type Run struct {
	Proof   string         `json:"proof"`
	Server  string         `json:"server"`
	Address string         `json:"address"`
	Started time.Time      `json:"started"`
	Results []check.Result `json:"-"`
}

// Counts tallies verdicts.
func (r Run) Counts() map[check.Verdict]int {
	counts := map[check.Verdict]int{}
	for _, res := range r.Results {
		counts[res.Verdict]++
	}
	return counts
}

var labels = map[check.Verdict]string{
	check.Pass:    "PASS ",
	check.Warned:  "WARN ",
	check.Failed:  "FAIL ",
	check.Skipped: "SKIP ",
	check.Errored: "ERROR",
}

// Text writes a human-readable report. With verbose set it includes
// notes for every check and transcripts for the ones that didn't pass.
func Text(w io.Writer, run Run, verbose bool) {
	fmt.Fprintf(w, "proof %s: %s at %s\n", run.Proof, run.Server, run.Address)
	group := ""
	for _, res := range run.Results {
		g, _, _ := strings.Cut(res.ID, ".")
		if g != group {
			group = g
			fmt.Fprintf(w, "\n%s\n", g)
		}
		fmt.Fprintf(w, "  %s  %-28s %s\n", labels[res.Verdict], res.ID, res.Title)
		if res.Expected != "" {
			fmt.Fprintf(w, "         expected: %s\n", res.Expected)
		}
		for _, d := range res.Details {
			fmt.Fprintf(w, "         %s\n", d)
		}
		if res.Verdict == check.Failed || res.Verdict == check.Warned {
			fmt.Fprintf(w, "         why: %s\n", res.Why)
			for _, ref := range res.Refs {
				fmt.Fprintf(w, "         see: %s %s\n", ref.Name, ref.URL)
			}
		}
		if verbose {
			for _, n := range res.Notes {
				fmt.Fprintf(w, "         note: %s\n", n)
			}
			for i, tr := range res.Transcripts {
				fmt.Fprintf(w, "         connection %d:\n", i+1)
				Transcript(w, tr, "           ")
			}
		}
	}
	c := run.Counts()
	fmt.Fprintf(w, "\n%d passed, %d failed", c[check.Pass], c[check.Failed])
	if n := run.expectedFailures(); n > 0 {
		fmt.Fprintf(w, " (%d expected)", n)
	}
	fmt.Fprintf(w, ", %d warnings, %d skipped", c[check.Warned], c[check.Skipped])
	if c[check.Errored] > 0 {
		fmt.Fprintf(w, ", %d errors", c[check.Errored])
	}
	fmt.Fprintln(w)
}

func (r Run) expectedFailures() int {
	n := 0
	for _, res := range r.Results {
		if res.Expected != "" {
			n++
		}
	}
	return n
}

// Transcript writes the events of a connection, one per line, with times
// relative to the first one.
func Transcript(w io.Writer, events []nrepl.Event, indent string) {
	if len(events) == 0 {
		return
	}
	start := events[0].Time
	for _, ev := range events {
		arrow := "<-"
		if ev.Dir == nrepl.Sent {
			arrow = "->"
		}
		body := ""
		switch {
		case ev.Err != nil:
			body = fmt.Sprintf("undecodable frame (%v): %q", ev.Err, ev.Raw)
		case ev.Msg != nil:
			body = ev.Msg.String()
		default:
			body = fmt.Sprintf("%q", ev.Raw)
		}
		fmt.Fprintf(w, "%s%s %6dms %s\n", indent, arrow, ev.Time.Sub(start).Milliseconds(), body)
	}
}

type jsonResult struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Verdict  check.Verdict `json:"verdict"`
	Severity string        `json:"severity"`
	Why      string        `json:"why"`
	Refs     []check.Ref   `json:"refs"`
	Details  []string      `json:"details,omitempty"`
	Notes    []string      `json:"notes,omitempty"`
	Expected string        `json:"expected,omitempty"`
	Millis   int64         `json:"ms"`
}

// JSON writes a machine-readable report, meant for building a
// compatibility matrix across servers.
func JSON(w io.Writer, run Run) error {
	out := struct {
		Run
		Summary map[check.Verdict]int `json:"summary"`
		Results []jsonResult          `json:"results"`
	}{Run: run, Summary: run.Counts()}
	for _, r := range run.Results {
		out.Results = append(out.Results, jsonResult{
			ID: r.ID, Title: r.Title, Verdict: r.Verdict, Severity: r.Severity.String(),
			Why: r.Why, Refs: r.Refs, Details: r.Details, Notes: r.Notes, Expected: r.Expected,
			Millis: r.Duration.Milliseconds(),
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
