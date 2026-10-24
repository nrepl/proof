package checks

import (
	"bufio"
	"errors"
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
	emptyCodeHangs   bool // an eval of empty code never gets a reply
	noNs             bool // replies don't carry ns
	nsFallback       bool // unknown ns silently falls back to user
	twoDones         bool
	valueAfterDone   bool
	stateAfterDone   bool // changed-namespaces follows done, like Babashka's
	payloadTogether  bool // out and value in one message
	errorThenOut     bool // output after the eval-error status
	errorThenState   bool // changed-namespaces after the eval-error status
	noSessionEcho    bool
	noID             bool // output messages carry no id
	intValue         bool // value is an integer
	printNewline     bool // a value printed with a print function ends in a newline
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
	noInterruptOp    bool // describe doesn't list interrupt
	interruptIgnored bool // interrupt says done, but the eval goes on
	noInterrupted    bool // an interrupted eval ends with a plain done
	noSessionIdle    bool // interrupting an idle session gets a plain done
	anyInterruptID   bool // interrupt stops the running eval, whatever its interrupt-id
	needsInterruptID bool // an interrupt without interrupt-id leaves the running eval alone
	dropQueued       bool // evals queued behind an interrupted one never run
	interruptHere    bool // the interrupted eval's done goes to the interrupt's connection
	diesOnInterrupt  bool // an interrupted session never answers again
	noLoadFileOp     bool // there's no load-file op
	loadFileNoValue  bool // load-file sends no value
	nreplLookups     bool // only nREPL's completions and lookup, without cider-nrepl's ops
	completionsLate  bool // the completions come before the message with done
	stringCandidates bool // completions are strings rather than dicts
	completePrefix   bool // complete only takes a prefix, not a symbol
	emptyLookupInfo  bool // lookup gives an empty dict for a symbol that doesn't exist
	lineString       bool // the line of a symbol is a string
	columnZero       bool // info counts columns from 0
	noNoInfo         bool // info for a symbol that doesn't exist gets a plain done
	eldocStrings     bool // eldoc's arglists are strings rather than lists
	pathFile         bool // the file of a symbol is a path rather than a URI
	ciderPrefixed    bool // cider-nrepl's ops go by their cider/ names only
	crashOnGarbage   bool // a frame that isn't a request takes the server down
	typesKill        bool // an eval with a line that isn't an integer kills its session
	diesOnDisconnect bool // a session dies when its eval's connection goes away
	oneEvalAtATime   bool // an eval in one session holds up all the others
}

var unknownSession = map[string]any{"status": []any{"error", "unknown-session", "done"}}

// fakeRun is an eval that runs until it's interrupted, on the connection
// it came from.
type fakeRun struct {
	c     net.Conn
	req   nrepl.Message
	local map[string]bool
}

type fakeSession struct {
	last string
	vars map[string]string
	// pending is an eval waiting for stdin.
	pending nrepl.Message
	// running is the eval that runs until it's interrupted, and queue the
	// evals waiting for it to finish.
	running *fakeRun
	queue   []fakeRun
	dead    bool
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
	defer s.disconnected(c)
	dec := bencode.NewDecoder(bufio.NewReader(c))
	local := map[string]bool{}
	for {
		v, err := dec.Decode()
		m, ok := v.Data.(map[string]any)
		if err != nil || !ok {
			var garbage *bencode.SyntaxError
			if s.q.crashOnGarbage && (err == nil || errors.As(err, &garbage)) {
				s.ln.Close()
			}
			return
		}
		if !s.handle(c, nrepl.Message(m), local) {
			return
		}
	}
}

