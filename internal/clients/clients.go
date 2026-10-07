// Package clients accepts the connections of nREPL clients and records
// everything said on them, so the commands that check clients can grade
// their requests afterwards.
package clients

import (
	"errors"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

// Listener accepts clients and numbers their connections. The transcripts
// are from the client's point of view: requests are Sent and replies are
// Received.
type Listener struct {
	ln net.Listener
	// Logf, if set before Serve, is told about connections coming and
	// going.
	Logf func(format string, args ...any)

	mu      sync.Mutex
	conns   []*Conn
	stopped bool
}

// Conn is a client's connection.
type Conn struct {
	// N numbers the connections in the order they were accepted, from 1.
	N      int
	Client net.Conn
	l      *Listener
	// gone is closed once the client's side of the connection is over,
	// and done once its handler has returned.
	gone, done chan struct{}
	goneOnce   sync.Once
	// also are closed along with Client when proof stops.
	also []net.Conn

	mu     sync.Mutex
	events []nrepl.Event
}

// Listen starts accepting clients on addr. Call Serve to handle them.
func Listen(addr string) (*Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return New(ln), nil
}

// New accepts clients from ln.
func New(ln net.Listener) *Listener { return &Listener{ln: ln} }

// Addr is the address clients should connect to.
func (l *Listener) Addr() string { return l.ln.Addr().String() }

// Log passes a message on to Logf.
func (l *Listener) Log(format string, args ...any) {
	if l.Logf != nil {
		l.Logf(format, args...)
	}
}

// Serve accepts clients until Stop is called, and handles each of them in
// a goroutine of its own. The connection is over (and closed) once handle
// returns.
func (l *Listener) Serve(handle func(*Conn)) {
	var delay time.Duration
	for {
		nc, err := l.ln.Accept()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			// Most likely out of file descriptors, which passes as the
			// clients hang up.
			delay = min(max(2*delay, 5*time.Millisecond), time.Second)
			l.Log("Couldn't accept a client: %v", err)
			time.Sleep(delay)
			continue
		}
		delay = 0
		l.mu.Lock()
		if l.stopped {
			l.mu.Unlock()
			nc.Close()
			return
		}
		c := &Conn{N: len(l.conns) + 1, Client: nc, l: l, gone: make(chan struct{}), done: make(chan struct{})}
		l.conns = append(l.conns, c)
		l.mu.Unlock()
		go func() {
			defer close(c.done)
			defer c.ClientGone()
			defer nc.Close()
			handle(c)
		}()
	}
}

// Stop stops accepting clients. Clients that are still connected get up
// to grace to hang up on their own (e.g. when a test suite has just
// finished), and are then disconnected. Stop returns once every handler
// has.
func (l *Listener) Stop(grace time.Duration) {
	l.mu.Lock()
	l.stopped = true
	conns := l.conns
	l.mu.Unlock()
	l.ln.Close()

	deadline := time.NewTimer(grace)
	defer deadline.Stop()
wait:
	for _, c := range conns {
		select {
		case <-c.gone:
		case <-deadline.C:
			break wait
		}
	}
	l.mu.Lock()
	for _, c := range conns {
		select {
		case <-c.gone:
		default:
			l.Log("Connection %d was still open", c.N)
		}
		c.Client.Close()
		for _, nc := range c.also {
			nc.Close()
		}
	}
	l.mu.Unlock()
	for _, c := range conns {
		<-c.done
	}
}

// Conns returns every connection so far, in the order they were opened.
func (l *Listener) Conns() []*Conn {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.conns)
}

// Traffic returns the transcript of every connection in which the client
// sent something, labelled with the numbers the log uses. Connections that
// only probed the port (e.g. a CI script waiting for proof to start) have
// nothing to check.
func (l *Listener) Traffic() []check.Traffic {
	var traffic []check.Traffic
	for _, c := range l.Conns() {
		events := c.Events()
		if slices.ContainsFunc(events, func(ev nrepl.Event) bool { return ev.Dir == nrepl.Sent && !ev.Closed }) {
			traffic = append(traffic, check.Traffic{Label: "connection " + strconv.Itoa(c.N), Events: events})
		}
	}
	return traffic
}

// Record adds an event to the connection's transcript.
func (c *Conn) Record(ev nrepl.Event) {
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
}

// Events returns the transcript so far.
func (c *Conn) Events() []nrepl.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.events)
}

// ClientGone marks the client's side of the connection as over, while the
// handler may still be busy with the rest (e.g. with a server that's slow
// to hang up). Serve does this anyway once the handler returns.
func (c *Conn) ClientGone() { c.goneOnce.Do(func() { close(c.gone) }) }

// Gone is closed once the client's side of the connection is over.
func (c *Conn) Gone() <-chan struct{} { return c.gone }

// Done is closed once the connection is over.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Also makes Stop close nc along with the client's connection. If proof is
// stopping already, it closes nc right away and reports false.
func (c *Conn) Also(nc net.Conn) bool {
	c.l.mu.Lock()
	defer c.l.mu.Unlock()
	if c.l.stopped {
		nc.Close()
		return false
	}
	c.also = append(c.also, nc)
	return true
}
