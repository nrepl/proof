package serve

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"
)

// bindings are what nREPL keeps for each session: the current namespace
// and the last results. Definitions belong to the server, as namespaces do
// in Clojure.
type bindings struct {
	mu      sync.Mutex
	ns      string
	results [3]any // *1, *2 and *3
	lastErr any    // *e
}

func newBindings() *bindings { return &bindings{ns: "user"} }

func (b *bindings) copy() *bindings {
	b.mu.Lock()
	defer b.mu.Unlock()
	return &bindings{ns: b.ns, results: b.results, lastErr: b.lastErr}
}

func (b *bindings) currentNS() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ns
}

func (b *bindings) setNS(ns string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ns = ns
}

// push makes v the latest result, *1.
func (b *bindings) push(v any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.results = [3]any{v, b.results[0], b.results[1]}
}

func (b *bindings) failed(ex *exception) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastErr = ex
}

// recall returns *1, *2, *3 or *e.
func (b *bindings) recall(name string) any {
	b.mu.Lock()
	defer b.mu.Unlock()
	if name == "*e" {
		return b.lastErr
	}
	return b.results[name[1]-'1']
}

// definitions are the namespaces and the vars defined in them, which all
// sessions share. The vars include clojure.core's functions.
type definitions struct {
	mu   sync.Mutex
	nss  map[string]bool
	vars map[string]any // by qualified name
}

func newDefinitions() *definitions {
	d := &definitions{nss: map[string]bool{"user": true, "clojure.core": true}, vars: map[string]any{}}
	for name := range builtins {
		if qualified(name) {
			d.vars[name] = &function{name}
		} else {
			d.vars["clojure.core/"+name] = &function{name}
		}
	}
	return d
}

func (d *definitions) lookup(qualified string) (any, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v, ok := d.vars[qualified]
	return v, ok
}

func (d *definitions) define(qualified string, v any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.vars[qualified] = v
}

func (d *definitions) hasNS(ns string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.nss[ns]
}

func (d *definitions) addNS(ns string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.nss[ns] = true
}

// evaluation evaluates the forms of one eval request.
type evaluation struct {
	defs *definitions
	b    *bindings
	ns   string // the namespace the code runs in
	// ctx ends when proof stops, and interrupts takes interrupts.
	ctx        context.Context
	interrupts <-chan struct{}
	// waiting is called before the code waits for something.
	waiting func()
	// output passes on what the code prints, as "out" or "err".
	output func(key, text string)
	// readLine reads a line of input, or returns nil at the end of it.
	readLine func(interrupts <-chan struct{}) (any, error)
	// locals are the names let and when-let bind.
	locals map[string]any
	// futures are the futures to run once the eval is done.
	futures []pending
	toErr   bool
}

// pending is the body of a future, with the evaluation it runs in.
type pending struct {
	e    *evaluation
	body []any
}

var errStopping = errors.New("proof is stopping")

type phase int

const (
	running phase = iota
	reading
	compiling
)

// thrown is an exception from running code, or from reading or compiling
// it.
type thrown struct {
	ex    *exception
	phase phase
	// line and col say where reading failed, and form which special form
	// couldn't be compiled.
	line, col int
	form      string
}

func (t *thrown) Error() string { return t.ex.msg }

func throwf(class, format string, args ...any) error {
	return &thrown{ex: &exception{class: class, msg: fmt.Sprintf(format, args...)}}
}

func compileError(form, format string, args ...any) error {
	return &thrown{ex: &exception{class: "clojure.lang.Compiler$CompilerException", msg: fmt.Sprintf(format, args...)},
		phase: compiling, form: form}
}

// errorReport is what nREPL 1.8.0 sends about an exception: the text for
// err, and the classes for ex and root-ex.
func errorReport(t *thrown, ns string) (text, ex, rootEx string) {
	class := "class " + t.ex.class
	switch t.phase {
	case reading:
		return fmt.Sprintf("Syntax error reading source at (REPL:%d:%d).\n%s\n", t.line, t.col, t.ex.msg),
			class, "class java.lang.RuntimeException"
	case compiling:
		form := ""
		if t.form != "" {
			form = t.form + " "
		}
		return fmt.Sprintf("Syntax error compiling %sat (REPL:0:0).\n%s\n", form, t.ex.msg), class, class
	}
	simple := t.ex.class[strings.LastIndexByte(t.ex.class, '.')+1:]
	return fmt.Sprintf("Execution error (%s) at %s/eval1 (REPL:1).\n%s\n", simple, ns, t.ex.msg), class, class
}

