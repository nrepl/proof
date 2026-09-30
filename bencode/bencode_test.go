package bencode

import (
	"bytes"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func decodeOne(t *testing.T, in string) (Value, error) {
	t.Helper()
	return NewDecoder(strings.NewReader(in)).Decode()
}

func TestDecodeValid(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"i42e", int64(42)},
		{"i-7e", int64(-7)},
		{"i0e", int64(0)},
		{"0:", ""},
		{"4:spam", "spam"},
		{"le", []any{}},
		{"l4:spami1ee", []any{"spam", int64(1)}},
		{"de", map[string]any{}},
		{"d2:id1:12:opl4:doneee", map[string]any{"id": "1", "op": []any{"done"}}},
		{"d1:ad1:bi1eee", map[string]any{"a": map[string]any{"b": int64(1)}}},
		{"6:h\xc3\xa9llo", "héllo"},
		{"i9223372036854775807e", int64(math.MaxInt64)},
		{"i-9223372036854775808e", int64(math.MinInt64)},
	}
	for _, c := range cases {
		v, err := decodeOne(t, c.in)
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(v.Data, c.want) {
			t.Errorf("%q: got %#v, want %#v", c.in, v.Data, c.want)
		}
		if len(v.Violations) != 0 {
			t.Errorf("%q: unexpected violations %v", c.in, v.Violations)
		}
		if string(v.Raw) != c.in {
			t.Errorf("%q: raw is %q", c.in, v.Raw)
		}
	}
}

func TestDecodeViolations(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"d1:bi1e1:ai2ee", "out of order"},
		{"d1:ai1e1:ai2ee", "duplicate dict key"},
		{"i03e", "leading zero"},
		{"i-0e", "negative zero"},
		{"03:abc", "leading zero"},
	}
	for _, c := range cases {
		v, err := decodeOne(t, c.in)
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.in, err)
			continue
		}
		if len(v.Violations) != 1 || !strings.Contains(v.Violations[0].Msg, c.want) {
			t.Errorf("%q: got violations %v, want one containing %q", c.in, v.Violations, c.want)
		}
	}
}

func TestDecodeSyntaxErrors(t *testing.T) {
	cases := []string{
		"ie",
		"i-e",
		"i+1e",
		"i1.5e",
		"i99999999999999999999e",
		"i9223372036854775808e",
		"i-9223372036854775809e",
		"x",
		"di1ei2ee",
		"4spam",
		"d4:spam",
	}
	for _, in := range cases {
		_, err := decodeOne(t, in)
		var se *SyntaxError
		if err == nil || (!errors.As(err, &se) && !errors.Is(err, io.ErrUnexpectedEOF)) {
			t.Errorf("%q: got %v, want a syntax error or unexpected EOF", in, err)
		}
	}
}

func TestDecodeTruncated(t *testing.T) {
	for _, in := range []string{"i42", "4:sp", "l4:spam", "d2:id"} {
		if _, err := decodeOne(t, in); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("%q: got %v, want io.ErrUnexpectedEOF", in, err)
		}
	}
}

func TestDecodeStringLimit(t *testing.T) {
	d := NewDecoder(strings.NewReader("999999999:x"))
	d.MaxStringLen = 1024
	var se *SyntaxError
	if _, err := d.Decode(); !errors.As(err, &se) {
		t.Fatalf("got %v, want a syntax error", err)
	}
}

func TestDecodeStream(t *testing.T) {
	d := NewDecoder(strings.NewReader("i1e4:spamle"))
	for _, want := range []any{int64(1), "spam", []any{}} {
		v, err := d.Decode()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(v.Data, want) {
			t.Fatalf("got %#v, want %#v", v.Data, want)
		}
	}
	if _, err := d.Decode(); !errors.Is(err, io.EOF) {
		t.Fatalf("got %v at end of stream, want io.EOF", err)
	}
}

// A decoder on a live connection must return a complete message without
// waiting for more bytes, or the harness would hang on the last reply.
func TestDecodeDoesNotReadAhead(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	go w.Write([]byte("d2:id1:1e"))
	done := make(chan error, 1)
	go func() {
		_, err := NewDecoder(r).Decode()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Decode blocked waiting for bytes past the end of the message")
	}
}

func TestMarshal(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"spam", "4:spam"},
		{42, "i42e"},
		{int64(-3), "i-3e"},
		{[]string{"a", "b"}, "l1:a1:be"},
		{map[string]any{"op": "eval", "code": "(+ 1 2)", "id": "1"}, "d4:code7:(+ 1 2)2:id1:12:op4:evale"},
		{map[string]any{"a": []any{1, map[string]string{"z": "y"}}}, "d1:ali1ed1:z1:yeee"},
	}
	for _, c := range cases {
		got, err := Marshal(c.in)
		if err != nil {
			t.Errorf("%#v: %v", c.in, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("%#v: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMarshalRejectsWhatBencodeCantRepresent(t *testing.T) {
	for _, in := range []any{nil, true, 1.5, map[string]any{"session": nil}} {
		if _, err := Marshal(in); err == nil {
			t.Errorf("%#v: expected an error", in)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	in := map[string]any{
		"id":     "7",
		"status": []any{"done", "eval-error"},
		"nested": map[string]any{"n": int64(-12), "s": "hé"},
	}
	b, err := Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewDecoder(bytes.NewReader(b)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v.Data, in) {
		t.Fatalf("got %#v, want %#v", v.Data, in)
	}
}
