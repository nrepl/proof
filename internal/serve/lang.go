package serve

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// The code proof serve evaluates is a sliver of Clojure: enough for the
// snippets in profiles/clojure.toml, and for client test suites to print,
// fail, read input and take their time. These are the forms it reads.
type (
	symbol  string
	keyword string
	list    []any
	vector  []any
	// mapForm holds keys and values, alternating, in the order they were
	// written.
	mapForm []any
)

// Everything else evaluation can produce.
type (
	ratio     struct{ num, den int64 }
	namespace string
	variable  struct{ ns, name string }
	function  struct{ name string }
	future    struct{}
	// exception is what throw throws. data is ex-info's map.
	exception struct {
		class, msg string
		data       any
	}
)

// reader reads forms from code one at a time, the way nREPL does, so the
// forms before a broken one still get evaluated.
type reader struct {
	src string
	pos int
}

// next returns the next form, or io.EOF once there are none left.
func (r *reader) next() (any, error) {
	r.skip()
	if r.pos >= len(r.src) {
		return nil, io.EOF
	}
	return r.form()
}

func (r *reader) skip() {
	for r.pos < len(r.src) {
		switch r.src[r.pos] {
		case ' ', '\t', '\n', '\r', ',':
			r.pos++
		case ';':
			for r.pos < len(r.src) && r.src[r.pos] != '\n' {
				r.pos++
			}
		default:
			return
		}
	}
}

func (r *reader) form() (any, error) {
	start := r.pos
	switch c := r.src[r.pos]; c {
	case '(':
		items, err := r.until(')', start)
		return list(items), err
	case '[':
		items, err := r.until(']', start)
		return vector(items), err
	case '{':
		items, err := r.until('}', start)
		if err == nil && len(items)%2 != 0 {
			return nil, r.errorf("Map literal must contain an even number of forms")
		}
		return mapForm(items), err
	case ')', ']', '}':
		r.pos++
		return nil, r.errorf("Unmatched delimiter: %c", c)
	case '"':
		return r.str(start)
	case '\'':
		return r.wrapped("quote", 1, start)
	case '@':
		return r.wrapped("deref", 1, start)
	case '#':
		if strings.HasPrefix(r.src[r.pos:], "#'") {
			return r.wrapped("var", 2, start)
		}
		r.pos++
		return nil, r.errorf("proof serve can't read %c", c)
	case '`', '~', '^', '\\':
		r.pos++
		return nil, r.errorf("proof serve can't read %c", c)
	}
	return r.token()
}

// wrapped reads what a reader macro like ' applies to, after its n
// characters, and wraps it in a call to op.
func (r *reader) wrapped(op string, n, start int) (any, error) {
	r.pos += n
	r.skip()
	if r.pos >= len(r.src) {
		return nil, r.eof(start)
	}
	f, err := r.form()
	return list{symbol(op), f}, err
}

// until reads forms up to the closing delimiter.
func (r *reader) until(closing byte, start int) ([]any, error) {
	r.pos++
	var items []any
	for {
		r.skip()
		if r.pos >= len(r.src) {
			return nil, r.eof(start)
		}
		if r.src[r.pos] == closing {
			r.pos++
			return items, nil
		}
		f, err := r.form()
		if err != nil {
			return nil, err
		}
		items = append(items, f)
	}
}

func (r *reader) str(start int) (any, error) {
	r.pos++
	var b strings.Builder
	for r.pos < len(r.src) {
		c := r.src[r.pos]
		r.pos++
		switch {
		case c == '"':
			return b.String(), nil
		case c != '\\':
			b.WriteByte(c)
		case r.pos >= len(r.src):
			return nil, r.eof(start)
		default:
			e := r.src[r.pos]
			r.pos++
			switch e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '"', '\\':
				b.WriteByte(e)
			default:
				return nil, r.errorf("Unsupported escape character: \\%c", e)
			}
		}
	}
	return nil, r.eofError("EOF while reading string")
}

