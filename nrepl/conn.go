package nrepl

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nrepl/proof/bencode"
)

// Direction says which way an event went.
type Direction int

const (
	Sent Direction = iota
	Received
)

// Event is one entry in a connection's transcript.
type Event struct {
	Dir  Direction
	Time time.Time
	// Msg is the decoded message. It's nil for frames that decoded to
	// something other than a dict, or didn't decode at all.
	Msg Message
	// Data is the decoded value, whatever its type.
	Data any
	Raw  []byte
	// Violations are non-fatal encoding problems in the frame.
	Violations []bencode.Violation
	// Err is set when a frame couldn't be decoded. Nothing that came after
	// it on the connection was decoded.
	Err error
}

// DecodeEvent records what bencode.Decoder.Decode returned: a frame, or
// a frame that couldn't be decoded. It returns false when the stream ended
// between frames or broke off (e.g. it was reset), which isn't a problem
// with the encoding.
func DecodeEvent(dir Direction, v bencode.Value, err error) (Event, bool) {
	ev := Event{Dir: dir, Time: time.Now(), Raw: v.Raw}
	var se *bencode.SyntaxError
	switch {
	case err == nil:
		ev.Data, ev.Violations = v.Data, v.Violations
		if m, ok := v.Data.(map[string]any); ok {
			ev.Msg = Message(m)
		}
	case errors.As(err, &se) || errors.Is(err, io.ErrUnexpectedEOF):
		ev.Err = err
	default:
		return Event{}, false
	}
	return ev, true
}

// Conn is a connection to an nREPL server.
type Conn struct {
	nc     net.Conn
	prefix string
	nextID atomic.Int64

	writeMu sync.Mutex

	mu      sync.Mutex
	events  []Event
	changed chan struct{}
	// readErr is set once reading has stopped: the server closed the
	// connection, or a frame failed to decode.
	readErr error
}

var connCounter atomic.Int64

// Dial connects to addr ("host:port").
func Dial(addr string, timeout time.Duration) (*Conn, error) {
	nc, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	c := &Conn{
		nc:      nc,
		prefix:  "proof-" + strconv.FormatInt(connCounter.Add(1), 10) + "-",
		changed: make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *Conn) readLoop() {
	dec := bencode.NewDecoder(bufio.NewReader(c.nc))
	for {
		v, err := dec.Decode()
		c.mu.Lock()
		if ev, ok := DecodeEvent(Received, v, err); ok {
			c.events = append(c.events, ev)
		}
		c.readErr = err
		c.notifyLocked()
		c.mu.Unlock()
		if err != nil {
			return
		}
	}
}

func (c *Conn) notifyLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}

// ErrTimeout means no "done" arrived in time.
var ErrTimeout = errors.New("timed out waiting for done")

// ErrClosed means the server closed the connection (or sent a frame that
// couldn't be decoded) before "done". The cause is wrapped alongside it.
var ErrClosed = errors.New("connection closed before done")

// ErrWrite means a request couldn't be written, which on an established
// connection means the server closed or reset it.
var ErrWrite = errors.New("couldn't write to the connection")

// Send writes a request and returns its id, adding one if the request has
// none. Ids are unique across all connections in this process, so ids in
// transcripts are unambiguous.
//
// A request that can't be encoded is a bug in the check, so Send panics
// rather than blaming the server.
func (c *Conn) Send(m Message) (string, error) {
	if !m.Has("id") {
		m = m.With(Message{"id": c.prefix + strconv.FormatInt(c.nextID.Add(1), 10)})
	}
	b, err := bencode.Marshal(map[string]any(m))
	if err != nil {
		panic(fmt.Sprintf("encoding %s: %v", m, err))
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	c.events = append(c.events, Event{Dir: Sent, Time: time.Now(), Msg: m, Data: map[string]any(m), Raw: b})
	c.mu.Unlock()
	if _, err := c.nc.Write(b); err != nil {
		return m.Str("id"), fmt.Errorf("%w: %v", ErrWrite, err)
	}
	return m.Str("id"), nil
}

// Request sends m and waits for its "done".
func (c *Conn) Request(m Message, timeout time.Duration) (Response, error) {
	id, err := c.Send(m)
	if err != nil {
		return Response{}, err
	}
	return c.Collect(id, timeout)
}

// Collect waits until a message with the given id carries "done", and
// returns everything received for that id up to that point. On an error
// the response holds whatever did arrive.
func (c *Conn) Collect(id string, timeout time.Duration) (Response, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	var resp Response
	seen := 0
	for {
		c.mu.Lock()
		events, readErr, changed := c.events[seen:], c.readErr, c.changed
		seen = len(c.events)
		c.mu.Unlock()
		for _, ev := range events {
			if ev.Dir != Received || ev.Msg == nil || ev.Msg.Str("id") != id {
				continue
			}
			resp.Messages = append(resp.Messages, ev.Msg)
			if ev.Msg.HasStatus("done") {
				resp.Done = true
				return resp, nil
			}
		}
		if readErr != nil {
			return resp, fmt.Errorf("%w (%v)", ErrClosed, readErr)
		}
		select {
		case <-changed:
		case <-deadline.C:
			return resp, ErrTimeout
		}
	}
}

// WaitFor waits until a received message satisfies pred and returns it.
// Messages that arrived before the call count too.
func (c *Conn) WaitFor(pred func(Message) bool, timeout time.Duration) (Message, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	seen := 0
	for {
		c.mu.Lock()
		events, readErr, changed := c.events[seen:], c.readErr, c.changed
		seen = len(c.events)
		c.mu.Unlock()
		for _, ev := range events {
			if ev.Dir == Received && ev.Msg != nil && pred(ev.Msg) {
				return ev.Msg, nil
			}
		}
		if readErr != nil {
			return nil, fmt.Errorf("%w (%v)", ErrClosed, readErr)
		}
		select {
		case <-changed:
		case <-deadline.C:
			return nil, ErrTimeout
		}
	}
}

// Settle waits until nothing has arrived for quiet, or until max has
// passed, so late messages make it into the transcript.
func (c *Conn) Settle(quiet, max time.Duration) {
	end := time.Now().Add(max)
	for time.Now().Before(end) {
		c.mu.Lock()
		changed, readErr := c.changed, c.readErr
		c.mu.Unlock()
		if readErr != nil {
			return
		}
		select {
		case <-changed:
		case <-time.After(quiet):
			return
		}
	}
}

// Transcript returns a copy of every event so far.
func (c *Conn) Transcript() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Event(nil), c.events...)
}

// Close closes the connection.
func (c *Conn) Close() error { return c.nc.Close() }

// String renders a message compactly for reports.
func (m Message) String() string {
	s := "{"
	for i, k := range m.Keys() {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%s %s", k, render(m[k]))
	}
	return s + "}"
}

func render(v any) string {
	switch x := v.(type) {
	case string:
		return strconv.Quote(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case []any:
		s := "["
		for i, item := range x {
			if i > 0 {
				s += " "
			}
			s += render(item)
		}
		return s + "]"
	case map[string]any:
		return Message(x).String()
	default:
		return fmt.Sprintf("%v", x)
	}
}
