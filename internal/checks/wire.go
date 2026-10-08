package checks

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nrepl/proof/bencode"
	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

// Fields that are strings wherever they appear.
var stringFields = []string{"id", "session", "ns", "value", "out", "err", "new-session", "ex", "root-ex"}

// Keys that may legitimately follow an error status, e.g. on the final
// "done" message.
var afterErrorKeys = map[string]bool{"id": true, "session": true, "status": true, "ns": true}

// Keys that may arrive after done: the spec allows late output.
var afterDoneKeys = map[string]bool{"id": true, "session": true, "out": true, "err": true}

// WireRules returns every wire rule.
func WireRules() []*check.Rule {
	return []*check.Rule{
		{
			ID:       "wire.bencode",
			Title:    "Every frame is valid bencode",
			Severity: check.Fail,
			Why:      "A client's decoder stops at the first broken frame, and the connection is lost with it.",
			Refs:     []check.Ref{specProtocol},
			Inspect:  eachReceived(badFrame),
		},
		{
			ID:       "wire.dict",
			Title:    "Every message is a dict",
			Severity: check.Fail,
			Why:      "Clients look up id, status and the rest by key; any other top-level value can't be routed.",
			Refs:     []check.Ref{specProtocol},
			Inspect:  eachReceived(notDict),
		},
		{
			ID:       "wire.canonical",
			Title:    "Frames use canonical bencode",
			Severity: check.Warn,
			Why:      "Clients tolerate unsorted keys and leading zeros today, but it's invalid bencode and a stricter decoder would reject it.",
			Refs:     []check.Ref{specProtocol},
			Inspect:  eachReceived(nonCanonical),
		},
		{
			ID:       "wire.utf8",
			Title:    "Strings are valid UTF-8",
			Severity: check.Warn,
			Why:      "Clients decode strings as UTF-8, so invalid bytes turn into garbage in the REPL.",
			Refs:     []check.Ref{specProtocol},
			Inspect: eachMessage(func(m nrepl.Message, report check.Reporter) {
				walkStrings("", map[string]any(m), func(path, s string) {
					if !utf8.ValidString(s) {
						report(path+" isn't valid UTF-8", strconv.Quote(s))
					}
				})
			}),
		},
		{
			ID:       "wire.status-type",
			Title:    "status is a list of strings",
			Severity: check.Fail,
			Why:      "CIDER tests status flags with member, which signals an error on anything but a list.",
			Refs:     []check.Ref{ciderDone, specProtocol},
			Inspect: eachMessage(func(m nrepl.Message, report check.Reporter) {
				if !m.Has("status") {
					return
				}
				list, ok := m["status"].([]any)
				if !ok {
					report("status is "+typeName(m["status"]), m.String())
					return
				}
				for _, x := range list {
					if _, ok := x.(string); !ok {
						report("status contains "+typeName(x), m.String())
						return
					}
				}
			}),
		},
		{
			ID:       "wire.field-types",
			Title:    "Standard fields are strings",
			Severity: check.Fail,
			Why:      "Clients insert id, session, ns, value, out and err straight into buffers and prompts; other types break them.",
			Refs:     []check.Ref{specEval, ciderPayloadCond},
			Inspect: eachMessage(func(m nrepl.Message, report check.Reporter) {
				wrongTypes[string](m, stringFields, report)
			}),
		},
		{
			ID:       "wire.one-payload",
			Title:    "A message carries at most one of value, out and err",
			Severity: check.Fail,
			Why:      "CIDER treats value, out and err as mutually exclusive within a message and silently drops all but one.",
			Refs:     []check.Ref{ciderPayloadCond},
			Inspect: eachMessage(func(m nrepl.Message, report check.Reporter) {
				var present []string
				for _, k := range []string{"value", "out", "err"} {
					if m.Has(k) {
						present = append(present, k)
					}
				}
				if len(present) > 1 {
					report(fmt.Sprintf("one message carries %v", present), m.String())
				}
			}),
		},
		{
			ID:       "wire.id",
			Title:    "Replies carry the id of their request",
			Severity: check.Warn,
			Why:      "Clients match replies to requests by id. The spec lets output go without one, but that's the reply most likely to get lost.",
			Refs:     []check.Ref{specProtocol},
			Inspect: eachReply(func(r reply, report check.Reporter) {
				if !r.Msg.Has("id") {
					report("a reply without an id", r.Msg.String())
				} else if r.Req == nil {
					report("a reply with an id no request on the connection used", r.Msg.String())
				}
			}),
		},
		{
			ID:       "wire.session-echo",
			Title:    "Replies to a request with a session carry that session",
			Severity: check.Fail,
			Why:      "Calva only hands a reply to a session when it carries that session's id; everything else is dropped.",
			Refs:     []check.Ref{calvaRouting},
			Inspect: eachReply(func(r reply, report check.Reporter) {
				if !r.Req.Has("session") || r.Msg.Str("session") == r.Req.Str("session") {
					return
				}
				example := fmt.Sprintf("request in session %q got %s", r.Req.Str("session"), r.Msg)
				if r.Msg.Has("session") {
					report("a reply to "+r.Req.Str("op")+" carried a different session", example)
				} else {
					report("a reply to "+r.Req.Str("op")+" carried no session", example)
				}
			}),
		},
		{
			ID:       "wire.one-done",
			Title:    "Each request gets exactly one done",
			Severity: check.Warn,
			Why:      "Clients stop listening at the first done and ignore the rest, but a second one is a sign of a confused server.",
			Refs:     []check.Ref{issue("nrepl/nrepl", 215), specProtocol},
			Inspect: eachReply(func(r reply, report check.Reporter) {
				if r.AfterDone && r.Msg.HasStatus("done") {
					report("a request got a second done", r.Msg.String())
				}
			}),
		},
		{
			ID:       "wire.after-done",
			Title:    "Only output follows done",
			Severity: check.Warn,
			Why:      "Clients drop a request's handler at done. The spec allows late output, but a late value or status is lost.",
			Refs:     []check.Ref{ciderDone, specProtocol},
			Inspect: eachReply(func(r reply, report check.Reporter) {
				// A repeated done is wire.one-done's business.
				if !r.AfterDone || r.Msg.HasStatus("done") {
					return
				}
				if k := firstKeyNotIn(r.Msg, afterDoneKeys); k != "" {
					report(k+" arrived after done", r.Msg.String())
				}
			}),
		},
		{
			ID:       "wire.error-terminal",
			Title:    "Nothing but done follows an error status",
			Severity: check.Fail,
			Why:      "REPLy (behind lein repl) and rebel-readline stop reading a request at the first error or eval-error status, so later output or values never show up.",
			Refs:     []check.Ref{replyTerminal, rebelTerminal},
			Inspect: eachReply(func(r reply, report check.Reporter) {
				if !r.AfterError {
					return
				}
				// nREPL goes on with the next form after one throws, and
				// REPLy sends one form at a time. Only Clojure code can be
				// told apart into forms, though.
				if k := firstKeyNotIn(r.Msg, afterErrorKeys); k != "" && !severalForms(r.Req.Str("code")) {
					report(k+" arrived after an error status", r.Msg.String())
				}
			}),
		},
		{
			ID:       "wire.need-input-alone",
			Title:    "need-input is sent on its own",
			Severity: check.Fail,
			Why:      "Calva compares status to \"need-input\" directly, which only matches a one-element list; with any other flag it never prompts.",
			Refs:     []check.Ref{calvaNeedInput},
			Inspect: eachMessage(func(m nrepl.Message, report check.Reporter) {
				if m.HasStatus("need-input") && len(m.Status()) != 1 {
					report("need-input arrived with other flags", m.String())
				}
			}),
		},
	}
}