func (r *reader) token() (any, error) {
	start := r.pos
	for r.pos < len(r.src) && !strings.ContainsRune(" \t\n\r,;()[]{}\"", rune(r.src[r.pos])) {
		r.pos++
	}
	tok := r.src[start:r.pos]
	switch tok {
	case "nil":
		return nil, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	if k, ok := strings.CutPrefix(tok, ":"); ok {
		if k == "" {
			return nil, r.errorf("Invalid token: :")
		}
		return keyword(k), nil
	}
	if digits := strings.TrimLeft(tok, "+-"); digits != "" && digits[0] >= '0' && digits[0] <= '9' {
		n, err := strconv.ParseInt(tok, 10, 64)
		if err != nil {
			return nil, r.errorf("Invalid number: %s", tok)
		}
		return n, nil
	}
	return symbol(tok), nil
}

// eof reports code that ends in the middle of a form.
func (r *reader) eof(start int) error {
	return r.eofError(fmt.Sprintf("EOF while reading, starting at line %d", 1+strings.Count(r.src[:start], "\n")))
}

// eofError reports the end of the code like nREPL does, at the line after
// it.
func (r *reader) eofError(msg string) error {
	return readError(2+strings.Count(r.src, "\n"), 1, msg)
}

func (r *reader) errorf(format string, args ...any) error {
	before := r.src[:r.pos]
	return readError(1+strings.Count(before, "\n"), 1+len(before)-(strings.LastIndexByte(before, '\n')+1), fmt.Sprintf(format, args...))
}

func readError(line, col int, msg string) error {
	return &thrown{ex: &exception{class: "clojure.lang.ExceptionInfo", msg: msg}, phase: reading, line: line, col: col}
}

// pr prints v the way pr-str does.
func pr(v any) string { return printed(v, true) }

// printed prints v, with strings as they are unless readably, the way
// print does then.
func printed(v any, readably bool) string {
	var b strings.Builder
	write(&b, v, readably)
	return b.String()
}

func write(b *strings.Builder, v any, readably bool) {
	items := func(open, close string, xs []any) {
		b.WriteString(open)
		for i, x := range xs {
			if i > 0 {
				b.WriteString(" ")
			}
			write(b, x, readably)
		}
		b.WriteString(close)
	}
	switch x := v.(type) {
	case nil:
		b.WriteString("nil")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case ratio:
		fmt.Fprintf(b, "%d/%d", x.num, x.den)
	case string:
		if readably {
			b.WriteString(quote(x))
		} else {
			b.WriteString(x)
		}
	case keyword:
		b.WriteString(":" + string(x))
	case symbol:
		b.WriteString(string(x))
	case list:
		items("(", ")", x)
	case vector:
		items("[", "]", x)
	case mapForm:
		// Clojure separates the entries with commas: {:a 1, :b 2}.
		b.WriteString("{")
		for i := 0; i < len(x); i += 2 {
			if i > 0 {
				b.WriteString(", ")
			}
			write(b, x[i], readably)
			b.WriteString(" ")
			write(b, x[i+1], readably)
		}
		b.WriteString("}")
	case namespace:
		fmt.Fprintf(b, "#object[clojure.lang.Namespace 0x1 %q]", string(x))
	case *variable:
		fmt.Fprintf(b, "#'%s/%s", x.ns, x.name)
	case *function:
		fmt.Fprintf(b, "#object[clojure.core$%s 0x1 \"clojure.core$%s@1\"]", x.name, x.name)
	case *future:
		b.WriteString("#object[clojure.core$future_call$reify__1 0x1 {:status :pending, :val nil}]")
	case *exception:
		fmt.Fprintf(b, "#error {\n :cause %s\n :data ", quote(x.msg))
		write(b, x.data, true)
		fmt.Fprintf(b, "\n :via\n [{:type %s\n   :message %s}]}", x.class, quote(x.msg))
	default:
		panic(fmt.Sprintf("can't print %T", v))
	}
}

var escapes = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`, "\r", `\r`)

func quote(s string) string { return `"` + escapes.Replace(s) + `"` }
