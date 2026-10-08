package checks

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nrepl/proof/internal/check"
	"github.com/nrepl/proof/nrepl"
)

// RecordedProfile turns the requests in traffic into a client profile,
// with a check for each connection, to be turned into a real one: it says
// what the client sent, but not yet what the client needs from the
// replies.
func RecordedProfile(traffic []check.Traffic) string {
	name := ""
	var checks strings.Builder
	sessions := &sessionNames{byID: map[string]string{}}
	for _, tr := range traffic {
		steps := recordedSteps(tr.Events, sessions)
		if len(steps) == 0 {
			continue
		}
		id := strings.ReplaceAll(tr.Label, " ", "-")
		fmt.Fprintf(&checks, "\n[[checks]]\nid = %s\ntitle = %s\nwhy = \"\"\n", tomlString(id), tomlString(tr.Label))
		for _, s := range steps {
			if n, ok := s.Send["client-name"].(string); ok && name == "" {
				name = n
			}
			checks.WriteString(recordedStep(s))
		}
	}
	return recordedHeader + "name = " + tomlString(name) + "\ncode = \"\"\n" + checks.String()
}

// sessionNames names the sessions clients cloned, across all connections,
// as sessions outlive them.
type sessionNames struct {
	byID map[string]string
	n    int
}

// add names a session the server handed out. One it handed out before
// gets a new name, as it's a new session for proof.
func (s *sessionNames) add(id string) string {
	s.n++
	s.byID[id] = "s" + strconv.Itoa(s.n)
	return s.byID[id]
}

// recordedSteps turns each request of a connection into a step, without
// its id, as proof gives requests ids of its own. The sessions the client
// cloned get names, so that the steps can use the sessions proof clones.
func recordedSteps(events []nrepl.Event, sessions *sessionNames) []clientStep {
	var steps []clientStep
	clones := map[string]int{} // the ids of clone requests, and their steps
	for _, ev := range events {
		m := ev.Msg
		if m == nil {
			continue
		}
		if ev.Dir == nrepl.Received {
			id, _ := idKey(m)
			i, ok := clones[id]
			if s := m.Str("new-session"); ok && s != "" && steps[i].NewSession == "" {
				steps[i].NewSession = sessions.add(s)
			}
			continue
		}
		send := request{}
		for k, v := range m {
			if k != "id" {
				send[k] = v
			}
		}
		if name := sessions.byID[m.Str("session")]; name != "" {
			send["session"] = "$" + name
		}
		if m.Str("op") == "clone" {
			id, _ := idKey(m)
			clones[id] = len(steps)
		}
		steps = append(steps, clientStep{Send: send})
	}
	return steps
}

const recordedHeader = `# The requests clients sent through proof, with a check for each
# connection. To make a client profile out of it, fill in the blanks, use
# snippets for the user's code and say what the client needs from each
# reply (see "Adding a Client Profile" in doc/hacking.md).
`

// recordedStep writes a step the way the profiles in clients/ write them.
func recordedStep(s clientStep) string {
	newSession := ""
	if s.NewSession != "" {
		newSession = "new-session = " + tomlString(s.NewSession) + "\n"
	}
	// The op goes first, as that's what tells the requests apart.
	keys := nrepl.Message(s.Send).Keys()
	if i := slices.Index(keys, "op"); i > 0 {
		keys = slices.Insert(slices.Delete(keys, i, i+1), 0, "op")
	}
	if send := "send = " + tomlTable(s.Send, keys); len(send) <= 100 {
		return "\n[[checks.steps]]\n" + send + "\n" + newSession
	}
	var b strings.Builder
	b.WriteString("\n[[checks.steps]]\n")
	if newSession != "" {
		b.WriteString(newSession + "\n")
	}
	b.WriteString("[checks.steps.send]\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %s\n", tomlKey(k), tomlValue(s.Send[k]))
	}
	return b.String()
}

// tomlValue writes a value decoded from bencode.
func tomlValue(v any) string {
	switch v := v.(type) {
	case string:
		return tomlString(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case []any:
		items := make([]string, len(v))
		for i, item := range v {
			items[i] = tomlValue(item)
		}
		return "[" + strings.Join(items, ", ") + "]"
	case map[string]any:
		return tomlTable(v, nrepl.Message(v).Keys())
	}
	panic(fmt.Sprintf("can't write %T as TOML", v))
}

// tomlTable writes the keys of m, in this order, as an inline table.
func tomlTable(m map[string]any, keys []string) string {
	if len(keys) == 0 {
		return "{}"
	}
	fields := make([]string, len(keys))
	for i, k := range keys {
		fields[i] = tomlKey(k) + " = " + tomlValue(m[k])
	}
	return "{ " + strings.Join(fields, ", ") + " }"
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func tomlKey(k string) string {
	if bareKey.MatchString(k) {
		return k
	}
	return tomlString(k)
}

// tomlString writes s as a basic string, on one line. Bytes that aren't
// UTF-8, which TOML can't have, become U+FFFD.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range strings.ToValidUTF8(s, string(utf8.RuneError)) {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
