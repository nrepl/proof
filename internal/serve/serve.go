// Package serve is the server behind proof serve: a small nREPL server
// that behaves like the reference implementation, or, scenario by
// scenario, like the servers that differ from it. Client test suites run
// against it to make sure a client copes with all of them. It records
// every connection, so the requests can be graded like the proxy's.
package serve

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nrepl/proof/bencode"
	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/internal/clients"
	"github.com/nrepl/proof/nrepl"
)

// Server accepts clients and answers them.
type Server struct {
	*clients.Listener
	b       behavior
	version string

	defs *definitions
	// ctx ends when proof stops, taking every eval still running with it.
	ctx  context.Context
	stop context.CancelFunc
	// work counts the goroutines running sessions, evals and futures.
	work sync.WaitGroup

	mu       sync.Mutex
	sessions map[string]*session
}

// Listen starts accepting clients on addr, with the given scenarios. Call
// Serve to handle them. version goes into describe's versions.
func Listen(addr, version string, scenarios []string) (*Server, error) {
	b, err := behave(scenarios)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return newServer(ln, version, b), nil
}

func newServer(ln net.Listener, version string, b behavior) *Server {
	ctx, stop := context.WithCancel(context.Background())
	return &Server{
		Listener: clients.New(ln), b: b, version: version, defs: newDefinitions(),
		ctx: ctx, stop: stop, sessions: map[string]*session{},
	}
}

// Serve accepts clients until Stop is called.
func (s *Server) Serve() {
	s.Listener.Serve(func(cc *clients.Conn) {
		s.Log("Connection %d opened", cc.N)
		c := &conn{Conn: cc, srv: s, shared: newBindings()}
		c.serve()
	})
}

// Stop stops accepting clients and returns the transcript of every
// connection in which the client sent something. Clients that are still
// connected get up to grace to hang up on their own, and are then
// disconnected.
func (s *Server) Stop(grace time.Duration) []check.Traffic {
	s.Listener.Stop(grace)
	// No connection is left to start anything new.
	s.stop()
	s.work.Wait()
	return s.Traffic()
}

type conn struct {
	*clients.Conn
	srv *Server
	// shared are the bindings of every session made on the connection, in
	// shared-state.
	shared *bindings

	// wmu keeps replies whole.
	wmu sync.Mutex
	// batch holds replies until the eval waits or ends, in batched-writes.
	batch []byte
}

func (c *conn) serve() {
	dec := bencode.NewDecoder(bufio.NewReader(c.Client))
	for {
		v, err := dec.Decode()
		ev, isFrame := nrepl.DecodeEvent(nrepl.Sent, v, err)
		if isFrame {
			c.Record(ev)
		}
		switch {
		case err == nil && ev.Msg != nil:
			if c.srv.handle(c, ev.Msg) {
				continue
			}
		case isFrame && !errors.Is(err, io.ErrUnexpectedEOF):
			// There's no telling what the client meant, or where its next
			// request starts.
			c.hangUp()
		case !errors.Is(err, net.ErrClosed):
			// The end of the stream (maybe in the middle of a request), or a
			// reset.
			c.Record(nrepl.Event{Dir: nrepl.Sent, Time: time.Now(), Closed: true})
			c.srv.Log("Connection %d closed", c.N)
		}
		return
	}
}

// hangUp closes the connection from the server's side.
func (c *conn) hangUp() {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.Record(nrepl.Event{Dir: nrepl.Received, Time: time.Now(), Closed: true})
	c.Client.Close()
	c.srv.Log("Connection %d closed by the server", c.N)
}

