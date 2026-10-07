// Package proxy relays connections from nREPL clients to a server and
// records everything that passes through, so the requests of a client can
// be graded afterwards.
package proxy

import (
	"bufio"
	"errors"
	"io"
	"net"
	"time"

	"github.com/nrepl/proof/bencode"
	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/internal/clients"
	"github.com/nrepl/proof/nrepl"
)

// Proxy accepts clients and connects each of them to the upstream server.
type Proxy struct {
	*clients.Listener
	upstream string
}

// conn is a client's connection, which the relays record.
type conn struct{ *clients.Conn }

// Listen starts accepting clients on addr. Call Serve to handle them.
func Listen(addr, upstream string) (*Proxy, error) {
	l, err := clients.Listen(addr)
	if err != nil {
		return nil, err
	}
	return &Proxy{Listener: l, upstream: upstream}, nil
}

// Serve accepts clients until Stop is called.
func (p *Proxy) Serve() { p.Listener.Serve(p.handle) }

// Stop stops accepting clients and returns the transcript of every
// connection in which the client sent something. Clients that are still
// connected get up to grace to hang up on their own, and are then
// disconnected.
func (p *Proxy) Stop(grace time.Duration) []check.Traffic {
	p.Listener.Stop(grace)
	return p.Traffic()
}

func (p *Proxy) handle(cc *clients.Conn) {
	server, err := net.DialTimeout("tcp", p.upstream, 5*time.Second)
	if err == nil && !cc.Also(server) {
		err = errors.New("proof is stopping")
	}
	if err != nil {
		p.Log("Connection %d: couldn't connect to %s: %v", cc.N, p.upstream, err)
		cc.Client.Close()
		return
	}
	c := conn{cc}
	p.Log("Connection %d opened", cc.N)

	go func() {
		defer cc.ClientGone()
		end := c.relay(cc.Client, server, nrepl.Sent)
		if end == dstGone {
			// The server is gone, but if the client hangs up before proof
			// cuts it off, that still counts.
			end = c.passOn(cc.Client, io.Discard, nrepl.Sent)
		}
		// Some servers never hang up their end, so this is when the
		// connection is over as far as the client is concerned.
		if end == hungUp {
			p.Log("Connection %d closed", cc.N)
			// Let the server see the end of the requests, while any
			// replies still on their way can reach the client.
			closeWrite(server)
		}
	}()
	switch c.relay(server, cc.Client, nrepl.Received) {
	case hungUp:
		p.Log("Connection %d closed by the server", cc.N)
	case dstGone:
		// The client is gone, and its side of the relay records that.
		// The server finds out the way it would without proof in between.
		server.Close()
		<-cc.Gone()
	}
	// Without a server there's nothing more for the client to do, and
	// nothing it still sends can get anywhere. (Or proof is stopping.)
	cc.Client.Close()
	server.Close()
	<-cc.Gone()
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
func (c conn) relay(src net.Conn, dst io.Writer, dir nrepl.Direction) ending {
	br := bufio.NewReader(src)
	dec := bencode.NewDecoder(br)
	for {
		v, err := dec.Decode()
		ev, isFrame := nrepl.DecodeEvent(dir, v, err)
		if isFrame {
			c.Record(ev)
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
func (c conn) passOn(src io.Reader, dst io.Writer, dir nrepl.Direction) ending {
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
func (c conn) ended(dir nrepl.Direction, err error) ending {
	if errors.Is(err, net.ErrClosed) {
		return stopped
	}
	// The end of the stream, or a reset (e.g. from a client that closed
	// its socket with replies it hadn't read yet).
	c.Record(nrepl.Event{Dir: dir, Time: time.Now(), Closed: true})
	return hungUp
}

func closeWrite(nc net.Conn) {
	if tc, ok := nc.(*net.TCPConn); ok {
		tc.CloseWrite()
	}
}
