package report

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nrepl/proof/internal/check"
)

func sampleRun(server string, verdicts ...check.Verdict) Run {
	ids := []string{"describe.reply", "eval.value", "eval.ns"}
	r := Run{Proof: "test", Server: server, Address: "localhost:1", Started: time.Unix(0, 0)}
	for i, v := range verdicts {
		r.Results = append(r.Results, check.Result{ID: ids[i], Title: "title " + ids[i], Verdict: v})
	}
	return r
}

// The matrix is built from JSON reports, so go through the real encoder.
func TestMatrixFromJSONReports(t *testing.T) {
	dir := t.TempDir()
	var reports []Report
	for i, run := range []Run{
		sampleRun("Alpha", check.Pass, check.Pass, check.Pass),
		sampleRun("Beta | Two", check.Pass, check.Failed, check.Skipped),
	} {
		path := filepath.Join(dir, string(rune('a'+i))+".json")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := JSON(f, run); err != nil {
			t.Fatal(err)
		}
		f.Close()
		r, err := ReadJSON(path)
		if err != nil {
			t.Fatal(err)
		}
		reports = append(reports, r)
	}

	var buf bytes.Buffer
	Matrix(&buf, reports)
	got := buf.String()
	for _, want := range []string{
		"| Check | Alpha | Beta \\| Two |",
		"| `eval.value` title eval.value | pass | **fail** |",
		"| `eval.ns` title eval.ns | pass | skip |",
		"| **Passed** | 3/3 | 1/2 |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("matrix is missing %q:\n%s", want, got)
		}
	}
}

func TestReadJSONRejectsOtherFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.json")
	os.WriteFile(path, []byte(`{"name": "not a report"}`), 0o644)
	if _, err := ReadJSON(path); err == nil {
		t.Error("expected an error")
	}
}

func TestTextMarksExpectedFailures(t *testing.T) {
	run := sampleRun("Alpha", check.Pass, check.Failed)
	run.Results[1].Expected = "known bug"
	var buf bytes.Buffer
	Text(&buf, run, false)
	out := buf.String()
	if !strings.Contains(out, "expected: known bug") || !strings.Contains(out, "1 failed (1 expected)") {
		t.Errorf("expected failure not shown:\n%s", out)
	}
}
