package proxy

import (
	"bytes"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

// upstream hands every connection it accepts to serve.
func upstream(t *testing.T, serve func(net.Conn)) string {
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
				serve(c)
			}()
		}
	}()
	return ln.Addr().String()
}

// sendAndHangUp sends data through p and returns whatever comes back
// before the proxy hangs up too.
func sendAndHangUp(t *testing.T, p *Proxy, data string) []byte {
	t.Helper()
	c, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte(data))
	c.(*net.TCPConn).CloseWrite()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	back, _ := io.ReadAll(c)
	return back
}

// start starts a proxy to addr, and returns it along with what it logged
// so far.
func start(t *testing.T, addr string) (*Proxy, func() string) {
	t.Helper()
	p, err := Listen("127.0.0.1:0", addr)
	if err != nil {
		t.Fatal(err)
	}
	logged := logTo(p)
	go p.Serve()
	t.Cleanup(func() { p.Stop(0) })
	return p, logged
}

// within waits for ch, and fails the test if that takes too long.
func within(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// firstConn returns the first connection p accepted.
func firstConn(t *testing.T, p *Proxy) *conn {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.mu.Lock()
		conns := p.conns
		p.mu.Unlock()
		if len(conns) > 0 {
			return conns[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for a connection")
		}
		time.Sleep(time.Millisecond)
	}
}

// logTo collects what p logs.
func logTo(p *Proxy) func() string {
	var logged bytes.Buffer
	var mu sync.Mutex
	p.Logf = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged.WriteString(format + "\n")
	}
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return logged.String()
	}
}

func describe(events []nrepl.Event) string {
	var desc []string
	for _, ev := range events {
		d := "->"
		if ev.Dir == nrepl.Received {
			d = "<-"
		}
		switch {
		case ev.Closed:
			d += " closed"
		case ev.Err != nil:
			d += " error " + string(ev.Raw)
		default:
			d += " " + ev.Msg.String()
		}
		if len(ev.Violations) > 0 {
			d += " (not canonical)"
		}
		desc = append(desc, d)
	}
	return strings.Join(desc, "\n")
}

// Every byte has to reach the other side as it was sent, whatever the
// proxy makes of it.
func TestRelaysEveryByteAndRecordsTheFrames(t *testing.T) {
	cases := []struct {
		name, requests string
		want           []string
	}{
		{
			"unsorted keys, then a frame that can't be decoded",
			"d2:op8:describe2:id1:1e" + "d2:id1:22:op4:evale" + "hello",
			[]string{`-> {id "1", op "describe"} (not canonical)`, `-> {id "2", op "eval"}`, `-> error `, `-> closed`},
		},
		{
			"a frame cut off in the middle of a string",
			"d2:id1:12:op8:describee" + "d4:code10:(+ 1",
			[]string{`-> {id "1", op "describe"}`, `-> error d4:code10:(+ 1`, `-> closed`},
		},
	}
	replies := "d2:id1:16:statusl4:doneee"
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := make(chan []byte, 1)
			p, _ := start(t, upstream(t, func(c net.Conn) {
				b, _ := io.ReadAll(c)
				got <- b
				c.Write([]byte(replies))
			}))

			back := sendAndHangUp(t, p, c.requests)
			if b := <-got; string(b) != c.requests {
				t.Errorf("server got %q, want %q", b, c.requests)
			}
			if string(back) != replies {
				t.Errorf("client got %q, want %q", back, replies)
			}
			traffic := p.Stop(time.Second)
			if len(traffic) != 1 || traffic[0].Label != "connection 1" {
				t.Fatalf("got %v", traffic)
			}
			want := strings.Join(append(c.want, `<- {id "1", status ["done"]}`, `<- closed`), "\n")
			if got := describe(traffic[0].Events); got != want {
				t.Errorf("transcript:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// A client that closes its socket with replies it hasn't read resets the
// connection, which is still the client hanging up.
func TestResetCountsAsHangingUp(t *testing.T) {
	cases := []struct {
		name, requests string
		want           string
	}{
		{"after a request", "d2:id1:12:op8:describee", `-> {id "1", op "describe"}`},
		{"after a frame that can't be decoded", "hello", `-> error `},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			received := make(chan struct{})
			p, _ := start(t, upstream(t, func(conn net.Conn) {
				io.ReadFull(conn, make([]byte, len(c.requests)))
				conn.Write([]byte("d2:id1:16:statusl4:doneee"))
				close(received)
				io.Copy(io.Discard, conn)
			}))
			nc, err := net.Dial("tcp", p.Addr())
			if err != nil {
				t.Fatal(err)
			}
			nc.Write([]byte(c.requests))
			within(t, received, "the server to get the requests")
			nc.(*net.TCPConn).SetLinger(0)
			nc.Close()

			traffic := p.Stop(time.Second)
			if len(traffic) != 1 {
				t.Fatalf("got %v", traffic)
			}
			// The reply may or may not be recorded before the reset.
			if got := describe(traffic[0].Events); !strings.HasPrefix(got, c.want+"\n") || !strings.Contains(got, "-> closed") {
				t.Errorf("transcript:\n%s\nwant %s, then the hang-up", got, c.want)
			}
		})
	}
}

// Once the client is gone, the server's writes fail as they would
// without proof in between, and both sockets are closed.
func TestBothSocketsAreClosedOnceTheClientIsGone(t *testing.T) {
	writeFailed := make(chan struct{})
	p, _ := start(t, upstream(t, func(c net.Conn) {
		io.Copy(io.Discard, c)
		// Until the proxy (or the end of the test) closes the connection.
		for {
			if _, err := c.Write([]byte("d2:id1:16:statusl4:doneee")); err != nil {
				close(writeFailed)
				return
			}
		}
	}))
	nc, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatal(err)
	}
	nc.Write([]byte("d2:id1:12:op8:describee"))
	nc.Close()

	c := firstConn(t, p)
	// Stop would close the sockets itself, so the connection has to end
	// on its own.
	within(t, c.done, "the connection to end")
	within(t, writeFailed, "the server's writes to fail")
	for name, nc := range map[string]net.Conn{"client": c.client, "server": c.server} {
		if err := nc.SetDeadline(time.Time{}); !errors.Is(err, net.ErrClosed) {
			t.Errorf("the %s socket is still open", name)
		}
	}
	if got, want := describe(c.events), "-> {id \"1\", op \"describe\"}\n-> closed"; !strings.HasPrefix(got, want) {
		t.Errorf("transcript:\n%s\nwant it to start with:\n%s", got, want)
	}
}

