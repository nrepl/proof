package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nrepl/proof/bencode"
)

// doneServer answers every request with done.
func doneServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				dec := bencode.NewDecoder(bufio.NewReader(c))
				for {
					v, err := dec.Decode()
					if err != nil {
						return
					}
					reply := map[string]any{"status": []any{"done"}}
					if m, ok := v.Data.(map[string]any); ok && m["id"] != nil {
						reply["id"] = m["id"]
					}
					b, _ := bencode.Marshal(reply)
					c.Write(b)
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// command is how the tests run proof proxy and proof serve: until ctx is
// done, telling listening where clients go.
type command func(ctx context.Context, args []string, stdout, stderr io.Writer, listening func(addr string)) int

// runWithClient runs a command with a client that sends the given frames
// and hangs up, and returns the exit status and output.
func runWithClient(t *testing.T, run command, frames []string, args ...string) (int, string, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := func(addr string) {
		defer cancel()
		if len(frames) == 0 {
			return
		}
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Error(err)
			return
		}
		defer c.Close()
		for _, f := range frames {
			c.Write([]byte(f))
		}
		c.(*net.TCPConn).CloseWrite()
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		io.Copy(io.Discard, c)
	}
	var stdout, stderr bytes.Buffer
	code := run(ctx, args, &stdout, &stderr, client)
	return code, stdout.String(), stderr.String()
}

// proxyRun runs proof proxy in front of a server that answers everything
// with done.
func proxyRun(t *testing.T, frames []string, args ...string) (int, string, string) {
	t.Helper()
	return runWithClient(t, proxyUntil, frames, append([]string{"-address", doneServer(t)}, args...)...)
}

func TestProxyExitCodes(t *testing.T) {
	cases := []struct {
		name   string
		frames []string
		code   int
		output string
	}{
		{"well-behaved client", []string{"d2:id1:12:op8:describee"}, 0, "client.id"},
		{"request without an id", []string{"d2:op8:describee"}, 1, "a request without an id"},
		{"no client", nil, 3, "nothing to check"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := proxyRun(t, c.frames)
			if code != c.code {
				t.Errorf("exit status %d, want %d\nstdout:\n%s\nstderr:\n%s", code, c.code, stdout, stderr)
			}
			if !strings.Contains(stdout+stderr, c.output) {
				t.Errorf("output doesn't mention %q\nstdout:\n%s\nstderr:\n%s", c.output, stdout, stderr)
			}
		})
	}
}

func TestProxyWritesJSONAndTranscripts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	code, stdout, _ := proxyRun(t, []string{"d2:id1:12:op8:describee"}, "-v", "-json", path)
	if code != 0 {
		t.Fatalf("exit status %d:\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "connection 1:") || !strings.Contains(stdout, "closed the connection") {
		t.Errorf("no transcript in the output:\n%s", stdout)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"client.need-input"`) {
		t.Errorf("JSON report has no client rules:\n%s", b)
	}
}

func TestProxyNeedsAServer(t *testing.T) {
	var stderr bytes.Buffer
	if code := proxyUntil(context.Background(), nil, io.Discard, &stderr, nil); code != 2 {
		t.Errorf("exit status %d, want 2", code)
	}
}

func TestListShowsEverything(t *testing.T) {
	var buf bytes.Buffer
	list(&buf)
	for _, want := range []string{"eval.value", "wire.dict", "client.need-input", "split-output"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("list is missing %s:\n%s", want, buf.String())
		}
	}
}
