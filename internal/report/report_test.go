package report

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

func sampleRun(server string, verdicts ...check.Verdict) Run {
	ids := []string{"describe.reply", "eval.value", "eval.ns"}
	clients := [][]string{{"Calva", "CIDER"}, {"CIDER"}, {"CIDER", "Conjure"}}
	r := Run{Proof: "test", Server: server, Address: "localhost:1", Started: time.Unix(0, 0)}
	for i, v := range verdicts {
		r.Results = append(r.Results, check.Result{ID: ids[i], Title: "title " + ids[i], Why: "why " + ids[i],
			Clients: clients[i], Verdict: v})
	}
	return r
}

// sampleReports are the reports of two servers, the second of which
// failed and skipped a check. The matrix is built from JSON reports, so
// they go through the real encoder.
func sampleReports(t *testing.T) []Report {
	t.Helper()
	beta := sampleRun("Beta | Two", check.Pass, check.Failed, check.Skipped)
	beta.ServerVersion = "2.0"
	beta.Homepage = "https://example.com/beta?a&b"
	beta.Started = time.Date(2026, 11, 3, 5, 0, 0, 0, time.UTC)
	beta.Results[1].Details = []string{"gave no value"}
	dir := t.TempDir()
	var reports []Report
	for i, run := range []Run{sampleRun("Alpha", check.Pass, check.Pass, check.Pass), beta} {
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
	return reports
}

func TestMatrixFromJSONReports(t *testing.T) {
	var buf bytes.Buffer
	Matrix(&buf, sampleReports(t))
	got := buf.String()
	for _, want := range []string{
		"| Check | Alpha | Beta \\| Two 2.0 |",
		"| `eval.value` title eval.value | pass | **fail** |",
		"| `eval.ns` title eval.ns | pass | skip |",
		"| **Passed** | 3/3 | 1/2 |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("matrix is missing %q:\n%s", want, got)
		}
	}
}

func TestMatrixAsAPage(t *testing.T) {
	var buf bytes.Buffer
	if err := HTML(&buf, sampleReports(t)); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{
		`<th>Alpha</th>`,
		`<th><a href="https://example.com/beta?a&amp;b">Beta | Two</a><span class="version">2.0</span></th>`,
		`<tr class="group"><th colspan="3">eval</th></tr>`,
		`<th scope="row" title="why eval.value"><code>eval.value</code><span class="title">title eval.value</span></th>`,
		`<td class="pass">pass</td><td class="fail" title="gave no value">fail</td>`,
		`<td class="skip">skip</td>`,
		`<td>3/3</td><td>1/2</td>`,
		// The clients the most checks break come first.
		`<tr><th>Client</th><th>Alpha</th>`,
		`<tr><th scope="row">CIDER</th><td class="pass">3/3</td><td class="fail" title="eval.value: title eval.value">1/2</td></tr>
<tr><th scope="row">Calva</th><td class="pass">1/1</td><td class="pass">1/1</td></tr>
<tr><th scope="row">Conjure</th><td class="pass">1/1</td><td class="skip">skip</td></tr>`,
		"Made with proof test on 2026-11-03.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("page is missing %q:\n%s", want, got)
		}
	}
}

