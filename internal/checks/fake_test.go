package checks

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/nrepl/proof/bencode"
	"github.com/nrepl/proof/nrepl"
)

// quirks switch on one misbehaviour each in the fake server, so the tests
// can confirm every check catches what it claims to and nothing else.
type quirks struct {
	opsList          bool // describe ops as a list
	noClone          bool // clone isn't listed in describe
	noVersions       bool
	noUnknownOp      bool // unknown ops get a bare done
	noOpEcho         bool // unknown-op reply doesn't echo op
	noSessionClosed  bool
	acceptAnySession bool // unknown sessions are silently accepted
	sharedState      bool // every session shares *1
	socketSessions   bool // sessions only exist on the connection that made them
	noEphemeral      bool // requests without a session get unknown-session
	lastValueOnly    bool
	dropErr          bool
	outAfterValue    bool
	noEvalError      bool // failed evals get no eval-error status
	noEx             bool
	noNoCode         bool // missing code gets eval-error instead of no-code
	noNs             bool // replies don't carry ns
	nsFallback       bool // unknown ns silently falls back to user
	twoDones         bool
	valueAfterDone   bool
	payloadTogether  bool // out and value in one message
	errorThenOut     bool // output after the eval-error status
	noSessionEcho    bool
	noID             bool // output messages carry no id
	intValue         bool // value is an integer
	statusString     bool // status "done" instead of ["done"] on unknown-op
	unsortedKeys     bool // describe reply has keys out of order
	badUTF8          bool // out isn't valid UTF-8
	crashOnDescribe  bool // describe closes the connection
	cloneHangs       bool // clone never replies
	noStdinOp        bool // describe doesn't list stdin
	noNeedInput      bool // reading stdin returns nil without asking
	needInputDone    bool // need-input comes with done
	eofError         bool // an empty stdin fails the read
	dropStdin        bool // stdin input never reaches the read
	flatFields       bool // a request with a dict or list in it kills the connection
}

type fakeSession struct {
	last string
	vars map[string]string
	// pending is an eval waiting for stdin.
	pending nrepl.Message
}

type fakeServer struct {
	q  quirks
	ln net.Listener

	mu       sync.Mutex
	sessions map[string]*fakeSession
	nextID   int
	shared   *fakeSession
	// namespaces are user and the ones ns forms created.
	namespaces map[string]bool
}

func startFake(t *testing.T, q quirks) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{q: q, ln: ln, sessions: map[string]*fakeSession{}, shared: newFakeSession(), namespaces: map[string]bool{"user": true}}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return ln.Addr().String()
}

func newFakeSession() *fakeSession {
	return &fakeSession{last: "nil", vars: map[string]string{}}
}

func (s *fakeServer) serve(c net.Conn) {
	defer c.Close()
	dec := bencode.NewDecoder(bufio.NewReader(c))
	local := map[string]bool{}
	for {
		v, err := dec.Decode()
		if err != nil {
			return
		}
		m, ok := v.Data.(map[string]any)
		if !ok {
			return
		}
		if !s.handle(c, nrepl.Message(m), local) {
			return
		}
	}
}

func (s *fakeServer) send(c net.Conn, req nrepl.Message, fields map[string]any) {
	out := map[string]any{}
	_, isOut := fields["out"]
	_, isErr := fields["err"]
	if !s.q.noID || !(isOut || isErr) {
		out["id"] = req.Str("id")
	}
	if sess, ok := req["session"]; ok && !s.q.noSessionEcho {
		out["session"] = sess
	}
	for k, v := range fields {
		out[k] = v
	}
	b, err := bencode.Marshal(out)
	if err != nil {
		panic(err)
	}
	c.Write(b)
}

func (s *fakeServer) session(req nrepl.Message, local map[string]bool) (*fakeSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := req["session"].(string)
	if !ok {
		if s.q.noEphemeral {
			return nil, false
		}
		return newFakeSession(), true
	}
	sess, found := s.sessions[id]
	if s.q.socketSessions && !local[id] {
		found = false
	}
	if !found {
		if s.q.acceptAnySession {
			return newFakeSession(), true
		}
		return nil, false
	}
	if s.q.sharedState {
		return s.shared, true
	}
	return sess, true
}

