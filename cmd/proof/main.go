// Command proof checks an nREPL server's compatibility with the clients
// people actually use, and checks clients against servers.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/internal/checks"
	"github.com/nrepl/proof/internal/profile"
	"github.com/nrepl/proof/internal/report"
	"github.com/nrepl/proof/internal/serve"
	"github.com/nrepl/proof/internal/server"
)

const version = "0.1.0-dev"

const usage = `proof checks an nREPL server's compatibility with existing clients.

Usage:
  proof run [flags] PROFILE           run the checks against the server a profile describes
  proof proxy [flags] [PROFILE]       check what a client sends to a server, by sitting between them
  proof serve [flags] [SCENARIO...]   be a server for a client's tests, acting like other servers where asked
  proof matrix REPORT...              build a Markdown compatibility matrix from JSON reports
  proof list                          list every check, rule and scenario
  proof version

The exit status of run is 0 when everything passed (or failed as the
profile expects), 1 when the server failed checks, 2 when proof couldn't
start (bad flags or profile, or the server didn't come up), and 3 when some
checks couldn't run at all. The same goes for proxy and serve, where 1
means the client failed rules and 3 means no client sent anything.

Run flags:
`

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		os.Exit(run(os.Args[2:]))
	case "proxy":
		os.Exit(runProxy(os.Args[2:]))
	case "serve":
		os.Exit(runServe(os.Args[2:]))
	case "matrix":
		os.Exit(matrix(os.Args[2:]))
	case "list":
		list(os.Stdout)
	case "version", "--version", "-version":
		fmt.Println("proof", version)
	case "help", "-h", "--help":
		printUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "proof: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, usage)
	runFlags(w).PrintDefaults()
	fmt.Fprint(w, "\nProxy flags:\n")
	proxyFlags(w, &proxyOptions{}).PrintDefaults()
	fmt.Fprint(w, "\nServe flags:\n")
	serveFlags(w, &serveOptions{}).PrintDefaults()
}

type options struct {
	address string
	json    string
	only    string
	verbose bool
}

var opts options

func runFlags(out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&opts.address, "address", "", "connect to a server already running at `host:port` instead of launching one")
	fs.StringVar(&opts.json, "json", "", "also write a JSON report to `file`")
	fs.StringVar(&opts.only, "only", "", "run only checks whose id matches the glob `pattern` (e.g. 'eval.*')")
	fs.BoolVar(&opts.verbose, "v", false, "show notes, and transcripts for checks that didn't pass")
	return fs
}

func run(args []string) int {
	fs := runFlags(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "proof run: expected one profile")
		return 2
	}
	p, err := profile.Load(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "proof:", err)
		return 2
	}

	// Catch mistakes in the profile or flags before paying for a server
	// start, which can take minutes.
	if err := check.UnknownIDs(p.ExpectedFailures, allIDs()); err != nil {
		fmt.Fprintf(os.Stderr, "proof: %s: %v\n", fs.Arg(0), err)
		return 2
	}
	selected, err := filter(checks.All(), opts.only)
	if err != nil {
		fmt.Fprintln(os.Stderr, "proof:", err)
		return 2
	}

	addr := opts.address
	if addr == "" {
		addr = p.Address
	}
	var srv *server.Server
	if addr == "" {
		fmt.Fprintf(os.Stderr, "Starting %s...\n", p.Name)
		srv, err = startServer(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, "proof:", err)
			return 2
		}
		defer srv.Stop()
		addr = srv.Addr
	}

	env := &check.Env{Profile: p, Addr: addr}
	started := time.Now()
	results := check.Run(env, selected, checks.WireRules())
	outcome := check.Apply(results, p.ExpectedFailures, opts.only == "")

	r := report.Run{Proof: version, Server: p.Name, Address: addr, Started: started, Results: results}
	report.Text(os.Stdout, r, opts.verbose)
	for _, id := range outcome.Stale {
		fmt.Printf("%s is listed in expected-failures but didn't fail; remove it from the profile\n", id)
	}
	showDeath(os.Stdout, srv)
	if opts.json != "" {
		if err := writeJSON(opts.json, r); err != nil {
			fmt.Fprintln(os.Stderr, "proof:", err)
			return 2
		}
	}
	switch {
	case len(outcome.Errors) > 0:
		return 3
	case !outcome.OK():
		return 1
	}
	return 0
}

