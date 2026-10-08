package serve

import (
	"io"
	"maps"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/internal/check/checktest"
	"github.com/nrepl/proof/internal/checks"
	"github.com/nrepl/proof/nrepl"
)

const timeout = 5 * time.Second

func connect(t *testing.T, s *Server) *nrepl.Conn {
	t.Helper()
	c, err := nrepl.Dial(s.Addr(), timeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func request(t *testing.T, c *nrepl.Conn, m nrepl.Message) nrepl.Response {
	t.Helper()
	r, err := c.Request(m, timeout)
	if err != nil {
		t.Fatalf("%v: %v", m, err)
	}
	return r
}

func clone(t *testing.T, c *nrepl.Conn) string {
	t.Helper()
	return request(t, c, nrepl.Message{"op": "clone"}).Str("new-session")
}

func eval(t *testing.T, c *nrepl.Conn, session, code string) nrepl.Response {
	t.Helper()
	return request(t, c, nrepl.Message{"op": "eval", "code": code, "session": session})
}

func TestEvaluatesLikeClojure(t *testing.T) {
	s := serveFor(t)
	cases := []struct {
		code   string
		values []string
		out    string
		err    string
	}{
		{code: "(+ 1 2)", values: []string{"3"}},
		{code: "1 2", values: []string{"1", "2"}},
		{code: `:k "s\n" nil true [1 "a"] {:a 1 :b "c"} '(1 x)`,
			values: []string{":k", `"s\n"`, "nil", "true", `[1 "a"]`, `{:a 1, :b "c"}`, "(1 x)"}},
		{code: "(- 5) (/ 1 2) (/ 4 2) (/ 2) (* 2 3 4) (inc 1) (dec 1)",
			values: []string{"-5", "1/2", "2", "1/2", "24", "2", "0"}},
		{code: `(str "a" nil 1 :k) (apply str (repeat 3 "ab"))`, values: []string{`"a1:k"`, `"ababab"`}},
		{code: "(if nil 1 2) (do 1 2)", values: []string{"2", "2"}},
		{code: `(print "proof")`, values: []string{"nil"}, out: "proof"},
		{code: `(println "a" 1 :k nil ["x"]) (prn "a" {:a "b"})`, values: []string{"nil", "nil"},
			out: "a 1 :k nil [x]\n\"a\" {:a \"b\"}\n"},
		{code: `(binding [*out* *err*] (print "proof"))`, values: []string{"nil"}, err: "proof"},
		{code: "(def x 1) x user/x", values: []string{"#'user/x", "1", "1"}},
		{code: "(let [x 1 y (+ x 1)] y) (when 1 2) (when nil 2) (when-let [x nil] 1) (when-let [x 3] x)",
			values: []string{"2", "2", "nil", "nil", "3"}},
		{code: "(require 'clojure.stacktrace) (def y 1) (resolve 'y) (resolve 'nope) @(resolve 'y) @#'y",
			values: []string{"nil", "#'user/y", "#'user/y", "nil", "1", "1"}},
		{code: `(ex-data (ex-info "x" {:a 1}))`, values: []string{"{:a 1}"}},
		{code: "(clojure.core// 4 2) (resolve '/) (resolve 'clojure.core/str)", values: []string{"2", "#'clojure.core//", "#'clojure.core/str"}},
		{code: "#'nope", err: "Syntax error compiling var at (REPL:0:0).\nUnable to resolve var: nope in this context\n"},
		{code: "@(future 1)", err: "Execution error (UnsupportedOperationException) at user/eval1 (REPL:1).\n" +
			"proof serve runs futures once the eval is done, so it can't wait for one\n"},
		{code: "(/ 1 0) 42", values: []string{"42"}, err: "Execution error (ArithmeticException) at user/eval1 (REPL:1).\nDivide by zero\n"},
		{code: `(str [1 "a"] {:a "b"} '(1 "c") *ns* #'clojure.core/str)`,
			values: []string{`"[1 \"a\"]{:a \"b\"}(1 \"c\")user#'clojure.core/str"`}},
		{code: "(+ 9223372036854775807 1)", err: "Execution error (ArithmeticException) at user/eval1 (REPL:1).\nlong overflow\n"},
		{code: "(Thread/sleep -1)", err: "Execution error (IllegalArgumentException) at user/eval1 (REPL:1).\ntimeout value is negative\n"},
		{code: "(let)", err: "Execution error (IllegalArgumentException) at user/eval1 (REPL:1).\nlet needs a vector of names and values\n"},
		{code: "(let [z 1] z) z", values: []string{"1"}, err: "Syntax error compiling at (REPL:0:0).\nUnable to resolve symbol: z in this context\n"},
		{code: "(in-ns 'clojure.core) (def zz 5) @#'zz", values: []string{`#object[clojure.lang.Namespace 0x1 "clojure.core"]`,
			"#'clojure.core/zz", "5"}},
		{code: `1 "abc`, values: []string{"1"}, err: "Syntax error reading source at (REPL:2:1).\nEOF while reading string\n"},
		{code: "(in-ns 'foo) *ns*", values: []string{`#object[clojure.lang.Namespace 0x1 "foo"]`,
			`#object[clojure.lang.Namespace 0x1 "foo"]`}},
		{code: "(/ 1 0)", err: "Execution error (ArithmeticException) at user/eval1 (REPL:1).\nDivide by zero\n"},
		{code: `(throw (ex-info "proof" {}))`, err: "Execution error (ExceptionInfo) at user/eval1 (REPL:1).\nproof\n"},
		{code: `(+ 1 "a")`, err: "Execution error (ClassCastException) at user/eval1 (REPL:1).\n" +
			"class java.lang.String cannot be cast to class java.lang.Number\n"},
		{code: "(1 2)", err: "Execution error (ClassCastException) at user/eval1 (REPL:1).\n" +
			"class java.lang.Long cannot be cast to class clojure.lang.IFn\n"},
		{code: "foo", err: "Syntax error compiling at (REPL:0:0).\nUnable to resolve symbol: foo in this context\n"},
		{code: "(+ 1", err: "Syntax error reading source at (REPL:2:1).\nEOF while reading, starting at line 1\n"},
		{code: "1 )", values: []string{"1"}, err: "Syntax error reading source at (REPL:1:4).\nUnmatched delimiter: )\n"},
		{code: "#{1}", err: "Syntax error reading source at (REPL:1:2).\nproof serve can't read #\n"},
	}
	c := connect(t, s)
	for _, tc := range cases {
		r := eval(t, c, clone(t, c), tc.code)
		if got := r.Values(); !slices.Equal(got, tc.values) {
			t.Errorf("%s: values %q, want %q", tc.code, got, tc.values)
		}
		if r.Out() != tc.out || r.Err() != tc.err {
			t.Errorf("%s: out %q and err %q, want %q and %q", tc.code, r.Out(), r.Err(), tc.out, tc.err)
		}
		if failed := r.HasStatus("eval-error"); failed != (tc.err != "" && !strings.HasPrefix(tc.code, "(binding")) {
			t.Errorf("%s: status %v", tc.code, r.Status())
		}
	}
}

func TestSessionsKeepTheirBindings(t *testing.T) {
	c := connect(t, serveFor(t))
	first, second := clone(t, c), clone(t, c)
	eval(t, c, first, ":marker (in-ns 'foo)")
	if v := eval(t, c, first, "*2 *ns*").Values(); !slices.Equal(v, []string{":marker", `#object[clojure.lang.Namespace 0x1 "foo"]`}) {
		t.Errorf("first session: %q", v)
	}
	if v := eval(t, c, second, "*1 *ns*").Values(); !slices.Equal(v, []string{"nil", `#object[clojure.lang.Namespace 0x1 "user"]`}) {
		t.Errorf("second session: %q", v)
	}
	r := eval(t, c, first, "(throw (ex-info \"proof\" {:a 1}))")
	if !r.HasStatus("eval-error") {
		t.Fatal(r.Status())
	}
	if v := eval(t, c, first, "*e").Values(); len(v) != 1 || !strings.Contains(v[0], ":cause \"proof\"\n :data {:a 1}") {
		t.Errorf("*e: %q", v)
	}
	eval(t, c, first, "(+ 1")
	if v := eval(t, c, first, "*e").Values(); len(v) != 1 || !strings.Contains(v[0], `:cause "EOF while reading`) {
		t.Errorf("a read error didn't set *e: %q", v)
	}
	// A namespace sent with an eval is only for that eval.
	in := func(ns, code string) []string {
		return request(t, c, nrepl.Message{"op": "eval", "code": code, "session": second, "ns": ns}).Values()
	}
	in("user", "(in-ns 'qqq)")
	if v := in("qqq", "(str *ns*)"); !slices.Equal(v, []string{`"qqq"`}) {
		t.Errorf("eval in qqq: %q", v)
	}
	if v := eval(t, c, second, "(str *ns*)").Values(); !slices.Equal(v, []string{`"user"`}) {
		t.Errorf("after evals with a namespace: %q", v)
	}
	// A clone starts with the bindings of the session it's cloned from.
	cloned := request(t, c, nrepl.Message{"op": "clone", "session": first}).Str("new-session")
	if v := eval(t, c, cloned, "*ns*").Values(); !slices.Equal(v, []string{`#object[clojure.lang.Namespace 0x1 "foo"]`}) {
		t.Errorf("clone: %q", v)
	}
}

func TestFutureOutputComesAfterDone(t *testing.T) {
	c := connect(t, serveFor(t))
	session := clone(t, c)
	r := eval(t, c, session, `(let [s "late"] (future (Thread/sleep 10) (println s)))`)
	if !strings.Contains(r.Values()[0], "future_call") {
		t.Errorf("value %q", r.Values())
	}
	late, err := c.WaitFor(func(m nrepl.Message) bool { return m.Str("out") == "late\n" }, timeout)
	if err != nil {
		t.Fatal(err)
	}
	id := r.Messages[0].Str("id")
	if late.Str("id") != id || late.Str("session") != session {
		t.Errorf("late output %v doesn't belong to the eval", late)
	}
	if got := kinds(c, id); !slices.Equal(got, []string{"value", "done", "out"}) {
		t.Errorf("the eval got %v, want the output after done", got)
	}
}

func TestInput(t *testing.T) {
	cases := []struct {
		scenario string
		inputs   []string
		values   []string
		err      string
	}{
		{inputs: []string{"proof\n", ""}, values: []string{`"proof"`, "nil"}},
		{inputs: []string{"pro", "of\nmore\n"}, values: []string{`"proof"`, `"more"`}},
		{inputs: []string{"partial", "", ""}, values: []string{`"partial"`, "nil"}},
		{inputs: []string{"", ""}, values: []string{"nil", "nil"}},
		{scenario: "eof-error", inputs: []string{"", ""},
			err: strings.Repeat("Execution error (ClassCastException) at user/eval1 (REPL:1).\n"+
				"class java.lang.Long cannot be cast to class java.lang.Character\n", 2)},
		{scenario: "no-stdin", values: []string{"nil", "nil"}},
		{scenario: "read-line-throws",
			err: strings.Repeat("Execution error (ExceptionInfo) at user/eval1 (REPL:1).\nTODO: port read-line\n", 2)},
	}
	for _, tc := range cases {
		t.Run(strings.Join(append([]string{tc.scenario}, tc.inputs...), "|"), func(t *testing.T) {
			var scenarios []string
			if tc.scenario != "" {
				scenarios = append(scenarios, tc.scenario)
			}
			c := connect(t, serveFor(t, scenarios...))
			session := clone(t, c)
			id, err := c.Send(nrepl.Message{"op": "eval", "code": "(read-line) (read-line)", "session": session})
			if err != nil {
				t.Fatal(err)
			}
			for _, input := range tc.inputs {
				if _, err := c.WaitFor(func(m nrepl.Message) bool { return m.HasStatus("need-input") }, timeout); err != nil {
					t.Fatal(err)
				}
				request(t, c, nrepl.Message{"op": "stdin", "stdin": input, "session": session})
			}
			r, err := c.Collect(id, timeout)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(r.Values(), tc.values) || r.Err() != tc.err {
				t.Errorf("values %q and err %q, want %q and %q", r.Values(), r.Err(), tc.values, tc.err)
			}
		})
	}
}

// received returns what the server sent for a request so far, done or not.
func received(c *nrepl.Conn, id string) nrepl.Response {
	var r nrepl.Response
	for _, ev := range c.Transcript() {
		if ev.Dir == nrepl.Received && ev.Msg.Str("id") == id {
			r.Messages = append(r.Messages, ev.Msg)
		}
	}
	return r
}

// kinds says what each message the server sent for a request so far was,
// in order.
func kinds(c *nrepl.Conn, id string) []string {
	var ks []string
	for _, m := range received(c, id).Messages {
		for _, k := range []string{"done", "need-input", "eval-error"} {
			if m.HasStatus(k) {
				ks = append(ks, k)
			}
		}
		for _, k := range []string{"out", "err", "value"} {
			if m.Has(k) {
				ks = append(ks, k)
			}
		}
	}
	return ks
}

func TestInterrupt(t *testing.T) {
	c := connect(t, serveFor(t))
	session := clone(t, c)
	if r := request(t, c, nrepl.Message{"op": "interrupt", "session": session}); !r.HasStatus("session-idle", "done") {
		t.Errorf("idle session: %v", r.Status())
	}
	if r := request(t, c, nrepl.Message{"op": "interrupt"}); !r.HasStatus("error", "session-ephemeral", "done") {
		t.Errorf("no session: %v", r.Status())
	}
	// What each of them sends once it's running.
	running := map[string]string{
		`(do (print "sleeping") (Thread/sleep 60000)) :after`: "out",
		"(read-line) :after": "need-input",
	}
	for code, started := range running {
		id, err := c.Send(nrepl.Message{"op": "eval", "code": code, "session": session})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.WaitFor(func(m nrepl.Message) bool { return m.Str("id") == id }, timeout); err != nil {
			t.Fatal(err)
		}
		r := request(t, c, nrepl.Message{"op": "interrupt", "session": session, "interrupt-id": "something else"})
		if !r.HasStatus("error", "interrupt-id-mismatch", "done") {
			t.Errorf("%s: interrupting some other eval: %v", code, r.Status())
		}
		interrupt, err := c.Send(nrepl.Message{"op": "interrupt", "session": session, "interrupt-id": id})
		if err != nil {
			t.Fatal(err)
		}
		// Like on nREPL, the eval is done before the interrupt, and the rest
		// of its code runs after that, without another done.
		if _, err := c.WaitFor(func(m nrepl.Message) bool { return m.Str("id") == id && m.Has("value") }, timeout); err != nil {
			t.Fatal(err)
		}
		c.Settle(50*time.Millisecond, time.Second)
		if got, want := kinds(c, id), []string{started, "done", "err", "eval-error", "value"}; !slices.Equal(got, want) {
			t.Errorf("%s: the eval got %v, want %v", code, got, want)
		}
		var order []string
		for _, ev := range c.Transcript() {
			if ev.Msg.HasStatus("done") && (ev.Msg.Str("id") == id || ev.Msg.Str("id") == interrupt) {
				order = append(order, ev.Msg.Str("id"))
			}
		}
		if !slices.Equal(order, []string{id, interrupt}) {
			t.Errorf("%s: dones came in the order %v, want the eval's first", code, order)
		}
		if all := received(c, id); !all.HasStatus("done", "interrupted") || !strings.Contains(all.Err(), "(InterruptedException)") {
			t.Errorf("%s: interrupted eval: %v %q", code, all.Status(), all.Err())
		}
	}
	// The session goes on.
	if v := eval(t, c, session, "(+ 1 2)").Values(); !slices.Equal(v, []string{"3"}) {
		t.Errorf("after the interrupts: %q", v)
	}
}

func TestClosingASessionInterruptsItsEval(t *testing.T) {
	c := connect(t, serveFor(t))
	session := clone(t, c)
	id, err := c.Send(nrepl.Message{"op": "eval", "code": `(do (print "sleeping") (Thread/sleep 60000)) :after`, "session": session})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitFor(func(m nrepl.Message) bool { return m.Str("out") == "sleeping" }, timeout); err != nil {
		t.Fatal(err)
	}
	if r := request(t, c, nrepl.Message{"op": "close", "session": session}); !r.HasStatus("session-closed") {
		t.Errorf("close: %v", r.Status())
	}
	r, err := c.Collect(id, timeout)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Err(), "(InterruptedException)") || !slices.Equal(r.Values(), []string{":after"}) || r.HasStatus("interrupted") {
		t.Errorf("the eval got %v", r.Messages)
	}
}

// The scenarios checked here don't change any verdict of the server
// checks, so the matrix test can't tell whether they work.
func TestScenarioShapes(t *testing.T) {
	cases := []struct {
		scenario string
		code     string
		want     func(r nrepl.Response) bool
	}{
		{"split-output", `(print "aä€")`, func(r nrepl.Response) bool {
			var chunks []string
			for _, m := range r.Messages {
				if m.Has("out") {
					chunks = append(chunks, m.Str("out"))
				}
			}
			return slices.Equal(chunks, []string{"a", "ä", "€"})
		}},
		{"empty-messages", "1", func(r nrepl.Response) bool {
			return slices.ContainsFunc(r.Messages, func(m nrepl.Message) bool { return len(m) == 2 && m.Has("id") && m.Has("session") })
		}},
		{"error-with-done", "(/ 1 0) 2", func(r nrepl.Response) bool {
			last := r.Messages[len(r.Messages)-1]
			return len(r.Messages) == 2 && last.HasStatus("eval-error", "done") && last.Has("ex")
		}},
		{"no-err", `(binding [*out* *err*] (print "x")) (/ 1 0)`, func(r nrepl.Response) bool {
			// Only the error report is left.
			return strings.HasPrefix(r.Err(), "Execution error")
		}},
		{"last-value", "1 2 3", func(r nrepl.Response) bool { return slices.Equal(r.Values(), []string{"3"}) }},
		{"last-value", "1 (/ 1 0) 3", func(r nrepl.Response) bool { return len(r.Values()) == 0 && r.HasStatus("eval-error") }},
	}
	for _, tc := range cases {
		t.Run(tc.scenario, func(t *testing.T) {
			c := connect(t, serveFor(t, tc.scenario))
			if r := eval(t, c, clone(t, c), tc.code); !tc.want(r) {
				t.Errorf("unexpected replies: %v", r.Messages)
			}
		})
	}
}

func TestDescribe(t *testing.T) {
	cases := []struct {
		scenarios []string
		ops       []string
		version   any
	}{
		{nil, []string{"clone", "close", "describe", "eval", "interrupt", "stdin"},
			map[string]any{"major": int64(0), "minor": int64(1), "incremental": int64(0), "version-string": "0.1.0-dev"}},
		{[]string{"no-close-op", "no-interrupt", "no-stdin", "string-versions"}, []string{"clone", "describe", "eval"}, "0.1.0-dev"},
		{[]string{"read-line-throws"}, []string{"clone", "close", "describe", "eval", "interrupt"},
			map[string]any{"major": int64(0), "minor": int64(1), "incremental": int64(0), "version-string": "0.1.0-dev"}},
	}
	for _, tc := range cases {
		c := connect(t, serveFor(t, tc.scenarios...))
		r := request(t, c, nrepl.Message{"op": "describe"})
		ops, _ := r.Get("ops").(map[string]any)
		if got := slices.Sorted(maps.Keys(ops)); !slices.Equal(got, tc.ops) {
			t.Errorf("%v: ops %v, want %v", tc.scenarios, got, tc.ops)
		}
		versions, _ := r.Get("versions").(map[string]any)
		if !reflect.DeepEqual(versions["proof"], tc.version) {
			t.Errorf("%v: versions %v", tc.scenarios, versions)
		}
	}
}

func TestOpsTheScenariosTakeAway(t *testing.T) {
	for scenario, op := range map[string]string{"no-interrupt": "interrupt", "no-stdin": "stdin", "read-line-throws": "stdin"} {
		c := connect(t, serveFor(t, scenario))
		if r := request(t, c, nrepl.Message{"op": op, "session": clone(t, c)}); !r.HasStatus("unknown-op") {
			t.Errorf("%s: %s got %v", scenario, op, r.Status())
		}
	}
}

// pipeListener hands out the server's end of a pipe, where every write
// arrives on its own.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{} }

// writes connects a client to a server over a pipe, and returns what each
// write of the replies to an eval carried.
func writes(t *testing.T, scenario string) []string {
	t.Helper()
	b, err := behave([]string{scenario})
	if err != nil {
		t.Fatal(err)
	}
	client, server := net.Pipe()
	ln := &pipeListener{conns: make(chan net.Conn, 1), closed: make(chan struct{})}
	ln.conns <- server
	s := newServer(ln, "0.1.0-dev", b)
	go s.Serve()
	t.Cleanup(func() {
		client.Close()
		s.Stop(0)
	})
	go client.Write([]byte("d4:code11:(print 1) 22:id1:12:op4:evale"))
	var got []string
	buf := make([]byte, 1024)
	client.SetReadDeadline(time.Now().Add(timeout))
	for !strings.Contains(strings.Join(got, ""), "4:done") {
		n, err := client.Read(buf)
		if err != nil {
			t.Fatalf("after %q: %v", got, err)
		}
		got = append(got, string(buf[:n]))
	}
	return got
}

func TestByteWrites(t *testing.T) {
	for _, w := range writes(t, "byte-writes") {
		if len(w) != 1 {
			t.Fatalf("a write of %q", w)
		}
	}
}

func TestBatchedWrites(t *testing.T) {
	// One write for everything up to the done.
	if w := writes(t, "batched-writes"); len(w) != 1 || strings.Count(w[0], "2:id") != 4 {
		t.Errorf("writes %q", w)
	}
}

func TestHangUp(t *testing.T) {
	s := serveFor(t, "hang-up")
	c := connect(t, s)
	request(t, c, nrepl.Message{"op": "describe"})
	if _, err := c.Request(nrepl.Message{"op": "eval", "code": "1"}, timeout); err == nil {
		t.Fatal("the eval got its done")
	}
	traffic := s.Stop(timeout)
	if events := traffic[0].Events; !events[len(events)-1].Closed || events[len(events)-1].Dir != nrepl.Received {
		t.Errorf("the server's hanging up isn't recorded last: %v", events[len(events)-1])
	}
}

func TestStopGradesWhatClientsSent(t *testing.T) {
	s := serveFor(t)
	c := connect(t, s)
	session := clone(t, c)
	// Still sleeping when proof stops, which doesn't hold Stop up.
	c.Send(nrepl.Message{"op": "eval", "code": "(Thread/sleep 60000)", "session": session})
	c.Close()

	start := time.Now()
	traffic := s.Stop(timeout)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Stop took %s", elapsed)
	}
	if len(traffic) != 1 {
		t.Fatalf("traffic: %v", traffic)
	}
	if v := checktest.ByID(check.Grade(checks.ClientRules(), traffic))["client.close"].Verdict; v != check.Warned {
		t.Errorf("the session was never closed, but client.close got %s", v)
	}
}