func (e *evaluation) eval(form any) (any, error) {
	switch f := form.(type) {
	case symbol:
		return e.resolve(string(f))
	case list:
		if len(f) == 0 {
			return f, nil
		}
		return e.call(f)
	case vector:
		xs, err := e.evalEach(f)
		return vector(xs), err
	case mapForm:
		xs, err := e.evalEach(f)
		return mapForm(xs), err
	}
	return form, nil
}

func (e *evaluation) evalEach(forms []any) ([]any, error) {
	vals := make([]any, 0, len(forms))
	for _, f := range forms {
		v, err := e.eval(f)
		if err != nil {
			return nil, err
		}
		vals = append(vals, v)
	}
	return vals, nil
}

// do evaluates forms in order and returns the value of the last one.
func (e *evaluation) do(forms []any) (any, error) {
	var v any
	for _, f := range forms {
		var err error
		if v, err = e.eval(f); err != nil {
			return nil, err
		}
	}
	return v, nil
}

func (e *evaluation) resolve(name string) (any, error) {
	if v, ok := e.locals[name]; ok {
		return v, nil
	}
	switch name {
	case "*1", "*2", "*3", "*e":
		return e.b.recall(name), nil
	case "*ns*":
		return namespace(e.ns), nil
	}
	if v, ok := e.defs.lookup(e.qualify(name)); ok {
		return v, nil
	}
	return nil, compileError("", "Unable to resolve symbol: %s in this context", name)
}

// qualify names the var a symbol refers to: clojure.core's if it has one
// by that name, and otherwise one in the current namespace.
func (e *evaluation) qualify(name string) string {
	switch {
	case qualified(name):
		return name
	case builtins[name] != nil:
		return "clojure.core/" + name
	}
	return e.ns + "/" + name
}

// qualified reports whether a symbol names its namespace (like
// clojure.core/str, but unlike /).
func qualified(name string) bool { return strings.Contains(name[1:], "/") }

// varOf returns the var a symbol refers to, or nil if there's none.
func (e *evaluation) varOf(name string) *variable {
	qualified := e.qualify(name)
	if _, ok := e.defs.lookup(qualified); !ok {
		return nil
	}
	ns, n, _ := strings.Cut(qualified, "/")
	return &variable{ns, n}
}

func (e *evaluation) call(l list) (any, error) {
	if s, ok := l[0].(symbol); ok && specials[string(s)] != nil {
		return specials[string(s)](e, l[1:])
	}
	head, err := e.eval(l[0])
	if err != nil {
		return nil, err
	}
	args, err := e.evalEach(l[1:])
	if err != nil {
		return nil, err
	}
	return e.apply(head, args)
}

func (e *evaluation) apply(f any, args []any) (any, error) {
	fn, ok := f.(*function)
	if !ok {
		return nil, castError(f, "clojure.lang.IFn")
	}
	return builtins[fn.name](e, args)
}

func truthy(v any) bool { return v != nil && v != false }

func firstSymbol(args []any) (symbol, bool) {
	if len(args) == 0 {
		return "", false
	}
	s, ok := args[0].(symbol)
	return s, ok
}

// enter switches to a namespace, creating it if needed.
func (e *evaluation) enter(ns string) {
	e.defs.addNS(ns)
	e.ns = ns
}

// builtin is a special form or a function. Special forms get their
// arguments as they were read, and functions get them evaluated.
type builtin func(e *evaluation, args []any) (any, error)

// specials are the special forms (and macros) proof serve knows, and
// builtins the functions. They're filled in by init, as some of them call
// back into the evaluator.
var specials, builtins map[string]builtin

