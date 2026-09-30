// Package server launches an nREPL server as a subprocess and finds the
// port it listens on.
package server

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nrepl/proof/internal/profile"
)

// Server is a running server process.
type Server struct {
	Addr  string
	cmd   *exec.Cmd
	dir   string
	stdin io.Closer

	mu     sync.Mutex
	output bytes.Buffer
	exited chan struct{}
}

const maxOutput = 64 << 10

// Start launches the server described by p and waits for its port. If ctx
// is cancelled first, the server is stopped again.
//
// The server runs in a fresh temporary directory, so port files and other
// droppings don't end up wherever proof was started.
func Start(ctx context.Context, p *profile.Profile) (*Server, error) {
	pattern, err := regexp.Compile(p.Launch.PortPattern)
	if err != nil {
		return nil, fmt.Errorf("launch.port-pattern: %w", err)
	}
	portGroup := 1
	if i := pattern.SubexpIndex("port"); i > 0 {
		portGroup = i
	}
	if pattern.NumSubexp() < portGroup {
		return nil, fmt.Errorf("launch.port-pattern needs a group capturing the port")
	}

	dir, err := os.MkdirTemp("", "proof-server-")
	if err != nil {
		return nil, err
	}
	argv := p.Command()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range p.Launch.Env {
		cmd.Env = append(cmd.Env, k+"="+os.ExpandEnv(v))
	}
	setProcessGroup(cmd)
	// If something the server spawned outside its process group keeps
	// stdout open, don't wait on it forever once the server has exited.
	cmd.WaitDelay = 2 * time.Second
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	// Servers that come with a terminal REPL (jank's, for one) quit when
	// stdin hits EOF, so hold it open until Stop.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("starting %s: %w", strings.Join(argv, " "), err)
	}

	s := &Server{cmd: cmd, dir: dir, stdin: stdin, exited: make(chan struct{})}
	go func() {
		cmd.Wait()
		pw.Close()
		close(s.exited)
	}()

	ports := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		found := false
		for sc.Scan() {
			line := sc.Text()
			s.appendOutput(line)
			if !found {
				if m := pattern.FindStringSubmatch(line); m != nil {
					found = true
					ports <- m[portGroup]
				}
			}
		}
		// Keep draining, or a chatty server blocks on a full pipe.
		io.Copy(io.Discard, pr)
	}()

	select {
	case port := <-ports:
		s.Addr = net.JoinHostPort(p.Launch.Host, port)
		return s, nil
	case <-s.exited:
		s.Stop()
		return nil, fmt.Errorf("server exited before printing its port:\n%s", s.Output())
	case <-ctx.Done():
		s.Stop()
		return nil, ctx.Err()
	case <-time.After(p.Launch.StartupTimeout):
		s.Stop()
		return nil, fmt.Errorf("no line matching %q within %s:\n%s", p.Launch.PortPattern, p.Launch.StartupTimeout, s.Output())
	}
}

func (s *Server) appendOutput(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.output.Len() < maxOutput {
		s.output.WriteString(line)
		s.output.WriteByte('\n')
	}
}

// Output is what the server has printed so far (capped).
func (s *Server) Output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.output.String()
}

// Alive reports whether the process is still running.
func (s *Server) Alive() bool {
	select {
	case <-s.exited:
		return false
	default:
		return true
	}
}

// Stop terminates the server and everything it spawned.
func (s *Server) Stop() {
	s.stdin.Close()
	if s.Alive() {
		terminate(s.cmd)
		select {
		case <-s.exited:
		case <-time.After(5 * time.Second):
			kill(s.cmd)
			<-s.exited
		}
	}
	os.RemoveAll(s.dir)
}