// When the client leaves while its requests are still on their way, and
// the server finds out by writing to it, the server's connection is
// closed, as it would be without proof in between. The client's hang-up
// still makes it into the transcript.
func TestClientHangingUpWhileItsRequestsAreOnTheirWay(t *testing.T) {
	p, _ := start(t, upstream(t, func(c net.Conn) {
		// Replies for a client that's gone by now, while the rest of the
		// request waits, so it's still on its way when the proxy finds
		// the client gone.
		c.Read(make([]byte, 64<<10))
		// Until the proxy (or the end of the test) closes the connection.
		for {
			if _, err := c.Write([]byte("d2:id1:16:statusl4:doneee")); err != nil {
				return
			}
		}
	}))
	nc, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatal(err)
	}
	// More than the socket buffers on the way to the server hold.
	request := bytes.Repeat([]byte("x"), len("d4:code16777216:")+16<<20+1)
	copy(request, "d4:code16777216:")
	request[len(request)-1] = 'e'
	nc.Write(request)
	nc.Close()

	c := firstConn(t, p)
	within(t, c.done, "the connection to end")
	if !slices.ContainsFunc(c.events, func(ev nrepl.Event) bool { return ev.Dir == nrepl.Sent && ev.Closed }) {
		t.Errorf("no hang-up in the transcript:\n%s", describe(c.events[1:]))
	}
}

// A server that hangs up its end and stops reading would leave the proxy
// stuck forwarding requests to it.
func TestServerHangingUpFreesAStuckClient(t *testing.T) {
	stuck, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	p, logged := start(t, upstream(t, func(c net.Conn) {
		select {
		case <-stuck:
		case <-release:
		}
		c.(*net.TCPConn).CloseWrite()
		<-release
	}))
	nc, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	// Send requests until a write times out, which means the proxy is
	// stuck writing to the server.
	frame := []byte("d4:code1048576:" + strings.Repeat("x", 1<<20) + "e")
	for {
		nc.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
		if _, err := nc.Write(frame); err != nil {
			break
		}
	}
	close(stuck)
	nc.SetReadDeadline(time.Now().Add(2 * time.Second))
	io.Copy(io.Discard, nc)
	stopPromptly(t, p, logged)
}

