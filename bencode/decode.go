// Package bencode is a strict bencode codec for testing nREPL servers.
//
// Most bencode decoders are lenient, which is exactly wrong for a test
// harness: they quietly accept the malformed output we want to report.
// This decoder separates hard errors (the input can't be decoded, so a
// client would choke on it) from violations (the input decodes fine but
// isn't canonical bencode, e.g. unsorted dict keys).
//
// Decoded values are string, int64, []any and map[string]any.
package bencode

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// DefaultMaxStringLen caps the length of a single string, so a corrupt
// length prefix can't make the decoder allocate gigabytes.
const DefaultMaxStringLen = 64 << 20

const maxDepth = 256

// SyntaxError reports input that can't be decoded at all.
type SyntaxError struct {
	Offset int64
	Msg    string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("bencode: %s at offset %d", e.Msg, e.Offset)
}

// Violation reports input that decodes but isn't canonical bencode.
type Violation struct {
	Offset int64
	Msg    string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s at offset %d", v.Msg, v.Offset)
}

// Decoder reads bencode values from a stream, one at a time. It never
// reads past the end of the current value, so it is safe to use on a live
// connection.
type Decoder struct {
	r            *bufio.Reader
	off          int64
	raw          []byte
	violations   []Violation
	MaxStringLen int
}

// NewDecoder returns a decoder reading from r.
func NewDecoder(r io.Reader) *Decoder {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	return &Decoder{r: br, MaxStringLen: DefaultMaxStringLen}
}

// Value is one decoded top-level value.
type Value struct {
	Data any
	// Raw is every byte the decoder consumed, which after an error is the
	// value up to the point where it went wrong.
	Raw        []byte
	Violations []Violation
}

// Decode reads the next value. It returns io.EOF only when the stream ends
// cleanly between values; a stream that ends mid-value is an
// io.ErrUnexpectedEOF. After a SyntaxError the stream position is
// unspecified and the decoder shouldn't be used again.
func (d *Decoder) Decode() (Value, error) {
	d.raw = nil
	d.violations = nil
	if _, err := d.peek(); err != nil {
		return Value{}, err
	}
	v, err := d.value(0)
	val := Value{Data: v, Raw: d.raw, Violations: d.violations}
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	return val, err
}

func (d *Decoder) peek() (byte, error) {
	bs, err := d.r.Peek(1)
	if err != nil {
		return 0, err
	}
	return bs[0], nil
}

func (d *Decoder) next() (byte, error) {
	b, err := d.r.ReadByte()
	if err != nil {
		return 0, err
	}
	d.off++
	d.raw = append(d.raw, b)
	return b, nil
}

func (d *Decoder) syntax(off int64, format string, args ...any) error {
	return &SyntaxError{Offset: off, Msg: fmt.Sprintf(format, args...)}
}

func (d *Decoder) violate(off int64, format string, args ...any) {
	d.violations = append(d.violations, Violation{Offset: off, Msg: fmt.Sprintf(format, args...)})
}

func (d *Decoder) value(depth int) (any, error) {
	if depth > maxDepth {
		return nil, d.syntax(d.off, "nesting deeper than %d levels", maxDepth)
	}
	start := d.off
	b, err := d.peek()
	if err != nil {
		return nil, err
	}
	switch {
	case b == 'i':
		return d.integer()
	case b >= '0' && b <= '9':
		return d.str()
	case b == 'l':
		d.next()
		list := []any{}
		for {
			b, err := d.peek()
			if err != nil {
				return nil, err
			}
			if b == 'e' {
				d.next()
				return list, nil
			}
			v, err := d.value(depth + 1)
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		}
	case b == 'd':
		d.next()
		dict := map[string]any{}
		var prev string
		for {
			b, err := d.peek()
			if err != nil {
				return nil, err
			}
			if b == 'e' {
				d.next()
				return dict, nil
			}
			keyOff := d.off
			if b < '0' || b > '9' {
				return nil, d.syntax(keyOff, "dict key is not a string (starts with %q)", b)
			}
			key, err := d.str()
			if err != nil {
				return nil, err
			}
			if _, dup := dict[key]; dup {
				d.violate(keyOff, "duplicate dict key %q", key)
			} else if key < prev {
				d.violate(keyOff, "dict key %q is out of order (after %q)", key, prev)
			}
			prev = key
			v, err := d.value(depth + 1)
			if err != nil {
				return nil, err
			}
			dict[key] = v
		}
	default:
		return nil, d.syntax(start, "unexpected byte %q", b)
	}
}

// digits reads a run of ASCII digits, returning them unparsed.
func (d *Decoder) digits() ([]byte, error) {
	var ds []byte
	for {
		b, err := d.peek()
		if err != nil {
			return nil, err
		}
		if b < '0' || b > '9' {
			return ds, nil
		}
		d.next()
		ds = append(ds, b)
	}
}

func (d *Decoder) integer() (any, error) {
	start := d.off
	d.next() // 'i'
	neg := false
	if b, err := d.peek(); err != nil {
		return nil, err
	} else if b == '-' {
		neg = true
		d.next()
	}
	ds, err := d.digits()
	if err != nil {
		return nil, err
	}
	b, err := d.next()
	if err != nil {
		return nil, err
	}
	if b != 'e' {
		return nil, d.syntax(d.off-1, "unexpected byte %q in integer", b)
	}
	if len(ds) == 0 {
		return nil, d.syntax(start, "integer with no digits")
	}
	if len(ds) > 1 && ds[0] == '0' {
		d.violate(start, "integer with leading zero")
	}
	if neg {
		ds = append([]byte{'-'}, ds...)
	}
	// The digits are already validated, so a range error is the only
	// thing ParseInt can report.
	n, err := strconv.ParseInt(string(ds), 10, 64)
	if err != nil {
		return nil, d.syntax(start, "integer overflows 64 bits")
	}
	if neg && n == 0 {
		d.violate(start, "negative zero")
	}
	return n, nil
}

func (d *Decoder) str() (string, error) {
	start := d.off
	ds, err := d.digits()
	if err != nil {
		return "", err
	}
	b, err := d.next()
	if err != nil {
		return "", err
	}
	if b != ':' {
		return "", d.syntax(d.off-1, "unexpected byte %q in string length", b)
	}
	if len(ds) > 1 && ds[0] == '0' {
		d.violate(start, "string length with leading zero")
	}
	n := 0
	for _, c := range ds {
		n = n*10 + int(c-'0')
		if n > d.MaxStringLen {
			return "", d.syntax(start, "string longer than %d bytes", d.MaxStringLen)
		}
	}
	buf := make([]byte, n)
	read, err := io.ReadFull(d.r, buf)
	d.off += int64(read)
	d.raw = append(d.raw, buf[:read]...)
	if err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return "", err
	}
	return string(buf), nil
}