// startServer launches the profile's server and makes sure it goes away
// if proof is interrupted. The server runs in its own process group, so a
// Ctrl-C in the terminal never reaches it on its own.
func startServer(p *profile.Profile) (*server.Server, error) {
	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	// srv is only read after started is closed, which orders it after the
	// assignment below.
	var srv *server.Server
	started := make(chan struct{})
	go func() {
		<-sigs
		cancel() // abandons a start that's still waiting for the port
		<-started
		if srv != nil {
			srv.Stop()
		}
		os.Exit(130)
	}()
	var err error
	srv, err = server.Start(ctx, p)
	close(started)
	return srv, err
}

// showDeath shows the output of a server that exited while proof was
// using it, as that explains whatever went wrong afterwards.
func showDeath(w io.Writer, srv *server.Server) {
	if srv != nil && !srv.Alive() {
		fmt.Fprintf(w, "\nThe server exited during the run. Its output:\n%s", srv.Output())
	}
}

func writeJSON(path string, r report.Run) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := report.JSON(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

type entry struct {
	id, title string
	severity  check.Severity
}

// catalog lists every check and wire rule, i.e. everything a server
// profile can expect to fail.
func catalog() []entry {
	var all []entry
	for _, c := range checks.All() {
		all = append(all, entry{c.ID, c.Title, c.Severity})
	}
	return append(all, ruleEntries(checks.WireRules())...)
}

func ruleEntries(rules []*check.Rule) []entry {
	var entries []entry
	for _, r := range rules {
		entries = append(entries, entry{r.ID, r.Title, r.Severity})
	}
	return entries
}

func allIDs() []string {
	var ids []string
	for _, e := range catalog() {
		ids = append(ids, e.id)
	}
	return ids
}

func matrix(paths []string) int {
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "proof matrix: expected one or more JSON reports (from proof run -json)")
		return 2
	}
	var reports []report.Report
	for _, path := range paths {
		r, err := report.ReadJSON(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "proof:", err)
			return 2
		}
		reports = append(reports, r)
	}
	report.Matrix(os.Stdout, reports)
	return 0
}

func filter(all []*check.Check, pattern string) ([]*check.Check, error) {
	if pattern == "" {
		return all, nil
	}
	var out []*check.Check
	for _, c := range all {
		ok, err := path.Match(pattern, c.ID)
		if err != nil {
			return nil, fmt.Errorf("-only: %w", err)
		}
		if ok {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("-only %q matches no checks", pattern)
	}
	return out, nil
}

// The names in proof list take up this many columns, and its wrapped lines
// stay within lineWidth.
const nameWidth, lineWidth = 28, 100

func list(w io.Writer) {
	for _, e := range append(catalog(), ruleEntries(checks.ClientRules())...) {
		fmt.Fprintf(w, "%-*s %-4s %s\n", nameWidth, e.id, e.severity, e.title)
	}
	fmt.Fprint(w, "\nScenarios for proof serve:\n")
	for _, s := range serve.Scenarios() {
		fmt.Fprintf(w, "%-*s %s (%s)\n", nameWidth, s.Name, s.Title, s.Who)
	}
	fmt.Fprint(w, "\nServers for proof serve -like, with their scenarios:\n")
	for _, p := range serve.Presets() {
		line := fmt.Sprintf("%-*s", nameWidth, p.Name)
		for i, s := range p.Scenarios {
			if i > 0 {
				line += ","
				if len(line)+len(" "+s+",") > lineWidth {
					fmt.Fprintln(w, line)
					line = strings.Repeat(" ", nameWidth)
				}
			}
			line += " " + s
		}
		fmt.Fprintln(w, line)
	}
}
