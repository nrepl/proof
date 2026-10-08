package serve

import (
	"context"
	"testing"
)

// The evaluator gets whatever code clients send, so nothing they send may
// crash it.
func FuzzEval(f *testing.F) {
	for _, code := range []string{
		"(+ 1 2) (/ 1 0) (- 5) (/ 2)", `(str "a" nil [1 "b"] {:a 1}) (apply str (repeat 3 "ab"))`,
		`(println "a" :k) (binding [*out* *err*] (prn 'x)) (flush)`, "(let [x 1] (when-let [y x] (if y x 2)))",
		"(def x 1) #'x @(resolve 'x) (in-ns 'foo) (ns bar) *ns* *1 *e", `(throw (ex-info "x" {:a 1})) (ex-data *e)`,
		"(read-line) (Thread/sleep 10) (future (println 1)) (require 'x)", "(let) (when) (1 2) '(1 \"a\") (quote)",
		`(or nil 1) (or) (System/getProperty "user.dir") (System/getProperty 1)`,
	} {
		f.Add(code)
	}
	interrupted := make(chan struct{})
	close(interrupted)
	f.Fuzz(func(t *testing.T, code string) {
		e := &evaluation{
			defs: newDefinitions(), b: newBindings(), ns: "user", ctx: context.Background(),
			// Sleeps end right away, as interrupted.
			interrupts: interrupted,
			waiting:    func() {},
			output:     func(string, string) {},
			readLine:   func(<-chan struct{}) (any, error) { return nil, nil },
		}
		rd := &reader{src: code}
		for {
			form, err := rd.next()
			if err != nil {
				return
			}
			e.eval(form)
		}
	})
}