// eachFrame calls f for every frame that went in the given direction.
func eachFrame(dir nrepl.Direction, f func(nrepl.Event, check.Reporter)) func([]nrepl.Event, check.Reporter) {
	return func(events []nrepl.Event, report check.Reporter) {
		for _, ev := range events {
			if ev.Dir == dir && !ev.Closed {
				f(ev, report)
			}
		}
	}
}

func eachReceived(f func(nrepl.Event, check.Reporter)) func([]nrepl.Event, check.Reporter) {
	return eachFrame(nrepl.Received, f)
}

func badFrame(ev nrepl.Event, report check.Reporter) {
	if ev.Err == nil {
		return
	}
	problem := ev.Err.Error()
	var se *bencode.SyntaxError
	if errors.As(ev.Err, &se) {
		problem = se.Msg
	}
	report(problem, excerpt(ev.Raw))
}

func notDict(ev nrepl.Event, report check.Reporter) {
	if ev.Err == nil && ev.Msg == nil {
		report("a top-level "+typeName(ev.Data)+" instead of a dict", excerpt(ev.Raw))
	}
}

func nonCanonical(ev nrepl.Event, report check.Reporter) {
	for _, v := range ev.Violations {
		report(v.Msg, "")
	}
}

// wrongTypes reports each of fields that m has, but not as a T.
func wrongTypes[T any](m nrepl.Message, fields []string, report check.Reporter) {
	for _, k := range fields {
		if v, ok := m[k]; ok {
			if _, ok := v.(T); !ok {
				report(k+" is "+typeName(v), m.String())
			}
		}
	}
}