func (s *fakeServer) handle(c net.Conn, req nrepl.Message, local map[string]bool) bool {
	q := s.q
	done := []any{"done"}
	if q.flatFields {
		for _, v := range req {
			switch v.(type) {
			case map[string]any, []any:
				return false
			}
		}
	}
	switch op := req.Str("op"); op {
	case "describe":
		if q.crashOnDescribe {
			return false
		}
		names := []string{"describe", "eval", "clone", "close"}
		if q.noClone {
			names = []string{"describe", "eval", "close"}
		}
		if !q.noStdinOp {
			names = append(names, "stdin")
		}
		var ops any
		if q.opsList {
			l := []any{}
			for _, n := range names {
				l = append(l, n)
			}
			ops = l
		} else {
			d := map[string]any{}
			for _, n := range names {
				d[n] = map[string]any{}
			}
			ops = d
		}
		fields := map[string]any{"ops": ops, "status": done}
		if !q.noVersions {
			fields["versions"] = map[string]any{"fake": map[string]any{"major": 1, "minor": 0, "version-string": "1.0"}}
		}
		if q.unsortedKeys {
			// Hand-encoded with "status" before "id".
			var session string
			if sess := req.Str("session"); sess != "" {
				session = "7:session" + strconv.Itoa(len(sess)) + ":" + sess
			}
			c.Write([]byte("d6:statusl4:donee2:id" + strconv.Itoa(len(req.Str("id"))) + ":" + req.Str("id") +
				"3:opsd8:describede4:evalde5:clonede5:closede5:stdindee" + session +
				"8:versionsd4:faked14:version-string3:1.0eee"))
			return true
		}
		s.send(c, req, fields)
	case "clone":
		if q.cloneHangs {
			return true
		}
		s.mu.Lock()
		s.nextID++
		id := "fake-session-" + strconv.Itoa(s.nextID)
		s.sessions[id] = newFakeSession()
		s.mu.Unlock()
		local[id] = true
		s.send(c, req, map[string]any{"new-session": id, "status": done})
	case "close":
		s.mu.Lock()
		delete(s.sessions, req.Str("session"))
		s.mu.Unlock()
		status := []any{"done", "session-closed"}
		if q.noSessionClosed {
			status = done
		}
		s.send(c, req, map[string]any{"status": status})
	case "eval":
		s.eval(c, req, local)
	case "stdin":
		s.stdin(c, req, local)
	case "interrupt":
		// Nothing runs long enough to be interrupted.
		s.send(c, req, map[string]any{"status": []any{"session-idle", "done"}})
	default:
		switch {
		case q.statusString:
			s.send(c, req, map[string]any{"status": "done"})
		case q.noUnknownOp:
			s.send(c, req, map[string]any{"status": done})
		default:
			fields := map[string]any{"status": []any{"error", "unknown-op", "done"}}
			if !q.noOpEcho {
				fields["op"] = op
			}
			s.send(c, req, fields)
		}
	}
	return true
}

// stdin finishes an eval waiting for input, answering like Clojure's
// read-line: the line without its newline, or nil at end of input.
func (s *fakeServer) stdin(c net.Conn, req nrepl.Message, local map[string]bool) {
	q := s.q
	if sess, ok := s.session(req, local); ok && sess.pending != nil {
		pending := sess.pending
		sess.pending = nil
		input := req.Str("stdin")
		switch {
		case q.dropStdin:
			s.send(c, pending, map[string]any{"value": `""`})
		case input == "" && q.eofError:
			s.send(c, pending, map[string]any{"err": "unexpected EOF\n"})
			s.send(c, pending, map[string]any{"status": []any{"eval-error"}, "ex": "fake.EOF"})
		case input == "":
			s.send(c, pending, map[string]any{"value": "nil"})
		default:
			s.send(c, pending, map[string]any{"value": strconv.Quote(strings.TrimSuffix(input, "\n"))})
		}
		s.send(c, pending, map[string]any{"status": []any{"done"}})
	}
	s.send(c, req, map[string]any{"status": []any{"done"}})
}

