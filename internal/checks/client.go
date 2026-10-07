package checks

import (
	"time"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

// Request fields that servers choke on when they're of the wrong type.
var (
	requestStringFields = []string{"op", "session", "code", "ns", "stdin"}
	requestIntFields    = []string{"line", "column"}
)

// ClientRules returns the rules for what a client sends, graded against
// the traffic recorded by proof proxy. Here a failure means that some
// server won't work properly with the client.
func ClientRules() []*check.Rule {
	return []*check.Rule{
		{
			ID:       "client.bencode",
			Title:    "Every frame is valid bencode",
			Severity: check.Fail,
			Why:      "Servers can't read past a broken frame: nREPL closes the connection and babashka.nrepl stops answering on it.",
			Refs:     []check.Ref{nreplConnLoop, bbSessionLoop, specProtocol},
			Inspect:  eachFrame(nrepl.Sent, badFrame),
		},
		{
			ID:       "client.dict",
			Title:    "Every request is a dict",
			Severity: check.Fail,
			Why:      "nREPL closes the connection on a request that isn't a dict, and babashka.nrepl stops answering on it.",
			Refs:     []check.Ref{nreplConnLoop, bbSessionLoop, specProtocol},
			Inspect:  eachFrame(nrepl.Sent, notDict),
		},
		{
			ID:       "client.canonical",
			Title:    "Frames use canonical bencode",
			Severity: check.Warn,
			Why:      "The servers we know of accept unsorted keys and leading zeros, but it's invalid bencode and a stricter decoder would reject it.",
			Refs:     []check.Ref{specProtocol},
			Inspect:  eachFrame(nrepl.Sent, nonCanonical),
		},
		{
			ID:       "client.id",
			Title:    "Every request has an id",
			Severity: check.Fail,
			Why:      "Replies to a request without an id can't be told apart from the rest: nREPL sends them without one, babashka.nrepl with \"unknown\" and Basilisp with \"\".",
			Refs:     []check.Ref{nreplReplyID, bbUnknownID, basilispReplyID, specProtocol},
			Inspect: eachRequest(func(m nrepl.Message, report check.Reporter) {
				if !m.Has("id") {
					report("a request without an id", m.String())
				}
			}),
		},
		{
			ID:       "client.active-id",
			Title:    "An id isn't reused while its request is active",
			Severity: check.Fail,
			Why:      "Replies are matched to requests by id, so two active requests with the same id get each other's replies (e.g. an eval ends at the done meant for a stdin request that reused its id).",
			Refs:     []check.Ref{specProtocol},
			Inspect:  reusedIDs,
		},
		{
			ID:       "client.field-types",
			Title:    "Request fields have the right types",
			Severity: check.Fail,
			Why:      "Servers break on fields of the wrong type: a line, column or stdin nREPL doesn't expect kills the session's thread, and nREPL and Basilisp never answer an eval whose ns isn't a string.",
			Refs:     []check.Ref{nreplLineColumn, issue("nrepl/nrepl", 477), nreplEvalNs, basilispEvalNs, basilispOp},
			Inspect: eachRequest(func(m nrepl.Message, report check.Reporter) {
				wrongTypes[string](m, requestStringFields, report)
				wrongTypes[int64](m, requestIntFields, report)
			}),
		},
		{
			ID:       "client.required-fields",
			Title:    "Requests carry the fields their op needs",
			Severity: check.Fail,
			Why:      "Servers can't do what such requests ask: an eval without code evaluates nothing, and stdin or interrupt without a session can't reach the eval they're meant for.",
			Refs:     []check.Ref{specProtocol, nreplNoCode, nreplStdinSession, nreplInterruptNoSes},
			Inspect: eachRequest(func(m nrepl.Message, report check.Reporter) {
				if !m.Has("op") {
					report("a request without an op", m.String())
				}
				switch m.Str("op") {
				case "eval":
					if !m.Has("code") {
						report("an eval request without code", m.String())
					}
				case "stdin":
					if !m.Has("stdin") {
						report("a stdin request without stdin", m.String())
					}
					if !m.Has("session") {
						report("a stdin request without a session", m.String())
					}
				case "interrupt":
					if !m.Has("session") {
						report("an interrupt request without a session", m.String())
					}
				}
			}),
		},
		{
			ID:         "client.need-input",
			Title:      "need-input is answered with stdin in the same session",
			Severity:   check.Fail,
			Why:        "Code reading input waits until it gets some, so an unanswered need-input leaves the eval, and the session it runs in, hanging forever.",
			Refs:       []check.Ref{nreplStdinSession, specStdin},
			InspectAll: unansweredInput,
		},
		{
			ID:         "client.close",
			Title:      "Sessions are closed before disconnecting",
			Severity:   check.Warn,
			Why:        "Sessions outlive connections, so nREPL keeps every session a client doesn't close (along with its thread) until the server stops.",
			Refs:       []check.Ref{nreplSessions, specClose},
			InspectAll: unclosedSessions,
		},
		{
			ID:       "client.unknown-op",
			Title:    "Requests use ops the server supports",
			Severity: check.Warn,
			Why:      "A request for an op the server doesn't support only gets unknown-op back. describe lists the supported ops, and CIDER checks it before using one.",
			Refs:     []check.Ref{ciderOpSupported, specDescribe},
			Inspect: eachReply(func(r reply, report check.Reporter) {
				if !r.Msg.HasStatus("unknown-op") || r.Req == nil {
					return
				}
				// Requests without a proper op are client.required-fields'
				// and client.field-types' business.
				if op := r.Req.Str("op"); op != "" {
					report(op+" isn't supported by the server", r.Req.String())
				}
			}),
		},
	}
}

func eachRequest(f func(nrepl.Message, check.Reporter)) func([]nrepl.Event, check.Reporter) {
	return eachMessageIn(nrepl.Sent, f)
}

// hungUp reports whether the client closed the connection while the
// server was still there, rather than after the server hung up (or proof
// cut it off). What a client leaves behind (sessions, evals waiting for
// input) only counts against it then. That includes replies that came
// after it left, as it didn't wait for them.
func hungUp(events []nrepl.Event) bool {
	for _, ev := range events {
		if ev.Closed {
			return ev.Dir == nrepl.Sent
		}
	}
	return false
}

func reusedIDs(events []nrepl.Event, report check.Reporter) {
	active := map[string]bool{}
	for _, ev := range events {
		key, ok := idKey(ev.Msg)
		if !ok {
			continue
		}
		switch {
		case ev.Dir == nrepl.Received && ev.Msg.HasStatus("done"):
			delete(active, key)
		case ev.Dir == nrepl.Sent:
			if active[key] {
				report("an id was reused while its request was still active", ev.Msg.String())
			}
			active[key] = true
		}
	}
}

// eachSessionRequest calls f for every request in a session, on any
// connection, since sessions outlive the connection that created them.
func eachSessionRequest(traffic []check.Traffic, f func(ev nrepl.Event, session string)) {
	for _, tr := range traffic {
		for _, ev := range tr.Events {
			if ev.Dir == nrepl.Sent && ev.Msg != nil && ev.Msg.Str("session") != "" {
				f(ev, ev.Msg.Str("session"))
			}
		}
	}
}

func unansweredInput(traffic []check.Traffic, reporter func(check.Traffic) check.Reporter) {
	// Sending input answers a need-input, and interrupting the eval or
	// closing the session gives up on it. Any of them can come from any
	// connection. An interrupt with an interrupt-id only stops the eval it
	// names, and the rest cover the whole session.
	type eval struct{ session, id string }
	bySession, byEval := map[string]time.Time{}, map[eval]time.Time{}
	eachSessionRequest(traffic, func(ev nrepl.Event, session string) {
		switch op := ev.Msg.Str("op"); {
		case op == "interrupt" && ev.Msg.Has("interrupt-id"):
			e := eval{session, valueKey(ev.Msg["interrupt-id"])}
			byEval[e] = latest(byEval[e], ev.Time)
		case op == "stdin", op == "interrupt", op == "close":
			bySession[session] = latest(bySession[session], ev.Time)
		}
	})
	answered := func(session string, w nrepl.Event) bool {
		id, _ := idKey(w.Msg)
		return !bySession[session].Before(w.Time) || !byEval[eval{session, id}].Before(w.Time)
	}
	for _, tr := range traffic {
		if !hungUp(tr.Events) {
			continue
		}
		requests := map[string]nrepl.Message{}
		// waiting maps sessions to the need-input asking for their input.
		waiting := map[string]nrepl.Event{}
		var order []string
		for _, ev := range tr.Events {
			m := ev.Msg
			id, ok := idKey(m)
			if !ok {
				continue
			}
			switch {
			case ev.Dir == nrepl.Sent:
				requests[id] = m
			case m.HasStatus("need-input"):
				session := m.Str("session")
				if session == "" {
					session = requests[id].Str("session")
				}
				if _, ok := waiting[session]; !ok {
					order = append(order, session)
				}
				waiting[session] = ev
			case m.HasStatus("done"):
				for session, w := range waiting {
					if key, _ := idKey(w.Msg); key == id {
						delete(waiting, session)
					}
				}
			}
		}
		for _, session := range order {
			if w, ok := waiting[session]; ok && !answered(session, w) {
				reporter(tr)("need-input went unanswered", w.Msg.String())
				delete(waiting, session)
			}
		}
	}
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func unclosedSessions(traffic []check.Traffic, reporter func(check.Traffic) check.Reporter) {
	closed := map[string]bool{}
	eachSessionRequest(traffic, func(ev nrepl.Event, session string) {
		if ev.Msg.Str("op") == "close" {
			closed[session] = true
		}
	})
	for _, tr := range traffic {
		if !hungUp(tr.Events) {
			continue
		}
		eachReply(func(r reply, report check.Reporter) {
			if s := r.Msg.Str("new-session"); r.Req.Str("op") == "clone" && s != "" && !closed[s] {
				report("a session was never closed", r.Msg.String())
			}
		})(tr.Events, reporter(tr))
	}
}