// disconnected kills the sessions whose running eval came from c, with
// diesOnDisconnect.
func (s *fakeServer) disconnected(c net.Conn) {
	if !s.q.diesOnDisconnect {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.sessions {
		if sess.running != nil && sess.running.c == c {
			sess.dead = true
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
		if !q.noInterruptOp {
			names = append(names, "interrupt")
		}
		if !q.noLoadFileOp {
			names = append(names, "load-file")
		}
		names = append(names, "completions", "lookup")
		switch {
		case q.ciderPrefixed:
			names = append(names, "cider/complete", "cider/info", "cider/eldoc")
		case !q.nreplLookups:
			names = append(names, "complete", "info", "eldoc")
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
				"3:opsd8:describede4:evalde5:clonede5:closede5:stdinde9:interruptde9:load-filede11:completionsde6:lookupde8:completede4:infode5:eldocdee" + session +
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
		s.interrupt(c, req, local)
	case "load-file":
		if q.noLoadFileOp {
			s.unknownOp(c, req)
			break
		}
		// Like nREPL, an eval of what's in the file.
		s.eval(c, req.With(nrepl.Message{"code": req["file"]}), local)
	case "completions":
		s.complete(c, req)
	case "lookup":
		s.lookUp(c, req)
	case "complete", "info", "eldoc", "cider/complete", "cider/info", "cider/eldoc":
		plain := nrepl.Message{"op": strings.TrimPrefix(op, "cider/")}
		switch {
		case q.nreplLookups, q.ciderPrefixed != strings.HasPrefix(op, "cider/"):
			s.unknownOp(c, req)
		case plain.Str("op") == "complete":
			s.complete(c, req.With(plain))
		default:
			s.lookUp(c, req.With(plain))
		}
	default:
		s.unknownOp(c, req)
	}
	return true
}

// anyRunning reports whether an eval runs in any session. s.mu is held.
func (s *fakeServer) anyRunning() bool {
	for _, sess := range s.sessions {
		if sess.running != nil {
			return true
		}
	}
	return false
}

// complete offers proof for a prefix of it, like nREPL's completions and
// cider-nrepl's complete do.
func (s *fakeServer) complete(c net.Conn, req nrepl.Message) {
	q := s.q
	prefix := req.Str("prefix")
	if req.Str("op") == "complete" && !q.completePrefix && prefix == "" {
		prefix = req.Str("symbol")
	}
	completions := []any{}
	if prefix != "" && strings.HasPrefix("proof", prefix) {
		var candidate any = map[string]any{"candidate": "proof", "ns": "user", "type": "function"}
		if q.stringCandidates {
			candidate = "proof"
		}
		completions = append(completions, candidate)
	}
	if q.completionsLate {
		s.send(c, req, map[string]any{"completions": completions})
		s.send(c, req, map[string]any{"status": []any{"done"}})
		return
	}
	s.send(c, req, map[string]any{"completions": completions, "status": []any{"done"}})
}

// lookUp says what proof is, like nREPL's lookup and cider-nrepl's info
// and eldoc do.
func (s *fakeServer) lookUp(c net.Conn, req nrepl.Message) {
	q := s.q
	op, sym := req.Str("op"), req.Str("sym")
	if op == "info" && sym == "" {
		sym = req.Str("symbol")
	}
	found := sym == "proof"
	info := map[string]any{"name": "proof", "ns": "user", "doc": "The proof.", "arglists-str": "([x])",
		"file": "file:/proof.clj", "line": int64(1), "column": int64(1)}
	if q.lineString {
		info["line"] = "1"
	}
	if q.columnZero {
		info["column"] = int64(0)
	}
	if q.pathFile {
		info["file"] = "/proof.clj"
	}
	done := []any{"done"}
	switch {
	case op == "lookup" && !found && q.emptyLookupInfo:
		s.send(c, req, map[string]any{"info": map[string]any{}, "status": done})
	case op == "lookup" && !found:
		s.send(c, req, map[string]any{"info": []any{}, "status": done})
	case op == "lookup":
		s.send(c, req, map[string]any{"info": info, "status": done})
	case op == "info" && !found && q.noNoInfo:
		s.send(c, req, map[string]any{"status": done})
	case !found:
		s.send(c, req, map[string]any{"status": []any{"done", "no-" + op}})
	case op == "info":
		info["status"] = done
		s.send(c, req, info)
	default:
		var arglist any = []any{"x"}
		if q.eldocStrings {
			arglist = "[x]"
		}
		s.send(c, req, map[string]any{"name": "proof", "ns": "user", "docstring": "The proof.", "type": "function",
			"eldoc": []any{arglist}, "status": done})
	}
}

func (s *fakeServer) unknownOp(c net.Conn, req nrepl.Message) {
	switch {
	case s.q.statusString:
		s.send(c, req, map[string]any{"status": "done"})
	case s.q.noUnknownOp:
		s.send(c, req, map[string]any{"status": []any{"done"}})
	default:
		fields := map[string]any{"status": []any{"error", "unknown-op", "done"}}
		if !s.q.noOpEcho {
			fields["op"] = req.Str("op")
		}
		s.send(c, req, fields)
	}
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

// interrupt stops the eval running in the session, like nREPL: the eval
// gets done and interrupted, then the interrupt gets its done, and the
// evals waiting for the session run after that.
func (s *fakeServer) interrupt(c net.Conn, req nrepl.Message, local map[string]bool) {
	q := s.q
	sess, ok := s.session(req, local)
	if !ok {
		s.send(c, req, unknownSession)
		return
	}
	s.mu.Lock()
	run := sess.running
	// status is the reply of an interrupt that leaves the session as it is.
	var status []any
	switch {
	case run == nil && q.noSessionIdle:
		status = []any{"done"}
	case run == nil:
		status = []any{"done", "session-idle"}
	case req.Has("interrupt-id") && req.Str("interrupt-id") != run.req.Str("id") && !q.anyInterruptID:
		status = []any{"done", "interrupt-id-mismatch", "error"}
	case !req.Has("interrupt-id") && q.needsInterruptID, q.interruptIgnored:
		status = []any{"done"}
	}
	if status != nil {
		s.mu.Unlock()
		s.send(c, req, map[string]any{"status": status})
		return
	}
	sess.running = nil
	queue := sess.queue
	sess.queue = nil
	sess.dead = sess.dead || q.diesOnInterrupt
	s.mu.Unlock()
	to, status := run.c, []any{"done", "interrupted"}
	if q.interruptHere {
		to = c
	}
	if q.noInterrupted {
		status = []any{"done"}
	}
	s.send(to, run.req, map[string]any{"status": status})
	s.send(c, req, map[string]any{"status": []any{"done"}})
	if !q.dropQueued {
		for _, r := range queue {
			s.eval(r.c, r.req, r.local)
		}
	}
}

func (s *fakeServer) eval(c net.Conn, req nrepl.Message, local map[string]bool) {
	q := s.q
	sess, ok := s.session(req, local)
	if !ok {
		s.send(c, req, unknownSession)
		return
	}
	s.mu.Lock()
	busy := sess.running != nil
	if busy {
		sess.queue = append(sess.queue, fakeRun{c, req, local})
	}
	if _, ok := req["line"].(string); ok && q.typesKill {
		sess.dead = true
	}
	skip := busy || sess.dead || q.oneEvalAtATime && s.anyRunning()
	s.mu.Unlock()
	if skip {
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
	if code == "" && q.emptyCodeHangs {
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
		if q.stateAfterDone {
			s.send(c, req, map[string]any{"changed-namespaces": map[string]any{}})
		}
	}
	var values []string
	for _, form := range forms(code) {
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
			if q.errorThenState {
				s.send(c, req, map[string]any{"changed-namespaces": map[string]any{}})
			}
			finish()
			return
		case "sleep":
			// Running before it says so, so an interrupt can't miss it.
			s.mu.Lock()
			sess.running = &fakeRun{c, req, local}
			s.mu.Unlock()
			s.send(c, req, map[string]any{"out": "proof"})
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
		if q.printNewline && req.Has("nrepl.middleware.print/print") {
			valueMsg["value"] = value + "\n"
		}
		if q.intValue {
			if n, err := strconv.Atoi(value); err == nil {
				valueMsg["value"] = n
			}
		}
		if q.lastValueOnly || q.loadFileNoValue && req.Str("op") == "load-file" {
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
