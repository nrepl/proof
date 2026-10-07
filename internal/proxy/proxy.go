// Package proxy relays connections from nREPL clients to a server and
// records everything that passes through, so the requests of a client can
// be graded afterwards.
package proxy

import (
	"bufio"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/nrepl/proof/bencode"
	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

// Proxy accepts clients and connects each of them to the upstream server.
// The transcripts are from the client's point of view: requests are Sent
// and replies are Received.
type Proxy struct {
	upstream string
	ln       net.Listener
	// Logf, if set before Serve, is told about connections coming and
	// going.
	Logf func(format string, args ...any)

	mu      sync.Mutex
	conns   []*conn
	stopped bool
}

type conn struct {
	n              int
	client, server net.Conn
	// clientDone is closed once the client's side of the connection has
	// ended (or never got going).
	clientDone chan struct{}
	// done is closed once the connection is over and both sockets are
	// closed.
	done chan struct{}

	mu     sync.Mutex
	events []nrepl.Event
}

// Listen starts accepting clients on addr. Call Serve to handle them.
func Listen(addr, upstream string) (*Proxy, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Proxy{upstream: upstream, ln: ln}, nil
}

// Addr is the address clients should connect to.
func (p *Proxy) Addr() string { return p.ln.Addr().String() }

func (p *Proxy) logf(format string, args ...any) {
	if p.Logf != nil {
		p.Logf(format, args...)
	}
}

// Serve accepts clients until Stop is called.
func (p *Proxy) Serve() {
	var delay time.Duration
	for {
		nc, err := p.ln.Accept()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			// Most likely out of file descriptors, which passes as the
			// clients hang up.
			delay = min(max(2*delay, 5*time.Millisecond), time.Second)
			p.logf("Couldn't accept a client: %v", err)
			time.Sleep(delay)
			continue
		}
		delay = 0
		p.mu.Lock()
		if p.stopped {
			p.mu.Unlock()
			nc.Close()
			return
		}
		c := &conn{n: len(p.conns) + 1, client: nc, clientDone: make(chan struct{}), done: make(chan struct{})}
		p.conns = append(p.conns, c)
		p.mu.Unlock()
		go p.handle(c)
	}
}

func (p *Proxy) handle(c *conn) {
	defer close(c.done)
	server, err := net.DialTimeout("tcp", p.upstream, 5*time.Second)
	p.mu.Lock()
	if err == nil && p.stopped {
		server.Close()
		err = errors.New("proof is stopping")
	}
	if err == nil {
		c.server = server
	}
	p.mu.Unlock()
	if err != nil {
		p.logf("Connection %d: couldn't connect to %s: %v", c.n, p.upstream, err)
		c.client.Close()
		close(c.clientDone)
		return
	}
	p.logf("Connection %d opened", c.n)

	go func() {
		defer close(c.clientDone)
		end := c.relay(c.client, c.server, nrepl.Sent)
		if end == dstGone {
			// The server is gone, but if the client hangs up before proof
			// cuts it off, that still counts.
			end = c.passOn(c.client, io.Discard, nrepl.Sent)
		}
		// Some servers never hang up their end, so this is when the
		// connection is over as far as the client is concerned.
		if end == hungUp {
			p.logf("Connection %d closed", c.n)
			// Let the server see the end of the requests, while any
			// replies still on their way can reach the client.
			closeWrite(c.server)
		}
	}()
	switch c.relay(c.server, c.client, nrepl.Received) {
	case hungUp:
		p.logf("Connection %d closed by the server", c.n)
	case dstGone:
		// The client is gone, and its side of the relay records that.
		// The server finds out the way it would without proof in between.
		c.server.Close()
		<-c.clientDone
	}
	// Without a server there's nothing more for the client to do, and
	// nothing it still sends can get anywhere. (Or proof is stopping.)
	c.client.Close()
	c.server.Close()
	<-c.clientDone
}

// ending says how a relay ended.
type ending int

const (
	hungUp  ending = iota // src closed the connection, or reset it
	dstGone               // writing to dst failed
	stopped               // proof closed the connection
)

// relay passes frames from src to dst exactly as they arrived, recording
// each of them (and src hanging up), and says how it ended. After a frame
// that can't be decoded the rest of the stream is passed on without being
// recorded, as there's no telling where the next frame starts.
func (c *conn) relay(src net.Conn, dst io.Writer, dir nrepl.Direction) ending {
	br := bufio.NewReader(src)
	dec := bencode.NewDecoder(br)
	for {
		v, err := dec.Decode()
		ev, isFrame := nrepl.DecodeEvent(dir, v, err)
		if isFrame {
			c.record(ev)
			if len(v.Raw) > 0 {
				if _, werr := dst.Write(v.Raw); werr != nil {
					return dstGone
				}
			}
		}
		switch {
		case err == nil:
			continue
		case isFrame:
			return c.passOn(br, dst, dir)
		}
		return c.ended(dir, err)
	}
}

// passOn copies the rest of src to dst as it is, and says how that ended.
func (c *conn) passOn(src io.Reader, dst io.Writer, dir nrepl.Direction) ending {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return dstGone
			}
		}
		if err != nil {
			return c.ended(dir, err)
		}
	}
}

// ended records src hanging up, unless it was proof that closed it.
func (c *conn) ended(dir nrepl.Direction, err error) ending {
	if errors.Is(err, net.ErrClosed) {
		return stopped
	}
	// The end of the stream, or a reset (e.g. from a client that closed
	// its socket with replies it hadn't read yet).
	c.record(nrepl.Event{Dir: dir, Time: time.Now(), Closed: true})
	return hungUp
}

func (c *conn) record(ev nrepl.Event) {
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
}

func closeWrite(nc net.Conn) {
	if tc, ok := nc.(*net.TCPConn); ok {
		tc.CloseWrite()
	}
}

// Stop stops accepting clients and returns the transcript of every
// connection in which the client sent something, in the order they were
// opened and labelled with the numbers the log uses. Clients that are
// still connected get up to grace to hang up on their own (e.g. when a
// test suite has just finished), and are then disconnected.
func (p *Proxy) Stop(grace time.Duration) []check.Traffic {
	p.mu.Lock()
	p.stopped = true
	conns := p.conns
	p.mu.Unlock()
	p.ln.Close()

	deadline := time.NewTimer(grace)
	defer deadline.Stop()
wait:
	for _, c := range conns {
		select {
		case <-c.clientDone:
		case <-deadline.C:
			break wait
		}
	}
	p.mu.Lock()
	for _, c := range conns {
		select {
		case <-c.clientDone:
		default:
			p.logf("Connection %d was still open", c.n)
		}
		c.client.Close()
		if c.server != nil {
			c.server.Close()
		}
	}
	p.mu.Unlock()
	for _, c := range conns {
		<-c.done
	}

	var traffic []check.Traffic
	for _, c := range conns {
		c.mu.Lock()
		if sentAnything(c.events) {
			traffic = append(traffic, check.Traffic{Label: "connection " + strconv.Itoa(c.n), Events: c.events})
		}
		c.mu.Unlock()
	}
	return traffic
}

// sentAnything reports whether the client sent anything. Connections that
// only probed the port (e.g. a CI script waiting for proof to start) have
// nothing to check.
func sentAnything(events []nrepl.Event) bool {
	for _, ev := range events {
		if ev.Dir == nrepl.Sent && !ev.Closed {
			return true
		}
	}
	return false
}