// reply sends a reply to req, carrying its id and session.
func (c *conn) reply(req nrepl.Message, fields map[string]any) {
	msg := map[string]any{}
	for _, k := range []string{"id", "session"} {
		if v, ok := req[k]; ok {
			msg[k] = v
		}
	}
	maps.Copy(msg, fields)
	raw := c.srv.encode(msg)
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.Record(nrepl.Event{Dir: nrepl.Received, Time: time.Now(), Msg: msg, Data: msg, Raw: raw})
	// A client that went away finds out soon enough, and its side of the
	// connection records that.
	switch {
	case c.srv.b.byteWrites:
		for i := range raw {
			if _, err := c.Client.Write(raw[i : i+1]); err != nil {
				return
			}
		}
	case c.srv.b.batchedWrites:
		c.batch = append(c.batch, raw...)
		if _, ok := msg["status"]; ok {
			c.flushLocked()
		}
	default:
		c.Client.Write(raw)
	}
}

// flush writes the replies held back in batched-writes.
func (c *conn) flush() {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.flushLocked()
}

func (c *conn) flushLocked() {
	if len(c.batch) > 0 {
		c.Client.Write(c.batch)
		c.batch = c.batch[:0]
	}
}

// encode encodes a reply. Every value in it came from a request or from
// proof serve itself, so it can always be encoded.
func (s *Server) encode(msg map[string]any) []byte {
	if !s.b.unsortedKeys {
		raw, err := bencode.Marshal(msg)
		if err != nil {
			panic(err)
		}
		return raw
	}
	keys := slices.Sorted(maps.Keys(msg))
	slices.Reverse(keys)
	var buf bytes.Buffer
	buf.WriteByte('d')
	for _, k := range keys {
		for _, v := range []any{k, msg[k]} {
			raw, err := bencode.Marshal(v)
			if err != nil {
				panic(err)
			}
			buf.Write(raw)
		}
	}
	buf.WriteByte('e')
	return buf.Bytes()
}

var (
	done           = []any{"done"}
	unknownSession = map[string]any{"status": []any{"error", "unknown-session", "done"}}
	ops            = []string{"clone", "close", "describe", "eval", "interrupt", "stdin"}
)

// offers reports whether the server supports an op.
func (s *Server) offers(op string) bool {
	switch op {
	case "interrupt":
		return !s.b.noInterrupt
	case "stdin":
		return s.b.stdin == askForInput
	}
	return slices.Contains(ops, op)
}

// handle answers a request, and reports false if the server hung up.
func (s *Server) handle(c *conn, req nrepl.Message) bool {
	sess, ok := s.session(c, req)
	if !ok {
		c.reply(req, unknownSession)
		return true
	}
	if !req.Has("session") {
		// Like nREPL, the replies say which session the request ran in,
		// even when it's one made up for the request.
		req = req.With(nrepl.Message{"session": sess.id})
	}
	op := req.Str("op")
	if !s.offers(op) {
		fields := map[string]any{"status": []any{"error", "unknown-op", "done"}}
		if v, ok := req["op"]; ok && !s.b.noOpEcho {
			fields["op"] = v
		}
		c.reply(req, fields)
		return true
	}
	switch op {
	case "describe":
		c.reply(req, s.describe())
	case "clone":
		s.clone(c, req, sess)
	case "close":
		s.close(c, req, sess)
	case "eval":
		if s.b.hangUp {
			c.hangUp()
			return false
		}
		s.eval(c, req, sess)
	case "stdin":
		sess.give(req.Str("stdin"))
		c.reply(req, map[string]any{"status": done})
	case "interrupt":
		s.interrupt(c, req, sess)
	}
	return true
}

func (s *Server) describe() map[string]any {
	listed := map[string]any{}
	for _, op := range ops {
		if s.offers(op) && !(op == "close" && s.b.noCloseOp) {
			listed[op] = map[string]any{}
		}
	}
	var version any = s.version
	if !s.b.stringVersions {
		version = versionDict(s.version)
	}
	return map[string]any{
		"ops":      listed,
		"versions": map[string]any{"proof": version},
		"aux":      map[string]any{"current-ns": "user"},
		"status":   done,
	}
}

// versionDict describes a version like nREPL does, e.g. 0.1.0-dev as
// major 0, minor 1 and incremental 0.
func versionDict(v string) map[string]any {
	d := map[string]any{"version-string": v}
	numbers, _, _ := strings.Cut(v, "-")
	for i, part := range strings.SplitN(numbers, ".", 3) {
		if n, err := strconv.ParseInt(part, 10, 64); err == nil {
			d[[]string{"major", "minor", "incremental"}[i]] = n
		}
	}
	return d
}