func init() {
	specials = map[string]builtin{
		"quote": func(e *evaluation, args []any) (any, error) {
			if len(args) != 1 {
				return nil, arity("quote", len(args))
			}
			return args[0], nil
		},
		"var": func(e *evaluation, args []any) (any, error) {
			s, ok := firstSymbol(args)
			if !ok || len(args) != 1 {
				return nil, arity("var", len(args))
			}
			if v := e.varOf(string(s)); v != nil {
				return v, nil
			}
			return nil, compileError("var", "Unable to resolve var: %s in this context", s)
		},
		"do": (*evaluation).do,
		"when": func(e *evaluation, args []any) (any, error) {
			if len(args) == 0 {
				return nil, arity("when", 0)
			}
			test, err := e.eval(args[0])
			if err != nil || !truthy(test) {
				return nil, err
			}
			return e.do(args[1:])
		},
		"let":      binder("let"),
		"when-let": binder("when-let"),
		"or": func(e *evaluation, args []any) (any, error) {
			var v any
			for _, arg := range args {
				var err error
				if v, err = e.eval(arg); err != nil || truthy(v) {
					return v, err
				}
			}
			return v, nil
		},
		"if": func(e *evaluation, args []any) (any, error) {
			if len(args) < 2 || len(args) > 3 {
				return nil, arity("if", len(args))
			}
			test, err := e.eval(args[0])
			switch {
			case err != nil:
				return nil, err
			case truthy(test):
				return e.eval(args[1])
			case len(args) == 3:
				return e.eval(args[2])
			}
			return nil, nil
		},
		"def": func(e *evaluation, args []any) (any, error) {
			s, ok := firstSymbol(args)
			if !ok || len(args) > 2 || strings.Contains(string(s), "/") {
				return nil, throwf("java.lang.RuntimeException", "proof serve only supports (def name value)")
			}
			v, err := e.do(args[1:])
			if err != nil {
				return nil, err
			}
			e.defs.define(e.ns+"/"+string(s), v)
			return &variable{e.ns, string(s)}, nil
		},
		"ns": func(e *evaluation, args []any) (any, error) {
			s, ok := firstSymbol(args)
			if !ok {
				return nil, throwf("java.lang.IllegalArgumentException", "ns needs a name")
			}
			e.enter(string(s))
			return nil, nil
		},
		"binding": func(e *evaluation, args []any) (any, error) {
			if len(args) == 0 || pr(args[0]) != "[*out* *err*]" {
				return nil, throwf("java.lang.RuntimeException", "proof serve only supports (binding [*out* *err*] ...)")
			}
			was := e.toErr
			e.toErr = true
			defer func() { e.toErr = was }()
			return e.do(args[1:])
		},
		"throw": func(e *evaluation, args []any) (any, error) {
			if len(args) != 1 {
				return nil, arity("throw", len(args))
			}
			v, err := e.eval(args[0])
			if err != nil {
				return nil, err
			}
			ex, ok := v.(*exception)
			if !ok {
				return nil, castError(v, "java.lang.Throwable")
			}
			return nil, &thrown{ex: ex}
		},
		"future": func(e *evaluation, args []any) (any, error) {
			// It runs with the bindings and locals it was made with.
			f := *e
			f.futures, f.locals = nil, maps.Clone(e.locals)
			e.futures = append(e.futures, pending{&f, args})
			return &future{}, nil
		},
	}

	builtins = map[string]builtin{
		"+":   arithmetic("+", (*big.Rat).Add, 0),
		"-":   arithmetic("-", (*big.Rat).Sub, 0),
		"*":   arithmetic("*", (*big.Rat).Mul, 1),
		"/":   arithmetic("/", (*big.Rat).Quo, 1),
		"inc": func(e *evaluation, args []any) (any, error) { return oneMore(e, "inc", "+", args) },
		"dec": func(e *evaluation, args []any) (any, error) { return oneMore(e, "dec", "-", args) },
		"str": func(e *evaluation, args []any) (any, error) {
			// Like toString in Clojure: strings as they are, namespaces by
			// name and everything else as pr prints it.
			var b strings.Builder
			for _, a := range args {
				switch x := a.(type) {
				case nil:
				case string:
					b.WriteString(x)
				case namespace:
					b.WriteString(string(x))
				default:
					b.WriteString(pr(x))
				}
			}
			return b.String(), nil
		},
		"print":   printer(false, false),
		"println": printer(false, true),
		"pr":      printer(true, false),
		"prn":     printer(true, true),
		// Output goes out as soon as it's printed.
		"flush": func(e *evaluation, args []any) (any, error) { return nil, nil },
		"read-line": func(e *evaluation, args []any) (any, error) {
			if len(args) != 0 {
				return nil, arity("read-line", len(args))
			}
			return e.readLine(e.interrupts)
		},
		"ex-info": func(e *evaluation, args []any) (any, error) {
			if len(args) < 2 || len(args) > 3 {
				return nil, arity("ex-info", len(args))
			}
			msg, ok := args[0].(string)
			if !ok {
				return nil, castError(args[0], "java.lang.String")
			}
			return &exception{class: "clojure.lang.ExceptionInfo", msg: msg, data: args[1]}, nil
		},
		"ex-data": func(e *evaluation, args []any) (any, error) {
			if len(args) != 1 {
				return nil, arity("ex-data", len(args))
			}
			if ex, ok := args[0].(*exception); ok {
				return ex.data, nil
			}
			return nil, nil
		},
		"in-ns": func(e *evaluation, args []any) (any, error) {
			if len(args) != 1 {
				return nil, arity("in-ns", len(args))
			}
			s, ok := args[0].(symbol)
			if !ok {
				return nil, castError(args[0], "clojure.lang.Symbol")
			}
			e.enter(string(s))
			return namespace(s), nil
		},
		// There's nothing to load, so requiring anything works.
		"require": func(e *evaluation, args []any) (any, error) { return nil, nil },
		"resolve": func(e *evaluation, args []any) (any, error) {
			if len(args) != 1 {
				return nil, arity("resolve", len(args))
			}
			s, ok := args[0].(symbol)
			if !ok {
				return nil, castError(args[0], "clojure.lang.Symbol")
			}
			if v := e.varOf(string(s)); v != nil {
				return v, nil
			}
			return nil, nil
		},
		"deref": func(e *evaluation, args []any) (any, error) {
			if len(args) != 1 {
				return nil, arity("deref", len(args))
			}
			switch x := args[0].(type) {
			case *variable:
				v, _ := e.defs.lookup(x.ns + "/" + x.name)
				return v, nil
			case *future:
				return nil, throwf("java.lang.UnsupportedOperationException", "proof serve runs futures once the eval is done, so it can't wait for one")
			}
			return nil, castError(args[0], "java.util.concurrent.Future")
		},
		// What vim-fireplace asks for to find the classpath.
		"System/getProperty": func(e *evaluation, args []any) (any, error) {
			if len(args) != 1 {
				return nil, arity("System/getProperty", len(args))
			}
			key, ok := args[0].(string)
			if !ok {
				return nil, castError(args[0], "java.lang.String")
			}
			switch key {
			case "path.separator":
				return string(os.PathListSeparator), nil
			case "java.class.path":
				return "src", nil
			case "user.dir":
				dir, err := os.Getwd()
				if err != nil {
					return nil, nil
				}
				return dir, nil
			}
			return nil, nil
		},
		"Thread/sleep": func(e *evaluation, args []any) (any, error) {
			if len(args) != 1 {
				return nil, arity("Thread/sleep", len(args))
			}
			ms, ok := args[0].(int64)
			if !ok {
				return nil, castError(args[0], "java.lang.Number")
			}
			if ms < 0 {
				return nil, throwf("java.lang.IllegalArgumentException", "timeout value is negative")
			}
			t := time.NewTimer(time.Duration(min(ms, math.MaxInt64/int64(time.Millisecond))) * time.Millisecond)
			defer t.Stop()
			e.waiting()
			select {
			case <-t.C:
				return nil, nil
			case <-e.interrupts:
				return nil, throwf("java.lang.InterruptedException", "sleep interrupted")
			case <-e.ctx.Done():
				return nil, errStopping
			}
		},
		"apply": func(e *evaluation, args []any) (any, error) {
			if len(args) < 2 {
				return nil, arity("apply", len(args))
			}
			var spread []any
			switch last := args[len(args)-1].(type) {
			case list:
				spread = last
			case vector:
				spread = last
			default:
				return nil, castError(last, "clojure.lang.ISeq")
			}
			return e.apply(args[0], append(append([]any{}, args[1:len(args)-1]...), spread...))
		},
		"repeat": func(e *evaluation, args []any) (any, error) {
			if len(args) != 2 {
				return nil, arity("repeat", len(args))
			}
			n, ok := args[0].(int64)
			if !ok {
				return nil, castError(args[0], "java.lang.Number")
			}
			// Plenty for a long value, while keeping one eval from eating
			// all the memory.
			if n > 1<<20 {
				return nil, throwf("java.lang.OutOfMemoryError", "Java heap space")
			}
			xs := make(list, max(n, 0))
			for i := range xs {
				xs[i] = args[1]
			}
			return xs, nil
		},
	}
}