func TestBrokenFramesEndTheConnection(t *testing.T) {
	s := serveFor(t)
	nc, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	nc.Write([]byte("d2:op8:describeXe"))
	nc.SetReadDeadline(time.Now().Add(timeout))
	if _, err := io.ReadAll(nc); err != nil {
		t.Errorf("the server didn't hang up: %v", err)
	}
}

func TestScenarioNames(t *testing.T) {
	for _, names := range [][]string{{"no-such-thing"}, {"ns-fallback", "ns-error"}, {"byte-writes", "batched-writes"}} {
		if _, err := Listen("127.0.0.1:0", "0", names); err == nil {
			t.Errorf("%v: no error", names)
		}
	}
	for _, sc := range Scenarios() {
		if _, err := behave([]string{sc.Name}); err != nil {
			t.Error(err)
		}
	}
}

func TestRepliesSayWhichSessionTheyRanIn(t *testing.T) {
	c := connect(t, serveFor(t))
	sessions := map[string]bool{}
	for _, req := range []nrepl.Message{{"op": "describe"}, {"op": "eval", "code": "(print 1) 2"}, {"op": "proof/nope"}} {
		r := request(t, c, req)
		for _, m := range r.Messages {
			if m.Str("session") != r.Messages[0].Str("session") || m.Str("session") == "" {
				t.Errorf("%v: replies in different sessions %v", req, r.Messages)
			}
		}
		sessions[r.Messages[0].Str("session")] = true
	}
	if len(sessions) != 3 {
		t.Errorf("each request should get a session of its own, got %v", sessions)
	}
}