// session finds the session a request names. A request that doesn't name
// one gets a new ephemeral session, and so does one naming a session that
// doesn't exist in any-session. ok is false when there's no such session.
func (s *Server) session(c *conn, req nrepl.Message) (sess *session, ok bool) {
	if !req.Has("session") {
		return s.ephemeral(c), true
	}
	id, _ := req["session"].(string)
	s.mu.Lock()
	sess, ok = s.sessions[id]
	s.mu.Unlock()
	if ok && s.b.socketSessions && sess.conn != c {
		ok = false
	}
	if !ok && s.b.anySession {
		return s.ephemeral(c), true
	}
	return sess, ok
}

func (s *Server) ephemeral(c *conn) *session {
	b := newBindings()
	if s.b.sharedState {
		b = c.shared
	}
	return newSession(c, b, true)
}

func (s *Server) clone(c *conn, req nrepl.Message, from *session) {
	b := from.b.copy()
	if s.b.sharedState {
		b = c.shared
	}
	sess := newSession(c, b, false)
	s.mu.Lock()
	s.sessions[sess.id] = sess
	s.mu.Unlock()
	s.work.Add(1)
	go func() {
		defer s.work.Done()
		s.run(sess)
	}()
	c.reply(req, map[string]any{"new-session": sess.id, "status": done})
}

func (s *Server) close(c *conn, req nrepl.Message, sess *session) {
	s.mu.Lock()
	if s.sessions[sess.id] == sess {
		delete(s.sessions, sess.id)
	}
	s.mu.Unlock()
	status := []any{"done", "session-closed"}
	if s.b.noSessionClosed {
		status = done
	}
	c.reply(req, map[string]any{"status": status})
	sess.close()
}

func (s *Server) interrupt(c *conn, req nrepl.Message, sess *session) {
	if sess.ephemeral {
		c.reply(req, map[string]any{"status": []any{"error", "session-ephemeral", "done"}})
		return
	}
	id := ""
	if req.Has("interrupt-id") {
		id = fmt.Sprint(req["interrupt-id"])
	}
	j, mismatch := sess.interrupt(id)
	switch {
	case mismatch:
		c.reply(req, map[string]any{"status": []any{"error", "interrupt-id-mismatch", "done"}})
		return
	case j == nil:
		c.reply(req, map[string]any{"status": []any{"session-idle", "done"}})
		return
	}
	// Like nREPL, this tells the eval it's done before telling the
	// interrupt, and the code finds out about it after that.
	j.c.reply(j.req, map[string]any{"status": []any{"done", "interrupted"}})
	c.reply(req, map[string]any{"status": done})
	signal(j.interrupts)
}

func (s *Server) eval(c *conn, req nrepl.Message, sess *session) {
	reply := func(fields map[string]any) { c.reply(req, fields) }
	switch req["code"].(type) {
	case string:
	case nil:
		reply(map[string]any{"status": []any{"error", "no-code", "done"}})
		return
	default:
		reply(map[string]any{"status": []any{"error", "unknown-code-type", "done"}})
		return
	}
	if ns, ok := req["ns"].(string); ok && !s.defs.hasNS(ns) {
		switch {
		case s.b.nsError:
			reply(s.fail(reply, &thrown{ex: &exception{class: "java.lang.Exception", msg: "No namespace: " + ns + " found"}}, "user"))
			return
		case !s.b.nsFallback:
			reply(map[string]any{"status": []any{"error", "namespace-not-found", "done"}, "ns": ns})
			return
		}
	}
	j := &job{c: c, req: req, interrupts: make(chan struct{}, 1)}
	if !sess.ephemeral {
		sess.queue(j)
		return
	}
	s.work.Add(1)
	go func() {
		defer s.work.Done()
		s.runEval(sess, j)
	}()
}

