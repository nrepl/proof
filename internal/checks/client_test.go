package checks

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/nrepl/proof/bencode"
	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/internal/check/checktest"
	"github.com/nrepl/proof/internal/proxy"
	"github.com/nrepl/proof/nrepl"
)

// clientQuirks switch on one mistake each in the scripted client, so the
// tests can confirm every client rule catches what it claims to and
// nothing else.
type clientQuirks struct {
	badFrame       bool // sends something that isn't bencode
	notDict        bool // sends a list instead of a dict
	unsortedKeys   bool
	noID           bool
	reusedID       bool // answers need-input with the id of the eval asking for it
	stringLine     bool // line is a string
	noCode         bool // an eval without code
	stdinNoSession bool
	ignoreInput    bool // never sends the input an eval waits for
	leaveOpen      bool // never closes its session
	closeElsewhere bool // closes its session from another connection
	unknownOp      bool
	// stayConnected leaves the client connected when the proxy stops.
	stayConnected bool
}

// runClient puts a scripted client session through the proxy to the fake
// server and grades what the client sent.
func runClient(t *testing.T, q clientQuirks) map[string]check.Result {
	t.Helper()
	px, err := proxy.Listen("127.0.0.1:0", startFake(t, quirks{}))
	if err != nil {
		t.Fatal(err)
	}
	go px.Serve()
	t.Cleanup(func() { px.Stop(0) })

	dial := func() *nrepl.Conn {
		t.Helper()
		c, err := nrepl.Dial(px.Addr(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	c := dial()
	request := func(m nrepl.Message) nrepl.Response {
		t.Helper()
		resp, err := c.Request(m, time.Second)
		if err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		return resp
	}
	request(nrepl.Message{"op": "describe"})
	session := nrepl.Message{"session": request(nrepl.Message{"op": "clone"}).Str("new-session")}

	eval := nrepl.Message{"op": "eval", "code": "value", "line": 1, "column": 1}
	if q.stringLine {
		eval["line"] = "1"
	}
	if q.noCode {
		delete(eval, "code")
	}
	evalID := request(session.With(eval)).Str("id")

	id, err := c.Send(session.With(nrepl.Message{"op": "eval", "code": "read"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitFor(needInput(id), time.Second); err != nil {
		t.Fatal(err)
	}
	if !q.ignoreInput {
		stdin := nrepl.Message{"op": "stdin", "stdin": "proof\n"}
		if !q.stdinNoSession {
			stdin = session.With(stdin)
		}
		if q.reusedID {
			stdin["id"] = id
		}
		request(stdin)
	}
	// An interrupt for an eval that's long done, which doesn't give up on
	// any input.
	request(session.With(nrepl.Message{"op": "interrupt", "interrupt-id": evalID}))
	if q.unknownOp {
		request(nrepl.Message{"op": "proof-unknown"})
	}
	closing := session.With(nrepl.Message{"op": "close"})
	if !q.leaveOpen && !q.stayConnected && !q.closeElsewhere {
		request(closing)
	}
	if q.closeElsewhere {
		c.Close()
		c = dial()
		request(closing)
		c.Close()
	}

	// Frames nrepl.Conn won't send go over connections of their own.
	var frames [][]byte
	if q.badFrame {
		frames = append(frames, []byte("hello\n"))
	}
	if q.notDict {
		frames = append(frames, []byte("l8:describee"))
	}
	if q.unsortedKeys {
		frames = append(frames, []byte("d2:op8:describe2:id1:xe"))
	}
	if q.noID {
		b, _ := bencode.Marshal(map[string]any{"op": "describe"})
		frames = append(frames, b)
	}
	for _, f := range frames {
		nc, err := net.Dial("tcp", px.Addr())
		if err != nil {
			t.Fatal(err)
		}
		nc.Write(f)
		// Hang up, and wait for the proxy to do the same, so the exchange
		// is over by the time the proxy stops.
		nc.(*net.TCPConn).CloseWrite()
		nc.SetReadDeadline(time.Now().Add(time.Second))
		io.Copy(io.Discard, nc)
		nc.Close()
	}

	grace := time.Second
	if q.stayConnected {
		grace = 50 * time.Millisecond
	} else {
		c.Close()
	}
	traffic := px.Stop(grace)
	want := 1 + len(frames)
	if q.closeElsewhere {
		want++
	}
	if len(traffic) != want {
		t.Fatalf("got %d transcripts, want %d", len(traffic), want)
	}
	return checktest.ByID(check.Grade(ClientRules(), traffic))
}

func TestWellBehavedClientPassesEverything(t *testing.T) {
	checktest.Verdicts(t, runClient(t, clientQuirks{}), nil)
}

// Each mistake must produce exactly the listed verdicts, and every other
// rule must still pass.
func TestClientRulesCatchMistakes(t *testing.T) {
	F, W := check.Failed, check.Warned
	cases := []struct {
		name string
		q    clientQuirks
		want map[string]check.Verdict
	}{
		{"broken frame", clientQuirks{badFrame: true}, map[string]check.Verdict{"client.bencode": F}},
		{"list instead of a dict", clientQuirks{notDict: true}, map[string]check.Verdict{"client.dict": F}},
		{"unsorted keys", clientQuirks{unsortedKeys: true}, map[string]check.Verdict{"client.canonical": W}},
		{"no id", clientQuirks{noID: true}, map[string]check.Verdict{"client.id": F}},
		{"id reused by stdin", clientQuirks{reusedID: true}, map[string]check.Verdict{"client.active-id": F}},
		{"line is a string", clientQuirks{stringLine: true}, map[string]check.Verdict{"client.field-types": F}},
		{"eval without code", clientQuirks{noCode: true}, map[string]check.Verdict{"client.required-fields": F}},
		{"stdin without a session", clientQuirks{stdinNoSession: true, leaveOpen: true}, map[string]check.Verdict{
			"client.required-fields": F, "client.need-input": F, "client.close": W}},
		{"input never sent", clientQuirks{ignoreInput: true, leaveOpen: true}, map[string]check.Verdict{
			"client.need-input": F, "client.close": W}},
		{"session closed instead of sending input", clientQuirks{ignoreInput: true}, nil},
		{"session left open", clientQuirks{leaveOpen: true}, map[string]check.Verdict{"client.close": W}},
		{"session closed from another connection", clientQuirks{closeElsewhere: true}, nil},
		{"unsupported op", clientQuirks{unknownOp: true}, map[string]check.Verdict{"client.unknown-op": W}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			checktest.Verdicts(t, runClient(t, c.q), c.want)
		})
	}
}

// A client that's still connected when proof stops hasn't had the chance
// to answer need-input or close its sessions, so it isn't judged on them.
func TestOpenConnectionsAreNotJudgedOnWhatTheyLeaveBehind(t *testing.T) {
	results := runClient(t, clientQuirks{ignoreInput: true, stayConnected: true})
	for _, id := range []string{"client.need-input", "client.close"} {
		if v := results[id].Verdict; v != check.Pass {
			t.Errorf("%s: got %s %v, want pass", id, v, results[id].Details)
		}
	}
}

// What a client leaves behind only counts when it left first. Replies
// that came after that still count, as it didn't wait for them.
func TestLeftoversCountOnlyWhenTheClientLeftFirst(t *testing.T) {
	clone := nrepl.Event{Dir: nrepl.Sent, Msg: nrepl.Message{"id": "1", "op": "clone"}}
	cloned := nrepl.Event{Dir: nrepl.Received, Msg: nrepl.Message{"id": "1", "new-session": "s", "status": []any{"done"}}}
	eval := nrepl.Event{Dir: nrepl.Sent, Msg: nrepl.Message{"id": "2", "op": "eval", "code": "(read-line)", "session": "s"}}
	needInput := nrepl.Event{Dir: nrepl.Received, Msg: nrepl.Message{"id": "2", "session": "s", "status": []any{"need-input"}}}
	interrupt := nrepl.Event{Dir: nrepl.Sent, Msg: nrepl.Message{"id": "3", "op": "interrupt", "session": "s", "interrupt-id": "2"}}
	otherInterrupt := nrepl.Event{Dir: nrepl.Sent, Msg: nrepl.Message{"id": "4", "op": "interrupt", "session": "s", "interrupt-id": "1"}}
	interrupted := nrepl.Event{Dir: nrepl.Received, Msg: nrepl.Message{"id": "2", "session": "s", "status": []any{"done", "interrupted"}}}
	clientGone := nrepl.Event{Dir: nrepl.Sent, Closed: true}
	serverGone := nrepl.Event{Dir: nrepl.Received, Closed: true}
	cases := []struct {
		name   string
		events []nrepl.Event
		rule   string
		want   check.Verdict
	}{
		{"session, client first", []nrepl.Event{clone, cloned, clientGone, serverGone}, "client.close", check.Warned},
		{"session, server first", []nrepl.Event{clone, cloned, serverGone, clientGone}, "client.close", check.Pass},
		{"session the client didn't wait for", []nrepl.Event{clone, clientGone, cloned, serverGone}, "client.close", check.Warned},
		{"input, client first", []nrepl.Event{eval, needInput, clientGone, serverGone}, "client.need-input", check.Failed},
		{"input, server first", []nrepl.Event{eval, needInput, serverGone, clientGone}, "client.need-input", check.Pass},
		{"input the client didn't wait for", []nrepl.Event{eval, clientGone, needInput, serverGone}, "client.need-input", check.Failed},
		{"interrupted before leaving", []nrepl.Event{eval, needInput, interrupt, clientGone, interrupted, serverGone}, "client.need-input", check.Pass},
		{"interrupted, then gone before the done", []nrepl.Event{eval, needInput, interrupt, clientGone, serverGone}, "client.need-input", check.Pass},
		{"some other eval interrupted", []nrepl.Event{eval, needInput, otherInterrupt, clientGone, serverGone}, "client.need-input", check.Failed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Input is matched up with need-input by time.
			events := make([]nrepl.Event, len(c.events))
			for i, ev := range c.events {
				ev.Time = time.Unix(int64(i), 0)
				events[i] = ev
			}
			results := checktest.ByID(check.Grade(ClientRules(), []check.Traffic{{Label: "connection 1", Events: events}}))
			checktest.Verdicts(t, results, map[string]check.Verdict{c.rule: c.want})
		})
	}
}