// binder makes let and when-let, which bind names to values (only plain
// names, no destructuring). when-let gives up on a value that isn't
// truthy.
func binder(name string) builtin {
	return func(e *evaluation, args []any) (any, error) {
		var pairs vector
		if len(args) > 0 {
			pairs, _ = args[0].(vector)
		}
		if pairs == nil || len(pairs)%2 != 0 || (name == "when-let" && len(pairs) != 2) {
			return nil, throwf("java.lang.IllegalArgumentException", "%s needs a vector of names and values", name)
		}
		saved := e.locals
		defer func() { e.locals = saved }()
		e.locals = maps.Clone(saved)
		if e.locals == nil {
			e.locals = map[string]any{}
		}
		for i := 0; i < len(pairs); i += 2 {
			s, ok := pairs[i].(symbol)
			if !ok {
				return nil, throwf("java.lang.IllegalArgumentException", "proof serve only binds plain names")
			}
			v, err := e.eval(pairs[i+1])
			if err != nil || (name == "when-let" && !truthy(v)) {
				return nil, err
			}
			e.locals[string(s)] = v
		}
		return e.do(args[1:])
	}
}

func printer(readably, newline bool) builtin {
	return func(e *evaluation, args []any) (any, error) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = printed(a, readably)
		}
		text := strings.Join(parts, " ")
		if newline {
			text += "\n"
		}
		key := "out"
		if e.toErr {
			key = "err"
		}
		if text != "" {
			e.output(key, text)
		}
		return nil, nil
	}
}