// When the server hangs up first, the client is disconnected right away,
// and that doesn't count as the client hanging up.
func TestServerHangingUpFirst(t *testing.T) {
	reply := "d2:id1:16:statusl4:doneee"
	p, logged := start(t, upstream(t, func(c net.Conn) {
		c.Read(make([]byte, 64))
		c.Write([]byte(reply))
	}))

	nc, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	nc.Write([]byte("d2:id1:12:op8:describee"))
	nc.SetReadDeadline(time.Now().Add(2 * time.Second))
	if back, err := io.ReadAll(nc); err != nil || string(back) != reply {
		t.Fatalf("client got %q (%v), want %q and then the end", back, err, reply)
	}

	traffic := stopPromptly(t, p, logged)
	if len(traffic) != 1 {
		t.Fatalf("got %v", traffic)
	}
	want := "-> {id \"1\", op \"describe\"}\n<- {id \"1\", status [\"done\"]}\n<- closed"
	if got := describe(traffic[0].Events); got != want {
		t.Errorf("transcript:\n%s\nwant:\n%s", got, want)
	}
}

// stopPromptly stops p, which shouldn't have to wait for connections that
// are already over.
func stopPromptly(t *testing.T, p *Proxy, logged func() string) []check.Traffic {
	t.Helper()
	started := time.Now()
	traffic := p.Stop(5 * time.Second)
	if d := time.Since(started); d > time.Second {
		t.Errorf("Stop waited %s for a connection that was over", d)
	}
	if strings.Contains(logged(), "still open") {
		t.Errorf("log:\n%s", logged())
	}
	return traffic
}

func TestStopDisconnectsClientsStillConnected(t *testing.T) {
	received := make(chan struct{})
	p, logged := start(t, upstream(t, func(c net.Conn) {
		c.Read(make([]byte, 64))
		close(received)
		io.Copy(io.Discard, c)
	}))

	c, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("d2:op8:describee"))
	within(t, received, "the server to get the request")

	traffic := p.Stop(10 * time.Millisecond)
	if len(traffic) != 1 || len(traffic[0].Events) != 1 || traffic[0].Events[0].Closed {
		t.Errorf("want just the request, got %v", traffic)
	}
	c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("client read: %v, want EOF", err)
	}
	if log := logged(); !strings.Contains(log, "was still open") || strings.Contains(log, "closed") {
		t.Errorf("log should say the connection was still open, and only that:\n%s", log)
	}
}

// Connections that never send anything (e.g. a script checking whether the
// port is open yet) aren't clients, but they keep their number so the
// report matches the log.
func TestConnectionsThatSendNothingAreLeftOut(t *testing.T) {
	p, _ := start(t, upstream(t, func(c net.Conn) { io.Copy(io.Discard, c) }))
	for _, frame := range []string{"", "d2:op8:describee"} {
		sendAndHangUp(t, p, frame)
	}
	traffic := p.Stop(time.Second)
	if len(traffic) != 1 || traffic[0].Label != "connection 2" {
		t.Errorf("got %v, want just connection 2", traffic)
	}
}

func TestUnreachableServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	p, _ := start(t, addr)

	c, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("client read: %v, want EOF", err)
	}
	if tr := p.Stop(time.Second); len(tr) != 0 {
		t.Errorf("got transcripts for a connection that never reached the server: %v", tr)
	}
}

// failOnce is a listener whose first Accept fails, the way it does when
// proof runs out of file descriptors.
type failOnce struct {
	net.Listener
	failed bool
}

func (l *failOnce) Accept() (net.Conn, error) {
	if !l.failed {
		l.failed = true
		return nil, errors.New("too many open files")
	}
	return l.Listener.Accept()
}

func TestKeepsAcceptingAfterAnError(t *testing.T) {
	addr := upstream(t, func(c net.Conn) {
		c.Read(make([]byte, 64))
		c.Write([]byte("d2:id1:16:statusl4:doneee"))
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &Proxy{upstream: addr, ln: &failOnce{Listener: ln}}
	logged := logTo(p)
	go p.Serve()
	t.Cleanup(func() { p.Stop(0) })

	c, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("d2:id1:12:op8:describee"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err != nil {
		t.Errorf("no reply: %v", err)
	}
	if !strings.Contains(logged(), "Couldn't accept a client") {
		t.Errorf("the error wasn't logged:\n%s", logged())
	}
}

type failing struct{}

func (failing) Write(p []byte) (int, error) { return 0, errors.New("the other side is gone") }

// A write that fails because the other side is gone isn't src hanging up,
// whether it's a frame or what comes after one that can't be decoded.
func TestFailedWritesAreNotHangUps(t *testing.T) {
	for _, data := range []string{"d2:op8:describee", "hello"} {
		client, proxied := net.Pipe()
		go client.Write([]byte(data))
		c := &conn{}
		if end := c.relay(proxied, failing{}, nrepl.Sent); end != dstGone {
			t.Errorf("%q: ended with %d, want dstGone", data, end)
		}
		if strings.Contains(describe(c.events), "closed") {
			t.Errorf("%q: transcript:\n%s", data, describe(c.events))
		}
		client.Close()
		proxied.Close()
	}
}
