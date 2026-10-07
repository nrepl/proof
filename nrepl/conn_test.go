package nrepl

import (
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"

	"github.com/nrepl/proof/bencode"
)

func TestDecodeEvent(t *testing.T) {
	reset := &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
	closed := &net.OpError{Op: "read", Net: "tcp", Err: net.ErrClosed}
	cases := []struct {
		name    string
		raw     string
		err     error
		frame   bool
		broken  bool
		message bool
	}{
		{"a dict", "d2:id1:1e", nil, true, false, true},
		{"a list", "le", nil, true, false, false},
		{"a byte that can't start a value", "", &bencode.SyntaxError{Msg: "unexpected byte 'x'"}, true, true, false},
		{"a frame cut off by the end", "d4:code", io.ErrUnexpectedEOF, true, true, false},
		{"a frame cut off by a reset", "d4:code", reset, false, false, false},
		{"the end between frames", "", io.EOF, false, false, false},
		{"a reset between frames", "", reset, false, false, false},
		{"proof closing the connection", "d4:code", closed, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var data any
			if c.err == nil {
				v, err := bencode.NewDecoder(strings.NewReader(c.raw)).Decode()
				if err != nil {
					t.Fatal(err)
				}
				data = v.Data
			}
			ev, frame := DecodeEvent(Sent, bencode.Value{Data: data, Raw: []byte(c.raw)}, c.err)
			if frame != c.frame {
				t.Fatalf("frame: got %v, want %v", frame, c.frame)
			}
			if broken := ev.Err != nil; broken != c.broken {
				t.Errorf("broken: got %v, want %v", broken, c.broken)
			}
			if c.broken && !errors.Is(ev.Err, c.err) {
				t.Errorf("err: got %v, want %v", ev.Err, c.err)
			}
			if message := ev.Msg != nil; message != c.message {
				t.Errorf("message: got %v, want %v", message, c.message)
			}
		})
	}
}
