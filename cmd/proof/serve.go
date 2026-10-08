package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/nrepl/proof/internal/report"
	"github.com/nrepl/proof/internal/serve"
)

type serveOptions struct {
	clientOptions
	like string
}

func serveFlags(out io.Writer, o *serveOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&o.like, "like", "", "behave like the server whose `profile` has this name (e.g. jank or profiles/jank.toml), along with any scenarios given")
	o.register(fs)
	return fs
}

// runServe is a server for clients to run their tests against until it's
// interrupted, and then grades everything the clients sent.
func runServe(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveUntil(ctx, args, os.Stdout, os.Stderr, nil)
}

// serveUntil does the work of runServe, grading the traffic once ctx is
// done. listening, if not nil, gets the address clients should use.
func serveUntil(ctx context.Context, args []string, stdout, stderr io.Writer, listening func(addr string)) int {
	// The server logs to stderr from goroutines of its own.
	stderr = &syncWriter{w: stderr}
	var o serveOptions
	fs := serveFlags(stderr, &o)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	scenarios, shown := fs.Args(), fs.Args()
	// The flag package stops at the first scenario.
	if i := slices.IndexFunc(scenarios, func(s string) bool { return strings.HasPrefix(s, "-") }); i >= 0 {
		fmt.Fprintf(stderr, "proof: %s comes after a scenario, but flags have to go first\n", scenarios[i])
		return 2
	}
	if o.like != "" {
		name := strings.TrimSuffix(filepath.Base(o.like), ".toml")
		var err error
		if scenarios, err = serve.Like(name, scenarios); err != nil {
			fmt.Fprintln(stderr, "proof:", err)
			return 2
		}
		shown = append([]string{"like " + name}, shown...)
	}
	srv, err := serve.Listen(o.listen, version, scenarios)
	if err != nil {
		fmt.Fprintln(stderr, "proof:", err)
		return 2
	}
	srv.Logf = logTo(stderr)
	go srv.Serve()
	started := time.Now()
	name := "proof serve"
	if len(shown) > 0 {
		name += " (" + strings.Join(shown, ", ") + ")"
	}
	fmt.Fprintf(stderr, "Running %s on %s. Connect your client and press Ctrl-C when it's done.\n", name, srv.Addr())
	if listening != nil {
		listening(srv.Addr())
	}

	<-ctx.Done()
	fmt.Fprintln(stderr)
	r := report.Run{Proof: version, Server: "client traffic to " + name, Address: srv.Addr(), Started: started}
	return gradeClients(stdout, stderr, r, srv.Stop(time.Second), o.clientOptions, nil)
}
