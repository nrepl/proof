package report

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"maps"
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
	return score(slices.Collect(maps.Values(t.results[i])))
}

// score says how many of the results are passes, out of the ones that
// ran.
func score(results []jsonResult) string {
	passed, ran := 0, 0
	for _, res := range results {
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

// clients are the clients that the checks say they break, the ones the
// most checks break first. Every report has a say, as the ones made
// before proof named clients don't name any.
func (t table) clients() []string {
	var names []string
	counts := map[string]int{}
	for _, id := range t.ids {
		var named []string
		for _, results := range t.results {
			for _, name := range results[id].Clients {
				if !slices.Contains(named, name) {
					named = append(named, name)
				}
			}
		}
		for _, name := range named {
			if counts[name] == 0 {
				names = append(names, name)
			}
			counts[name]++
		}
	}
	slices.SortStableFunc(names, func(a, b string) int { return counts[b] - counts[a] })
	return names
}

// client is how the server of the i-th report fares with a client: the
// score of the checks that break the client, and the ones it didn't
// pass. The verdict is the worst of theirs, or skip when none ran.
func (t table) client(i int, name string) (check.Verdict, string, []string) {
	verdict := check.Pass
	var ran []jsonResult
	var missed []string
	for _, id := range t.ids {
		res, ok := t.results[i][id]
		if !ok || res.Verdict == check.Skipped || !slices.Contains(res.Clients, name) {
			continue
		}
		ran = append(ran, res)
		switch res.Verdict {
		case check.Failed:
			verdict = check.Failed
			missed = append(missed, res.ID+": "+res.Title)
		case check.Errored:
			if verdict != check.Failed {
				verdict = check.Errored
			}
			missed = append(missed, res.ID+": "+res.Title+" (error)")
		}
	}
	if len(ran) == 0 {
		return check.Skipped, "skip", nil
	}
	return verdict, score(ran), missed
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

// The page comes from matrix.html, with the tables and the versions of
// proof where its markers are.
//
//go:embed matrix.html
var matrixPage string

// HTML writes the same matrix as Matrix as a web page, with the checks
// grouped like in the text report. The why of a check and the details of
// a verdict show up when hovering over them. Another tab of the page has
// how each client fares with the servers.
func HTML(w io.Writer, reports []Report) error {
	t := newTable(reports)
	esc := html.EscapeString
	var servers strings.Builder
	for _, r := range reports {
		name := esc(r.Server)
		if r.Homepage != "" {
			name = fmt.Sprintf(`<a href="%s">%s</a>`, esc(r.Homepage), name)
		}
		fmt.Fprintf(&servers, "<th>%s", name)
		if r.ServerVersion != "" {
			fmt.Fprintf(&servers, `<span class="version">%s</span>`, esc(r.ServerVersion))
		}
		servers.WriteString("</th>")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<thead>\n<tr><th>Check</th>%s</tr>\n</thead>\n<tbody>\n", servers.String())
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

	var c strings.Builder
	fmt.Fprintf(&c, "<thead>\n<tr><th>Client</th>%s</tr>\n</thead>\n<tbody>\n", servers.String())
	for _, client := range t.clients() {
		fmt.Fprintf(&c, `<tr><th scope="row">%s</th>`, esc(client))
		for i := range reports {
			verdict, score, missed := t.client(i, client)
			fmt.Fprintf(&c, `<td class="%s"%s>%s</td>`, verdict, titleAttr(strings.Join(missed, "\n")), score)
		}
		c.WriteString("</tr>\n")
	}
	c.WriteString("</tbody>\n")

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
	_, err := strings.NewReplacer("<!-- checks -->\n", b.String(), "<!-- clients -->\n", c.String(),
		"<!-- proof -->", esc(made)).WriteString(w, matrixPage)
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