// A check the first report doesn't have still goes in its group.
func TestChecksFromLaterReportsJoinTheirGroup(t *testing.T) {
	reports := []Report{
		{Run: Run{Server: "Alpha"}, Results: []jsonResult{{ID: "eval.value"}, {ID: "describe.reply"}}},
		{Run: Run{Server: "Beta"}, Results: []jsonResult{{ID: "eval.value"}, {ID: "describe.reply"}, {ID: "eval.ns"}}},
	}
	var buf bytes.Buffer
	if err := HTML(&buf, reports); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if n := strings.Count(got, `<tr class="group"><th colspan="3">eval</th></tr>`); n != 1 {
		t.Errorf("eval has %d groups:\n%s", n, got)
	}
	if i, j := strings.Index(got, "<code>eval.ns</code>"), strings.Index(got, "<code>describe.reply</code>"); i > j {
		t.Errorf("eval.ns isn't with the other eval checks:\n%s", got)
	}
	if !strings.Contains(got, "Made with proof.") {
		t.Errorf("reports without a proof version still get one:\n%s", got)
	}
	reports[0].Proof, reports[1].Proof = "0.1.0", "0.2.0"
	buf.Reset()
	if err := HTML(&buf, reports); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.Contains(got, "Made with proof 0.1.0, 0.2.0.") {
		t.Errorf("the versions of proof aren't listed:\n%s", got)
	}
	buf.Reset()
	Matrix(&buf, reports)
	if got := buf.String(); strings.Index(got, "`eval.ns`") > strings.Index(got, "`describe.reply`") {
		t.Errorf("eval.ns isn't with the other eval checks:\n%s", got)
	}
}

func TestTextShowsTheServerVersion(t *testing.T) {
	run := sampleRun("Alpha", check.Pass)
	run.ServerVersion = "1.2.3"
	var buf bytes.Buffer
	Text(&buf, run, false)
	if out := buf.String(); !strings.HasPrefix(out, "proof test: Alpha 1.2.3 at localhost:1") {
		t.Errorf("no version in the report:\n%s", out)
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

func TestTranscriptShowsWhoHungUp(t *testing.T) {
	start := time.Unix(0, 0)
	events := []nrepl.Event{
		{Dir: nrepl.Sent, Time: start, Msg: nrepl.Message{"id": "1", "op": "describe"}},
		{Dir: nrepl.Sent, Time: start.Add(5 * time.Millisecond), Closed: true},
	}
	var buf bytes.Buffer
	Transcript(&buf, events, "  ")
	want := "  ->      0ms {id \"1\", op \"describe\"}\n  ->      5ms closed the connection\n"
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

// A client fares as badly as the worst of the checks that break it.
func TestClientsGetTheWorstVerdict(t *testing.T) {
	reports := []Report{{Run: Run{Server: "Alpha"}, Results: []jsonResult{
		{ID: "a.x", Title: "x", Verdict: check.Errored, Clients: []string{"CIDER"}},
		{ID: "a.y", Title: "y", Verdict: check.Pass, Clients: []string{"CIDER"}},
		{ID: "a.z", Title: "z", Verdict: check.Failed, Clients: []string{"Calva"}},
	}}}
	verdict, score, missed := newTable(reports).client(0, "CIDER")
	if verdict != check.Errored || score != "1/2" || !slices.Equal(missed, []string{"a.x: x (error)"}) {
		t.Errorf("got %s %s %v", verdict, score, missed)
	}
	reports[0].Results[2].Clients = []string{"CIDER"}
	verdict, score, missed = newTable(reports).client(0, "CIDER")
	if verdict != check.Failed || score != "1/3" || !slices.Equal(missed, []string{"a.x: x (error)", "a.z: z"}) {
		t.Errorf("got %s %s %v", verdict, score, missed)
	}
}

// Reports made before proof named clients don't hide the clients that
// later reports name.
func TestClientsFromAnyReport(t *testing.T) {
	reports := []Report{
		{Run: Run{Server: "Old"}, Results: []jsonResult{{ID: "eval.value", Verdict: check.Pass}}},
		{Run: Run{Server: "New"}, Results: []jsonResult{{ID: "eval.value", Verdict: check.Pass, Clients: []string{"CIDER"}}}},
	}
	tbl := newTable(reports)
	if got := tbl.clients(); !slices.Equal(got, []string{"CIDER"}) {
		t.Errorf("got clients %v", got)
	}
	if verdict, _, _ := tbl.client(0, "CIDER"); verdict != check.Skipped {
		t.Errorf("a report without clients gives %s", verdict)
	}
}
