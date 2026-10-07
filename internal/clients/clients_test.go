package clients

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

	"github.com/nrepl/proof/nrepl"
)

// echo records what a client sends and sends it back, until the client
// hangs up.
func echo(c *Conn) {
	buf := make([]byte, 1024)
	for {
		n, err := c.Client.Read(buf)
		if n > 0 {
			c.Record(nrepl.Event{Dir: nrepl.Sent, Raw: slices.Clone(buf[:n])})
			c.Client.Write(buf[:n])
		}
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				c.Record(nrepl.Event{Dir: nrepl.Sent, Closed: true})
			}
			return
		}
	}
}

// start serves clients from ln with handle, and returns what it logged so
// far.
func start(t *testing.T, ln net.Listener, handle func(*Conn)) (*Listener, func() string) {
	t.Helper()
	l := New(ln)
	var logged bytes.Buffer
	var mu sync.Mutex
	l.Logf = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged.WriteString(format + "\n")
	}
	go l.Serve(handle)
	t.Cleanup(func() { l.Stop(0) })
	return l, func() string {
		mu.Lock()
		defer mu.Unlock()
		return logged.String()
	}
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func dial(t *testing.T, l *Listener) net.Conn {
	t.Helper()
	nc, err := net.Dial("tcp", l.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nc.Close() })
	return nc
}

// sendAndHangUp sends data and waits for the echo, which also means the
// listener has it.
func sendAndHangUp(t *testing.T, l *Listener, data string) {
	t.Helper()
	nc := dial(t, l)
	nc.Write([]byte(data))
	nc.(*net.TCPConn).CloseWrite()
	nc.SetReadDeadline(time.Now().Add(2 * time.Second))
	io.ReadAll(nc)
}

// Connections that never send anything (e.g. a script checking whether the
// port is open yet) aren't clients, but they keep their number so the
// report matches the log.
func TestConnectionsThatSendNothingAreLeftOut(t *testing.T) {
	l, _ := start(t, listen(t), echo)
	for _, data := range []string{"", "d2:op8:describee"} {
		sendAndHangUp(t, l, data)
	}
	l.Stop(time.Second)
	if traffic := l.Traffic(); len(traffic) != 1 || traffic[0].Label != "connection 2" {
		t.Errorf("got %v, want just connection 2", traffic)
	}
}

func TestStopDisconnectsClientsStillConnected(t *testing.T) {
	l, logged := start(t, listen(t), echo)
	sendAndHangUp(t, l, "1")
	still := dial(t, l)
	still.Write([]byte("2"))
	still.Read(make([]byte, 1))

	started := time.Now()
	l.Stop(50 * time.Millisecond)
	if d := time.Since(started); d > time.Second {
		t.Errorf("Stop took %s", d)
	}
	still.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := still.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("client read: %v, want EOF", err)
	}
	if log := logged(); log != "Connection %d was still open\n" {
		t.Errorf("only connection 2 should've been still open:\n%s", log)
	}
}

// A handler can say the client is gone before it's done itself, and Stop
// closes whatever else the connection needs along with it.
func TestStopClosesWhatTheConnectionHolds(t *testing.T) {
	held, other := net.Pipe()
	defer other.Close()
	l, logged := start(t, listen(t), func(c *Conn) {
		c.Also(held)
		c.ClientGone()
		// Until Stop closes it.
		io.Copy(io.Discard, held)
	})
	dial(t, l).Write([]byte("1"))
	for len(l.Conns()) == 0 {
		time.Sleep(time.Millisecond)
	}
	<-l.Conns()[0].Gone()

	started := time.Now()
	l.Stop(5 * time.Second)
	if d := time.Since(started); d > time.Second {
		t.Errorf("Stop waited %s for a client that was gone", d)
	}
	if strings.Contains(logged(), "still open") {
		t.Errorf("log:\n%s", logged())
	}
	held2, other2 := net.Pipe()
	defer other2.Close()
	if l.Conns()[0].Also(held2) {
		t.Error("Also took a connection after Stop")
	}
	if _, err := held2.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("Also should've closed the connection it didn't take: %v", err)
	}
}

// The connection is over once its handler returns, so the client hears
// about it right away.
func TestClosesConnectionsOnceHandled(t *testing.T) {
	l, _ := start(t, listen(t), func(c *Conn) {})
	nc := dial(t, l)
	nc.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := nc.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("client read: %v, want EOF", err)
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
	l, logged := start(t, &failOnce{Listener: listen(t)}, echo)
	nc := dial(t, l)
	nc.Write([]byte("1"))
	nc.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := nc.Read(make([]byte, 1)); err != nil {
		t.Errorf("no reply: %v", err)
	}
	if !strings.Contains(logged(), "Couldn't accept a client") {
		t.Errorf("the error wasn't logged:\n%s", logged())
	}
}
