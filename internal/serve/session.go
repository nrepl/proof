package serve

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"sync"

	"github.com/nrepl/proof/nrepl"
)

// job is an eval request, waiting for its turn or running.
type job struct {
	c   *conn
	req nrepl.Message
	// interrupts works like a thread's interrupt flag: the next sleep or
	// read of input gets an InterruptedException.
	interrupts chan struct{}
	// interrupted means interrupt has sent the eval's done already.
	interrupted bool
}

type session struct {
	id string
	// conn is the connection that created the session.
	conn *conn
	b    *bindings
	// ephemeral sessions only last for one request.
	ephemeral bool

	mu      sync.Mutex
	jobs    []*job
	running *job
	closed  bool
	// input is what stdin sent that hasn't been read yet, and eof means
	// an empty stdin came after it.
	input string
	eof   bool
	// wake says there's a job or the session was closed, and inputs that
	// input arrived.
	wake, inputs chan struct{}
}

func newSession(c *conn, b *bindings, ephemeral bool) *session {
	return &session{
		id: newID(), conn: c, b: b, ephemeral: ephemeral,
		wake: make(chan struct{}, 1), inputs: make(chan struct{}, 1),
	}
}

// newID makes a session id like nREPL's, a random UUID.
func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (sess *session) queue(j *job) {
	sess.mu.Lock()
	sess.jobs = append(sess.jobs, j)
	sess.mu.Unlock()
	signal(sess.wake)
}

// next waits for the next job, reporting false once the session is closed
// or ctx is done.
func (sess *session) next(ctx context.Context) (*job, bool) {
	for {
		sess.mu.Lock()
		if sess.closed {
			sess.mu.Unlock()
			return nil, false
		}
		if len(sess.jobs) > 0 {
			j := sess.jobs[0]
			sess.jobs = sess.jobs[1:]
			sess.running = j
			sess.mu.Unlock()
			return j, true
		}
		sess.mu.Unlock()
		select {
		case <-sess.wake:
		case <-ctx.Done():
			return nil, false
		}
	}
}

// finish marks j as no longer running, and reports whether it still
// needs its done, which an interrupt sends itself.
func (sess *session) finish(j *job) bool {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.running == j {
		sess.running = nil
	}
	return !j.interrupted
}

// interrupt marks the running job as interrupted and returns it, unless
// there's none or it isn't the one with the given id ("" for any).
func (sess *session) interrupt(id string) (j *job, mismatch bool) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	j = sess.running
	switch {
	case j == nil:
		return nil, false
	case id != "" && id != fmt.Sprint(j.req["id"]):
		return nil, true
	}
	j.interrupted = true
	return j, false
}

// close ends the session. Like nREPL, it interrupts the eval that's
// running, which goes on to finish the rest of its code.
func (sess *session) close() {
	sess.mu.Lock()
	sess.closed = true
	j := sess.running
	sess.mu.Unlock()
	if j != nil {
		signal(j.interrupts)
	}
	signal(sess.wake)
}

func (sess *session) give(input string) {
	sess.mu.Lock()
	if input == "" {
		sess.eof = true
	} else {
		sess.input += input
	}
	sess.mu.Unlock()
	signal(sess.inputs)
}

// readLine reads a line of input, asking for it with ask when there's
// none. It returns false at the end of the input, which an empty stdin
// marks.
func (sess *session) readLine(ctx context.Context, interrupts <-chan struct{}, ask func()) (string, bool, error) {
	for {
		// Input from before this point is in sess.input already.
		select {
		case <-sess.inputs:
		default:
		}
		sess.mu.Lock()
		if i := strings.IndexByte(sess.input, '\n'); i >= 0 {
			line := sess.input[:i]
			sess.input = sess.input[i+1:]
			sess.mu.Unlock()
			return line, true, nil
		}
		if sess.eof {
			line := sess.input
			sess.input, sess.eof = "", false
			sess.mu.Unlock()
			return line, line != "", nil
		}
		sess.mu.Unlock()
		ask()
		select {
		case <-sess.inputs:
		case <-interrupts:
			return "", false, throwf("java.lang.InterruptedException", "")
		case <-ctx.Done():
			return "", false, errStopping
		}
	}
}