func TestCodeThatIsntAString(t *testing.T) {
	c := connect(t, serveFor(t))
	if r := request(t, c, nrepl.Message{"op": "eval", "code": int64(1)}); !r.HasStatus("error", "unknown-code-type", "done") {
		t.Errorf("status %v", r.Status())
	}
}

func TestBatchedWritesDontHoldOutputBack(t *testing.T) {
	c := connect(t, serveFor(t, "batched-writes"))
	c.Send(nrepl.Message{"op": "eval", "code": `(do (print "sleeping") (Thread/sleep 60000))`, "session": clone(t, c)})
	if _, err := c.WaitFor(func(m nrepl.Message) bool { return m.Str("out") == "sleeping" }, timeout); err != nil {
		t.Errorf("output before a sleep: %v", err)
	}
}

func TestHangingUpInTheMiddleOfARequest(t *testing.T) {
	s := serveFor(t)
	nc, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	nc.Write([]byte("d2:id1:12:op5:clonee"))
	nc.SetReadDeadline(time.Now().Add(timeout))
	if _, err := nc.Read(make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
	nc.Write([]byte("d2:op4:eval"))
	nc.Close()
	traffic := s.Stop(timeout)
	if events := traffic[0].Events; !events[len(events)-1].Closed || events[len(events)-1].Dir != nrepl.Sent {
		t.Errorf("the client's hanging up isn't recorded last: %v", events[len(events)-1])
	}
	if v := checktest.ByID(check.Grade(checks.ClientRules(), traffic))["client.close"].Verdict; v != check.Warned {
		t.Errorf("the session was never closed, but client.close got %s", v)
	}
}