// eachMessageIn calls f for every dict that went in the given direction.
func eachMessageIn(dir nrepl.Direction, f func(nrepl.Message, check.Reporter)) func([]nrepl.Event, check.Reporter) {
	return eachFrame(dir, func(ev nrepl.Event, report check.Reporter) {
		if ev.Msg != nil {
			f(ev.Msg, report)
		}
	})
}

func eachMessage(f func(nrepl.Message, check.Reporter)) func([]nrepl.Event, check.Reporter) {
	return eachMessageIn(nrepl.Received, f)
}

// reply is a received message along with what came before it on the
// connection.
type reply struct {
	Msg nrepl.Message
	// Req is the request the reply's id refers to, or nil.
	Req nrepl.Message
	// AfterDone and AfterError say whether an earlier message for the same
	// request carried done, or an error status. Replies without an id
	// can't be tied to a request, so both stay false for them.
	AfterDone, AfterError bool
}

// idKey tells ids of different types apart, since servers echo whatever
// they got.
func idKey(m nrepl.Message) (string, bool) {
	v, ok := m["id"]
	return valueKey(v), ok
}

func valueKey(v any) string {
	return fmt.Sprintf("%T %v", v, v)
}

// eachReply walks a connection once, annotating every received message.
func eachReply(f func(reply, check.Reporter)) func([]nrepl.Event, check.Reporter) {
	return func(events []nrepl.Event, report check.Reporter) {
		sent := map[string]nrepl.Message{}
		done, errored := map[string]bool{}, map[string]bool{}
		for _, ev := range events {
			if ev.Msg == nil {
				continue
			}
			id, hasID := idKey(ev.Msg)
			if ev.Dir == nrepl.Sent {
				if hasID {
					sent[id] = ev.Msg
				}
				continue
			}
			r := reply{Msg: ev.Msg}
			if hasID {
				r.Req = sent[id]
				r.AfterDone, r.AfterError = done[id], errored[id]
			}
			f(r, report)
			if hasID {
				done[id] = done[id] || ev.Msg.HasStatus("done")
				errored[id] = errored[id] || ev.Msg.HasStatus("error") || ev.Msg.HasStatus("eval-error")
			}
		}
	}
}

// firstKeyNotIn returns the first key of m (in sorted order) that allowed
// doesn't list, or "".
func firstKeyNotIn(m nrepl.Message, allowed map[string]bool) string {
	for _, k := range m.Keys() {
		if !allowed[k] {
			return k
		}
	}
	return ""
}

func walkStrings(path string, v any, f func(path, s string)) {
	switch x := v.(type) {
	case string:
		f(path, x)
	case []any:
		for i, item := range x {
			walkStrings(path+"["+strconv.Itoa(i)+"]", item, f)
		}
	case map[string]any:
		for k, item := range x {
			p := k
			if path != "" {
				p = path + "." + k
			}
			f(p+" (key)", k)
			walkStrings(p, item, f)
		}
	}
}

func typeName(v any) string {
	switch v.(type) {
	case string:
		return "a string"
	case int64:
		return "an integer"
	case []any:
		return "a list"
	case map[string]any:
		return "a dict"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func excerpt(raw []byte) string {
	const max = 120
	if len(raw) > max {
		return strconv.Quote(string(raw[:max])) + "..."
	}
	return strconv.Quote(string(raw))
}

// severalForms says whether code is more than one Clojure form, each in
// brackets, like the code clients send when they connect.
func severalForms(code string) bool {
	fs := forms(code)
	return len(fs) > 1 && !slices.ContainsFunc(fs, func(f string) bool { return !strings.ContainsAny(f[:1], "([{") })
}

// forms splits Clojure code into its top-level forms (e.g.
// `(System/getProperty "user.dir")`), leaving out comments.
func forms(code string) []string {
	var forms []string
	var form strings.Builder
	end := func() {
		if form.Len() > 0 {
			forms = append(forms, form.String())
			form.Reset()
		}
	}
	depth, inString, escaped, inComment := 0, false, false, false
	for _, r := range code {
		switch {
		case inComment:
			inComment = r != '\n'
			continue
		case escaped:
			escaped = false
		case inString:
			escaped = r == '\\'
			inString = r != '"'
		case r == '\\':
			// A character, e.g. \(
			escaped = true
		case r == '"':
			inString = true
		case r == ';':
			inComment = true
			if depth == 0 {
				end()
			}
			continue
		case strings.ContainsRune("([{", r):
			depth++
		case strings.ContainsRune(")]}", r):
			depth--
			if depth == 0 {
				form.WriteRune(r)
				end()
				continue
			}
		case (unicode.IsSpace(r) || r == ',') && depth == 0:
			end()
			continue
		}
		form.WriteRune(r)
	}
	end()
	return forms
}
