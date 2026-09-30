// Package checks holds the compatibility checks and wire rules.
package checks

import (
	"strconv"

	"github.com/nrepl/proof/internal/check"
)

// Links are pinned to commits so line numbers stay put.
const (
	ciderBase     = "https://github.com/clojure-emacs/cider/blob/9e049baa1c2c136724d7538b1df6898ee77de6e7/lisp/"
	calvaBase     = "https://github.com/BetterThanTomorrow/calva/blob/ad00dd7e518e0d08262810673111eaeddc5c2fdd/src/nrepl/index.ts"
	conjureBase   = "https://github.com/Olical/conjure/blob/9842bf38464df071f72546f65fa6ec44942ca7b2/fnl/conjure/client/clojure/nrepl/server.fnl"
	fireplaceBase = "https://github.com/tpope/vim-fireplace/blob/5e66509599de92550762cf2681338fc4cd1e71cf/autoload/"
	nreplBase     = "https://github.com/nrepl/nrepl/blob/edf294a7739b99549accdb6dbbc2fc83db6d9094/src/clojure/nrepl/"
	specBase      = "https://github.com/nrepl/spec.nrepl.org/blob/67796e34ac34f2f28c3af685fc3ab432fe5eb03f/spec.md"
	replyBase     = "https://github.com/trptcolin/reply/blob/2b28587004aa3b5cd5d4548eb87f7674eb999b13/src/reply/eval_modes/nrepl.clj"
	rebelBase     = "https://github.com/bhauman/rebel-readline/blob/d8573a61aad5cbbd83532e2050b6595cfb5b13bb/rebel-readline-nrepl/src/rebel_readline/nrepl/service/nrepl.clj"
)

func ref(name, url string) check.Ref { return check.Ref{Name: name, URL: url} }

func issue(repo string, n int) check.Ref {
	num := strconv.Itoa(n)
	return check.Ref{Name: repo + "#" + num, URL: "https://github.com/" + repo + "/issues/" + num}
}

var (
	ciderOpSupported   = ref("CIDER nrepl-op-supported-p", ciderBase+"nrepl-client.el#L233-L237")
	ciderClone         = ref("CIDER clones a main and a tooling session on connect", ciderBase+"nrepl-client.el#L740-L760")
	ciderPayloadCond   = ref("CIDER reads value/out/err as mutually exclusive", ciderBase+"nrepl-client.el#L875-L887")
	ciderEvalError     = ref("CIDER eval-error handling", ciderBase+"nrepl-client.el#L898-L899")
	ciderDone          = ref("CIDER completes requests on done", ciderBase+"nrepl-client.el#L900-L902")
	ciderNsNotFound    = ref("CIDER namespace-not-found handling", ciderBase+"nrepl-client.el#L945")
	ciderRuntime       = ref("CIDER runtime detection from versions", ciderBase+"cider-session.el#L218-L291")
	calvaOpSupported   = ref("Calva describe.ops[op]", calvaBase+"#L492")
	calvaUnknownOp     = ref("Calva unknown-op handling", calvaBase+"#L50-L61")
	calvaRouting       = ref("Calva routes replies by session", calvaBase+"#L287-L291")
	calvaHandshake     = ref("Calva's sessionless handshake eval", calvaBase+"#L297-L299")
	calvaEvalError     = ref("Calva eval-error handling", calvaBase+"#L1465")
	calvaNeedInput     = ref("Calva need-input check", calvaBase+"#L1477")
	conjureSession     = ref("Conjure unknown-session recovery", conjureBase+"#L412-L414")
	conjureNsMissing   = ref("Conjure namespace-not-found handling", conjureBase+"#L415-L416")
	fireplaceClosed    = ref("vim-fireplace session-closed handling", fireplaceBase+"fireplace/transport.vim#L147")
	fireplaceNsMissing = ref("vim-fireplace namespace-not-found handling", fireplaceBase+"fireplace.vim#L486")
	replyTerminal      = ref("REPLy stops at error/eval-error", replyBase+"#L57-L66")
	rebelTerminal      = ref("rebel-readline stops at error/eval-error", rebelBase+"#L70-L83")
	nreplUnknownOp     = ref("nREPL unknown-op reply", nreplBase+"server.clj#L109-L112")
	nreplNoCode        = ref("nREPL no-code reply", nreplBase+"middleware/interruptible_eval.clj#L190")
	nreplNsNotFound    = ref("nREPL namespace-not-found reply", nreplBase+"middleware/interruptible_eval.clj#L197")
	nreplEvalError     = ref("nREPL eval-error reply", nreplBase+"middleware/interruptible_eval.clj#L118-L123")
	nreplSessionClosed = ref("nREPL close reply", nreplBase+"middleware/session.clj#L303")
	nreplUnknownSess   = ref("nREPL unknown-session reply", nreplBase+"middleware/session.clj#L335")
	nreplDescribe      = ref("nREPL describe reply", nreplBase+"middleware.clj#L62-L66")
	specProtocol       = ref("spec: protocol description", specBase+"#L21-L53")
	specDescribe       = ref("spec: describe op", specBase+"#L59-L90")
	specEval           = ref("spec: eval op", specBase+"#L91-L182")
	specOptionalOps    = ref("spec: optional operations", specBase+"#L213-L218")
	specClone          = ref("spec: clone op", specBase+"#L356-L386")
	specClose          = ref("spec: close op", specBase+"#L387-L411")
)
