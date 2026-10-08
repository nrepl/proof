package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/internal/checks"
	"github.com/nrepl/proof/internal/profile"
	"github.com/nrepl/proof/internal/proxy"
	"github.com/nrepl/proof/internal/report"
	"github.com/nrepl/proof/internal/server"
)

// clientOptions are the options of the commands that check clients.
type clientOptions struct {
	listen  string
	json    string
	record  string
	verbose bool
}

func (o *clientOptions) register(fs *flag.FlagSet) {
	fs.StringVar(&o.listen, "listen", "127.0.0.1:0", "accept clients on `host:port` (port 0 picks a free one)")
	fs.StringVar(&o.json, "json", "", "also write a JSON report to `file`")
	fs.StringVar(&o.record, "record", "", "also write the requests the client sent to `file`, as the start of a client profile")
	fs.BoolVar(&o.verbose, "v", false, "show every message the client and the server exchanged")
}

type proxyOptions struct {
	clientOptions
	address string
}

func proxyFlags(out io.Writer, o *proxyOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&o.address, "address", "", "forward clients to a server already running at `host:port` instead of launching the one the profile describes")
	o.register(fs)
	return fs
}

// logTo writes log messages to w, one per line.
func logTo(w io.Writer) func(format string, args ...any) {
	return func(format string, args ...any) {
		fmt.Fprintf(w, format+"\n", args...)
	}
}

// runProxy sits between a client and a server until it's interrupted, and
// then grades everything the client sent.
func runProxy(args []string) int {
	// Ctrl-C abandons a server that's still starting, and after that it
	// means the client is done. The handler stays for the rest of the run,
	// as the server is in its own process group and has to be stopped by
	// proof.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return proxyUntil(ctx, args, os.Stdout, os.Stderr, nil)
}

// proxyUntil does the work of runProxy, grading the traffic once ctx is
// done. listening, if not nil, gets the address clients should use.
func proxyUntil(ctx context.Context, args []string, stdout, stderr io.Writer, listening func(addr string)) int {
	// The proxy logs to stderr from goroutines of its own.
	stderr = &syncWriter{w: stderr}
	var o proxyOptions
	fs := proxyFlags(stderr, &o)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 || (fs.NArg() == 0 && o.address == "") {
		fmt.Fprintln(stderr, "proof proxy: expected a profile or -address")
		return 2
	}
	upstream, name := o.address, "the server"
	var p *profile.Profile
	if fs.NArg() == 1 {
		var err error
		if p, err = profile.Load(fs.Arg(0)); err != nil {
			fmt.Fprintln(stderr, "proof:", err)
			return 2
		}
		name = p.Name
		if upstream == "" {
			upstream = p.Address
		}
	}

	var srv *server.Server
	if upstream == "" {
		fmt.Fprintf(stderr, "Starting %s...\n", p.Name)
		var err error
		if srv, err = server.Start(ctx, p); err != nil {
			if ctx.Err() != nil {
				return 130
			}
			fmt.Fprintln(stderr, "proof:", err)
			return 2
		}
		defer srv.Stop()
		upstream = srv.Addr
	}
	px, err := proxy.Listen(o.listen, upstream)
	if err != nil {
		fmt.Fprintln(stderr, "proof:", err)
		return 2
	}
	px.Logf = logTo(stderr)
	go px.Serve()
	started := time.Now()
	fmt.Fprintf(stderr, "Forwarding %s to %s. Connect your client to %s and press Ctrl-C when it's done.\n",
		px.Addr(), upstream, px.Addr())
	if listening != nil {
		listening(px.Addr())
	}

	<-ctx.Done()
	fmt.Fprintln(stderr)
	r := report.Run{Proof: version, Server: "client traffic to " + name, Address: upstream, Started: started}
	return gradeClients(stdout, stderr, r, px.Stop(time.Second), o.clientOptions, srv)
}

// gradeClients grades the requests in traffic, reports on them, and
// returns the exit status. srv is the server proof started, if any.
func gradeClients(stdout, stderr io.Writer, r report.Run, traffic []check.Traffic, o clientOptions, srv *server.Server) int {
	if len(traffic) == 0 {
		fmt.Fprintln(stderr, "proof: no client sent anything to the server, so there's nothing to check")
		showDeath(stderr, srv)
		return 3
	}
	r.Results = check.Grade(checks.ClientRules(), traffic)
	report.Text(stdout, r, o.verbose)
	if o.verbose {
		for _, tr := range traffic {
			fmt.Fprintf(stdout, "\n%s:\n", tr.Label)
			report.Transcript(stdout, tr.Events, "  ")
		}
	}
	showDeath(stdout, srv)
	// A recording can't be made again, so it's written even when the
	// report can't be.
	var errs []error
	if o.json != "" {
		errs = append(errs, writeJSON(o.json, r))
	}
	if o.record != "" {
		errs = append(errs, os.WriteFile(o.record, []byte(checks.RecordedProfile(traffic)), 0o666))
	}
	if err := errors.Join(errs...); err != nil {
		fmt.Fprintln(stderr, "proof:", err)
		return 2
	}
	if r.Counts()[check.Failed] > 0 {
		return 1
	}
	return 0
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