func (s *fakeServer) eval(c net.Conn, req nrepl.Message, local map[string]bool) {
	q := s.q
	sess, ok := s.session(req, local)
	if !ok {
		s.send(c, req, map[string]any{"status": []any{"error", "unknown-session", "done"}})
		return
	}
	code, hasCode := req["code"].(string)
	if !hasCode {
		status := []any{"error", "no-code", "done"}
		if q.noNoCode {
			status = []any{"eval-error", "done"}
		}
		s.send(c, req, map[string]any{"status": status})
		return
	}
	s.mu.Lock()
	if name, ok := strings.CutPrefix(code, "(ns "); ok {
		s.namespaces[strings.TrimRight(name, ") \n")] = true
	}
	known := s.namespaces[req.Str("ns")]
	s.mu.Unlock()
	if ns := req.Str("ns"); ns != "" && !known && !q.nsFallback {
		s.send(c, req, map[string]any{"status": []any{"error", "namespace-not-found", "done"}})
		return
	}
	finish := func() {
		s.send(c, req, map[string]any{"status": []any{"done"}})
		if q.twoDones {
			s.send(c, req, map[string]any{"status": []any{"done"}})
		}
		if q.valueAfterDone {
			s.send(c, req, map[string]any{"value": "late"})
		}
	}
	var values []string
	for _, form := range strings.Fields(code) {
		value, out := "nil", ""
		switch form {
		case "value":
			value = "3"
		case "stdout":
			out = "proof"
			if q.badUTF8 {
				out = "pro\xffof"
			}
		case "stderr":
			if !q.dropErr {
				s.send(c, req, map[string]any{"err": "proof"})
			}
		case "throw":
			s.send(c, req, map[string]any{"err": "boom\n"})
			fields := map[string]any{"status": []any{"eval-error"}}
			if q.noEvalError {
				fields = map[string]any{}
			}
			if !q.noEx {
				fields["ex"] = "fake.Error"
			}
			if len(fields) > 0 {
				s.send(c, req, fields)
			}
			if q.errorThenOut {
				s.send(c, req, map[string]any{"out": "after the error"})
			}
			finish()
			return
		case "define":
			sess.vars["answer"] = "42"
			value = "#'user/answer"
		case "use":
			value = sess.vars["answer"]
		case "read":
			if !q.noNeedInput {
				sess.pending = req
				status := []any{"need-input"}
				if q.needInputDone {
					status = []any{"need-input", "done"}
				}
				s.send(c, req, map[string]any{"status": status})
				return
			}
		case "marker":
			value = ":proof-marker"
		case "*1":
			value = sess.last
		default:
			value = form
		}
		sess.last = value
		values = append(values, value)
		valueMsg := map[string]any{"value": value}
		if !q.noNs {
			valueMsg["ns"] = "user"
		}
		if q.intValue {
			if n, err := strconv.Atoi(value); err == nil {
				valueMsg["value"] = n
			}
		}
		if q.lastValueOnly {
			if out != "" {
				s.send(c, req, map[string]any{"out": out})
			}
			continue
		}
		switch {
		case out != "" && q.payloadTogether:
			valueMsg["out"] = out
			s.send(c, req, valueMsg)
		case out != "" && q.outAfterValue:
			s.send(c, req, valueMsg)
			s.send(c, req, map[string]any{"out": out})
		case out != "":
			s.send(c, req, map[string]any{"out": out})
			s.send(c, req, valueMsg)
		default:
			s.send(c, req, valueMsg)
		}
	}
	if q.lastValueOnly && len(values) > 0 {
		s.send(c, req, map[string]any{"value": values[len(values)-1], "ns": "user"})
	}
	finish()
}