// arithmetic works on exact numbers like Clojure does, so (/ 1 2) is a
// ratio and (/ 1 0) throws.
func arithmetic(name string, op func(z, x, y *big.Rat) *big.Rat, identity int64) builtin {
	return func(e *evaluation, args []any) (any, error) {
		nums := make([]*big.Rat, len(args))
		for i, a := range args {
			switch n := a.(type) {
			case int64:
				nums[i] = big.NewRat(n, 1)
			case ratio:
				nums[i] = big.NewRat(n.num, n.den)
			default:
				return nil, castError(a, "java.lang.Number")
			}
		}
		if len(nums) == 0 {
			if name == "-" || name == "/" {
				return nil, arity(name, 0)
			}
			return identity, nil
		}
		acc, rest := new(big.Rat).Set(nums[0]), nums[1:]
		if len(nums) == 1 && (name == "-" || name == "/") {
			// (- x) is (- 0 x), and (/ x) is (/ 1 x).
			acc, rest = big.NewRat(identity, 1), nums
		}
		for _, n := range rest {
			if name == "/" && n.Sign() == 0 {
				return nil, throwf("java.lang.ArithmeticException", "Divide by zero")
			}
			op(acc, acc, n)
		}
		if !acc.Num().IsInt64() || !acc.Denom().IsInt64() {
			return nil, throwf("java.lang.ArithmeticException", "long overflow")
		}
		if acc.IsInt() {
			return acc.Num().Int64(), nil
		}
		return ratio{acc.Num().Int64(), acc.Denom().Int64()}, nil
	}
}

func oneMore(e *evaluation, name, op string, args []any) (any, error) {
	if len(args) != 1 {
		return nil, arity(name, len(args))
	}
	return builtins[op](e, []any{args[0], int64(1)})
}

func arity(name string, n int) error {
	return throwf("clojure.lang.ArityException", "Wrong number of args (%d) passed to: clojure.core/%s", n, name)
}

func castError(v any, to string) error {
	return throwf("java.lang.ClassCastException", "class %s cannot be cast to class %s", className(v), to)
}

func className(v any) string {
	switch v.(type) {
	case nil:
		return "nil"
	case bool:
		return "java.lang.Boolean"
	case int64:
		return "java.lang.Long"
	case ratio:
		return "clojure.lang.Ratio"
	case string:
		return "java.lang.String"
	case keyword:
		return "clojure.lang.Keyword"
	case symbol:
		return "clojure.lang.Symbol"
	case list:
		return "clojure.lang.PersistentList"
	case vector:
		return "clojure.lang.PersistentVector"
	case mapForm:
		return "clojure.lang.PersistentArrayMap"
	case namespace:
		return "clojure.lang.Namespace"
	case *variable:
		return "clojure.lang.Var"
	case *exception:
		return "clojure.lang.ExceptionInfo"
	}
	return "java.lang.Object"
}
