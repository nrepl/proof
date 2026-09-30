# Spec changes

A running list of things [spec.nrepl.org](https://spec.nrepl.org) needs to fix
or add, collected while building the suite. When the draft spec and existing
clients disagree, the suite follows the clients and the discrepancy lands here.

## Decisions

- Existing clients win over the draft spec. The suite enforces what CIDER,
  Calva and friends actually need, and the spec gets amended to match.
- Sessions are required. The draft lists `clone` and `close` as optional, but
  CIDER can't even connect without `clone` (it fails when there's no
  `new-session` in the reply).

## Conflicts between the spec and clients

- **`describe` ops shape.** The spec prefers a list and calls the dict form "no
  longer recommended". CIDER (`nrepl-op-supported-p` in `nrepl-client.el`)
  and Calva (`describe.ops[op]`) both index it as a dict, so a list breaks
  both. dialtone emits a dict for exactly this reason. For now the suite
  requires a dict. Plan: teach CIDER and Calva to accept both forms, then
  relax the suite to allow the list.
- **`load-file` request shape** ([spec#3](https://github.com/nrepl/spec.nrepl.org/issues/3)).
  The spec uses `file-path`. Every server we looked at takes `file` (the
  file contents), some with `file-path` and `file-name` alongside.
- **Where `interrupted` goes.** The spec's example reuses the eval's `id` for
  the interrupt request, leaves out `interrupt-id`, and puts `interrupted` on
  the interrupt's own reply. The reference server and dialtone reply to the
  interrupt with plain `done` (or `session-idle` / `interrupt-id-mismatch`)
  and mark the interrupted eval with `interrupted` + `done`.
- **`lookup` arglists.** The spec says `arglist`; implementations send
  `arglists-str`.
- **Sessions optional.** See decisions above.

## Missing from the spec

- Most status values: `eval-error`, `no-code`, `unknown-session`,
  `namespace-not-found`, `session-idle`, `session-closed`,
  `interrupt-id-mismatch`, `session-ephemeral`, `no-info`. Servers disagree
  on nearly all of them. See the status section below.
- The `ns` reply field, and the fact that a request's `ns` doesn't change the
  session's current namespace ([nrepl#171](https://github.com/nrepl/nrepl/issues/171)).
- Requests without a `session` (ephemeral sessions). Calva's handshake opens
  with one, so they're effectively required too.
- Several forms in one `code`: one `value` per form, stopping at the first
  error ([nbb#294](https://github.com/babashka/nbb/issues/294),
  [nrepl#147](https://github.com/nrepl/nrepl/issues/147)).
- Exactly one `done` per request, and what may still arrive after it. The
  spec allows late output but doesn't say how clients should route it.
  ([nrepl#215](https://github.com/nrepl/nrepl/issues/215) reported two
  `done`s for an eval in an unknown namespace; it doesn't reproduce with
  nREPL 1.7.0.)
- `value`, `out` and `err` in separate messages. CIDER's response handler
  treats them as mutually exclusive and silently drops all but one when they
  share a message.
- Echoing `session` on every reply. Calva drops replies without it.
- `value` and `status` in the same message. The spec's own example does it,
  but CIDER only handled it after cider#3869 (found via jank).
- `root-ex`, and what `ex` should contain outside the JVM.
- `ls-sessions`.
- Sessions outliving connections ([nrepl#183](https://github.com/nrepl/nrepl/issues/183)).
- Malformed input: broken frames, a top-level value that isn't a dict, wrong
  field types ([nrepl#477](https://github.com/nrepl/nrepl/issues/477) kills the
  session thread and later evals never get `done`).
- No nil or boolean values on the wire. Bencode has neither
  ([nrepl#196](https://github.com/nrepl/nrepl/issues/196),
  [scittle#123](https://github.com/babashka/scittle/issues/123)).
- What goes in `versions`. Clients identify the runtime by its keys, and jank
  shipped an empty one ([jank#782](https://github.com/jank-lang/jank/issues/782)).
  The entries aren't uniform either: CIDER reads `version-string` from
  `nrepl`, `clojure` and `java`, takes `babashka` as a plain string, and
  builds let-go's version from `major`/`minor`.
- Session isolation. Sessions must not share state like `*1`; CIDER keeps a
  separate tooling session for exactly that reason.
- The startup banner and `.nrepl-port` file. Today they're only described in
  nrepl.org's "Building servers" page, and CIDER parses the banner.
- Conformance levels ([spec#2](https://github.com/nrepl/spec.nrepl.org/issues/2))
  and MUST/SHOULD wording. The examples are also in JSON while the wire
  format is bencode, which is worth a sentence.

## Status values

Surveyed nrepl, dialtone, babashka.nrepl, nbb, basilisp, jank, let-go,
clr.tools.nrepl, shadow-cljs and cider-nrepl on the server side, and CIDER,
Calva, Conjure, vim-fireplace, vim-iced, neat, REPLy, rebel-readline and
`nrepl.cmdline` on the client side.

- **The generic `error` status.** It dates back to the 2012 nREPL rewrite,
  which added it next to `unknown-op`, `unknown-session`, `no-code` and
  `interrupt-id-mismatch` without giving a reason. The only written rule is
  in `design/handlers.adoc`: a malformed request "should" get `error`. The
  draft spec never mentions it. No client needs it next to a specific
  status, and none breaks when it's missing. Proposal: specific status plus
  `done` is required, `error` is allowed, and `error` must only ever appear
  on the final message (REPLy and rebel-readline treat it as terminal).
- **Eval failures** get `eval-error` from most servers, but nbb, let-go and
  shadow-cljs's cljs path send only `ex`/`err` with no status. CIDER, Calva,
  REPLy and rebel key on `eval-error`, so it should be required.
- **`close`**: nrepl, bb, nbb, clr and let-go send `session-closed`, dialtone,
  basilisp and jank send bare `done`. vim-fireplace only drops a session on
  `session-closed`, and nrepl's own docstring tells clients to check for it.
  Should be required.
- **Closing an unknown session** returns `unknown-session` (nrepl), plain
  `done` (dialtone) or `session-closed` anyway (bb, nbb, clr, let-go). No
  client cares. Pick one.
- **Unknown session** gets `unknown-session` from nrepl and dialtone and isn't
  checked by anyone else. Conjure's session recovery depends on it.
  `session-not-found` isn't used by any server or client.
- **Internal failures** get `server-error` per the spec, and only dialtone
  sends it. nrepl sends `<op>-error` + `error` for some ops, cider-nrepl sends
  `<op>-error` without `error`, and most servers send nothing at all and the
  client hangs. The spec is right here, and implementations need to catch up.
  At minimum a failing handler must still send `done`.
- **Lookup misses**: the spec says to omit `info`. cider-nrepl and let-go add
  `no-info`, jank sends a bare `error` + `done`. Clients (CIDER, Calva,
  Conjure, iced) look for `no-info`, so the spec should adopt it.
- **`status` is a set, but some clients compare lists.** `nrepl.cmdline`
  requires exactly `["done" "session-idle"]` on an idle interrupt, and
  vim-iced's doc lookup requires exactly `["done"]`. The spec should say
  status is unordered and that success replies carry no extra flags. The
  clients should compare sets.
- **Echoing `op` on `unknown-op`**: Calva builds its error message from
  `res.op`. nrepl echoes it and the spec doesn't mention it.
- **`need-input` has to arrive alone.** Calva checks
  `msg.status == 'need-input'`, which in JS only matches a one-element list,
  so `["need-input", "done"]` or any extra flag leaves it waiting forever.
  The spec should say `need-input` goes on its own message, never with `done`.
