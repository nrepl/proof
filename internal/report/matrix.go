package report

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/nrepl/proof/internal/check"
)

// Report is a JSON report read back in. Its results are the ones JSON
// wrote, rather than the Results of Run.
type Report struct {
	Run
	Results []jsonResult `json:"results"`
}

// ReadJSON loads a report written by JSON.
func ReadJSON(path string) (Report, error) {
	var r Report
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("%s: %w", path, err)
	}
	if r.Server == "" {
		return r, fmt.Errorf("%s: doesn't look like a proof report", path)
	}
	return r, nil
}

// table is a row per check, in the order of the first report that has
// it, and a column per report.
type table struct {
	ids []string
	// checks has the first result of each check, for its title and why.
	checks  map[string]jsonResult
	results []map[string]jsonResult
}

func newTable(reports []Report) table {
	t := table{checks: map[string]jsonResult{}, results: make([]map[string]jsonResult, len(reports))}
	for i, r := range reports {
		t.results[i] = map[string]jsonResult{}
		for _, res := range r.Results {
			if _, ok := t.checks[res.ID]; !ok {
				t.ids = slices.Insert(t.ids, t.place(res.ID), res.ID)
				t.checks[res.ID] = res
			}
			t.results[i][res.ID] = res
		}
	}
	return t
}

// place is where a check that isn't in the table yet goes: after the last
// one in its group (e.g. eval), so a check only a later report has (e.g.
// one made by a newer proof) still ends up with the rest of its group.
func (t table) place(id string) int {
	group, _, _ := strings.Cut(id, ".")
	for i := len(t.ids) - 1; i >= 0; i-- {
		if g, _, _ := strings.Cut(t.ids[i], "."); g == group {
			return i + 1
		}
	}
	return len(t.ids)
}

// passed returns how many checks the server of the i-th report passed,
// out of the ones that ran.
func (t table) passed(i int) string {
	passed, ran := 0, 0
	for _, res := range t.results[i] {
		if res.Verdict == check.Skipped {
			continue
		}
		ran++
		if res.Verdict == check.Pass {
			passed++
		}
	}
	return fmt.Sprintf("%d/%d", passed, ran)
}

var cells = map[check.Verdict]string{
	check.Pass:    "pass",
	check.Failed:  "**fail**",
	check.Warned:  "warn",
	check.Skipped: "skip",
	check.Errored: "error",
}

// Matrix writes a Markdown table with a row per check and a column per
// server. Checks appear in the order of the first report that has them,
// with the rest of their group.
func Matrix(w io.Writer, reports []Report) {
	t := newTable(reports)
	header := []string{"Check"}
	rule := []string{"---"}
	for _, r := range reports {
		header = append(header, escape(r.Title()))
		rule = append(rule, "---")
	}
	fmt.Fprintf(w, "| %s |\n|%s|\n", strings.Join(header, " | "), strings.Join(rule, "|"))
	for _, id := range t.ids {
		row := []string{fmt.Sprintf("`%s` %s", id, escape(t.checks[id].Title))}
		for i := range reports {
			row = append(row, cells[t.results[i][id].Verdict])
		}
		fmt.Fprintf(w, "| %s |\n", strings.Join(row, " | "))
	}

	totals := []string{"**Passed**"}
	for i := range reports {
		totals = append(totals, t.passed(i))
	}
	fmt.Fprintf(w, "| %s |\n", strings.Join(totals, " | "))
}

func escape(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}

// The page comes from matrix.html, with the table and the versions of
// proof where its markers are.
//
//go:embed matrix.html
var matrixPage string

// HTML writes the same matrix as Matrix as a web page, with the checks
// grouped like in the text report. The why of a check and the details of
// a verdict show up when hovering over them.
func HTML(w io.Writer, reports []Report) error {
	t := newTable(reports)
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString("<thead>\n<tr><th>Check</th>")
	for _, r := range reports {
		name := esc(r.Server)
		if r.Homepage != "" {
			name = fmt.Sprintf(`<a href="%s">%s</a>`, esc(r.Homepage), name)
		}
		fmt.Fprintf(&b, "<th>%s", name)
		if r.ServerVersion != "" {
			fmt.Fprintf(&b, `<span class="version">%s</span>`, esc(r.ServerVersion))
		}
		b.WriteString("</th>")
	}
	b.WriteString("</tr>\n</thead>\n<tbody>\n")
	group := ""
	for _, id := range t.ids {
		if g, _, _ := strings.Cut(id, "."); g != group {
			group = g
			fmt.Fprintf(&b, "<tr class=\"group\"><th colspan=\"%d\">%s</th></tr>\n", len(reports)+1, esc(g))
		}
		res := t.checks[id]
		fmt.Fprintf(&b, `<tr><th scope="row"%s><code>%s</code><span class="title">%s</span></th>`,
			titleAttr(res.Why), esc(id), esc(res.Title))
		for i := range reports {
			res := t.results[i][id]
			notes := res.Details
			if res.Expected != "" {
				notes = append([]string{"expected: " + res.Expected}, notes...)
			}
			fmt.Fprintf(&b, `<td class="%s"%s>%s</td>`, res.Verdict, titleAttr(strings.Join(notes, "\n")), res.Verdict)
		}
		b.WriteString("</tr>\n")
	}
	b.WriteString("</tbody>\n<tfoot>\n<tr><th scope=\"row\">Passed</th>")
	for i := range reports {
		fmt.Fprintf(&b, "<td>%s</td>", t.passed(i))
	}
	b.WriteString("</tr>\n</tfoot>\n")

	var proofs []string
	var latest time.Time
	for _, r := range reports {
		if r.Proof != "" && !slices.Contains(proofs, r.Proof) {
			proofs = append(proofs, r.Proof)
		}
		if r.Started.After(latest) {
			latest = r.Started
		}
	}
	made := "proof"
	if len(proofs) > 0 {
		made += " " + strings.Join(proofs, ", ")
	}
	if !latest.IsZero() {
		made += " on " + latest.UTC().Format(time.DateOnly)
	}
	_, err := strings.NewReplacer("<!-- table -->\n", b.String(), "<!-- proof -->", esc(made)).WriteString(w, matrixPage)
	return err
}

// titleAttr is a title attribute with the given text, or nothing when
// there's no text.
func titleAttr(text string) string {
	if text == "" {
		return ""
	}
	return ` title="` + html.EscapeString(text) + `"`
}