// fail reports an exception the way nREPL does, and returns the message
// that ends the eval. In error-with-done that message carries the error.
func (s *Server) fail(reply func(map[string]any), t *thrown, ns string) map[string]any {
	text, ex, rootEx := errorReport(t, ns)
	reply(map[string]any{"err": text})
	fields := map[string]any{"ex": ex, "root-ex": rootEx, "status": []any{"eval-error"}}
	if s.b.errorWithDone {
		fields["status"] = []any{"eval-error", "done"}
		return fields
	}
	reply(fields)
	return map[string]any{"status": done}
}

// run carries out the session's evals one at a time, as nREPL does.
func (s *Server) run(sess *session) {
	for {
		j, ok := sess.next(s.ctx)
		if !ok {
			return
		}
		s.runEval(sess, j)
	}
}

func (s *Server) runEval(sess *session, j *job) {
	c, req := j.c, j.req
	reply := func(fields map[string]any) { c.reply(req, fields) }
	if s.b.emptyMessages {
		reply(nil)
	}
	ns := sess.b.currentNS()
	if n, ok := req["ns"].(string); ok && s.defs.hasNS(n) {
		ns = n
	}
	e := &evaluation{
		defs: s.defs, b: sess.b, ns: ns, ctx: s.ctx, interrupts: j.interrupts, waiting: c.flush,
		output: func(key, text string) {
			if key == "err" && s.b.noErr {
				return
			}
			if !s.b.splitOutput {
				reply(map[string]any{key: text})
				return
			}
			for _, r := range text {
				reply(map[string]any{key: string(r)})
			}
		},
		readLine: func(interrupts <-chan struct{}) (any, error) {
			switch s.b.stdin {
			case throwOnRead:
				// What jank says.
				return nil, throwf("clojure.lang.ExceptionInfo", "TODO: port read-line")
			case endOfInput:
				// The end of the server's own stdin.
				return nil, nil
			}
			line, ok, err := sess.readLine(s.ctx, interrupts, func() { reply(map[string]any{"status": []any{"need-input"}}) })
			switch {
			case err != nil:
				return nil, err
			case ok:
				return line, nil
			case s.b.eofError:
				// What nREPL 1.7.0 says.
				return nil, throwf("java.lang.ClassCastException", "class java.lang.Long cannot be cast to class java.lang.Character")
			}
			return nil, nil
		},
	}
	final := map[string]any{"status": done}
	// held is the value last-value sends at the end.
	var held map[string]any
	rd := &reader{src: req.Str("code")}
	for {
		form, err := rd.next()
		if err == io.EOF {
			break
		}
		var v any
		if err == nil {
			v, err = e.eval(form)
		}
		if errors.Is(err, errStopping) {
			return
		}
		if err != nil {
			t := err.(*thrown)
			sess.b.failed(t.ex)
			final, held = s.fail(reply, t, e.ns), nil
			// nREPL goes on with the next form, unless there's no telling
			// where it starts. Basilisp and jank give up on the rest.
			if t.phase == reading || s.b.lastValue || s.b.errorWithDone {
				break
			}
			continue
		}
		sess.b.push(v)
		msg := map[string]any{"value": pr(v), "ns": e.ns}
		if s.b.lastValue {
			held = msg
		} else {
			reply(msg)
		}
	}
	if held != nil {
		reply(held)
	}
	// A namespace given with the request is only for this eval.
	if !req.Has("ns") {
		sess.b.setNS(e.ns)
	}
	if sess.finish(j) {
		reply(final)
	}
	// Futures print after the eval is done, so their output is late, as it
	// is on nREPL.
	s.runFutures(c, e.futures)
}

func (s *Server) runFutures(c *conn, futures []pending) {
	for _, f := range futures {
		s.work.Add(1)
		go func() {
			defer s.work.Done()
			// Interrupting the eval doesn't stop its futures.
			f.e.interrupts = nil
			f.e.do(f.body)
			c.flush()
			s.runFutures(c, f.e.futures)
		}()
	}
}
