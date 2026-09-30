package report

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nrepl/proof/internal/check"
)

// Report is a JSON report read back in.
type Report struct {
	Server  string       `json:"server"`
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

var cells = map[check.Verdict]string{
	check.Pass:    "pass",
	check.Failed:  "**fail**",
	check.Warned:  "warn",
	check.Skipped: "skip",
	check.Errored: "error",
}

// Matrix writes a Markdown table with a row per check and a column per
// server. Checks appear in the order of the first report that has them.
func Matrix(w io.Writer, reports []Report) {
	var ids []string
	titles := map[string]string{}
	verdicts := make([]map[string]check.Verdict, len(reports))
	for i, r := range reports {
		verdicts[i] = map[string]check.Verdict{}
		for _, res := range r.Results {
			if _, ok := titles[res.ID]; !ok {
				ids = append(ids, res.ID)
				titles[res.ID] = res.Title
			}
			verdicts[i][res.ID] = res.Verdict
		}
	}

	header := []string{"Check"}
	rule := []string{"---"}
	for _, r := range reports {
		header = append(header, escape(r.Server))
		rule = append(rule, "---")
	}
	fmt.Fprintf(w, "| %s |\n|%s|\n", strings.Join(header, " | "), strings.Join(rule, "|"))
	for _, id := range ids {
		row := []string{fmt.Sprintf("`%s` %s", id, escape(titles[id]))}
		for i := range reports {
			row = append(row, cells[verdicts[i][id]])
		}
		fmt.Fprintf(w, "| %s |\n", strings.Join(row, " | "))
	}

	totals := []string{"**Passed**"}
	for i := range reports {
		passed, ran := 0, 0
		for _, v := range verdicts[i] {
			if v == check.Skipped {
				continue
			}
			ran++
			if v == check.Pass {
				passed++
			}
		}
		totals = append(totals, fmt.Sprintf("%d/%d", passed, ran))
	}
	fmt.Fprintf(w, "| %s |\n", strings.Join(totals, " | "))
}

func escape(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}
