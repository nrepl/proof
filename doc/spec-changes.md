# Spec Changes

This is a running list of things the
[nREPL protocol spec](https://spec.nrepl.org) has to fix or add, which
we've collected while building proof. When the draft spec and the
existing clients disagree, proof follows the clients and the difference
gets documented here.

## Decisions

- The existing clients win over the draft spec. proof checks for what
  CIDER, Calva and the other clients actually need, and the spec should
  eventually be updated to match.
- Sessions are required. The draft spec lists `clone` and `close` as
  optional, but CIDER can't even connect to a server without `clone` (it
  fails when the response has no `new-session`).

## Conflicts Between the Spec and the Clients

### The Format of `ops` in `describe`

The spec says the response "must have a list of `ops`" and calls the map
format "no longer recommended". CIDER (`nrepl-op-supported-p` in
`nrepl-client.el`) and Calva (`describe.ops[op]`) both look up ops in a
map, so a list breaks both of them. (dialtone returns a map for this very
reason.) For now proof needs a map. The plan is to make CIDER and Calva
accept both formats and then relax the check to allow lists.

### The Contents of `versions`

The spec says `versions` is there "for debugging purposes" and its
example has a plain string for `nrepl`. Clients, however, identify the
runtime by the keys in `versions`, and jank used to return an empty one
([jank#782](https://github.com/jank-lang/jank/issues/782)). The entries
aren't uniform either - CIDER reads `version-string` from `nrepl`,
`clojure` and `java`, expects `babashka` to be a plain string and builds
the let-go version from `major` and `minor`.

### Mixing `value`, `out` and `err`

The spec allows `out` and `err` in the same message as `value`, and some
of its examples do that. CIDER's response handler treats them as mutually
exclusive and silently drops all but one of them when they arrive in the
same message, so they should arrive in separate messages.

### The Format of `load-file` Requests

See [spec#3](https://github.com/nrepl/spec.nrepl.org/issues/3). The spec
uses `file-path`, but all the servers we've looked at expect `file` (the
contents of the file), and some of them accept `file-path` and
`file-name` as well.

### Where `interrupted` Goes

The spec's example reuses the `id` of the eval request for the interrupt
request, doesn't specify an `interrupt-id` and puts `interrupted` in the
response to the interrupt request. The reference implementation and
dialtone respond to the interrupt request with a plain `done` (or with
`session-idle`, `interrupt-id-mismatch` or `session-ephemeral`, the
latter two along with `error` in nREPL's case) and add `interrupted` and
`done` to the response of the interrupted eval request. The spec's
fallback when there's no `interrupt-id` ("the most recent request of the
current session") also assumes sessions.

### Reused Request IDs in the Examples

The `stdin` and `interrupt` examples reuse the `id` of the eval request,
which contradicts the spec's own rule that request IDs should be unique.

### The Arglists in `lookup`

The spec says `arglist`, but the implementations use `arglists-str`
(nREPL sends a stringified `arglists` as well).

### Required `stdin`

The spec requires `stdin`, but Babashka, Basilisp, jank and ClojureCLR
don't support it, and clients work fine without it, as long as nothing
tries to read input. For now proof skips the `stdin` checks for servers
that don't advertise the op.

### Optional Sessions

See the decisions above.

## Missing From the Spec

- Most status values - `eval-error`, `no-code`, `unknown-session`,
  `namespace-not-found`, `session-idle`, `session-closed`,
  `interrupt-id-mismatch`, `session-ephemeral` and `no-info`. Servers
  disagree on almost all of them (see [Status Values](#status-values)
  below).
- The `ns` field in responses, and the fact that the `ns` of a request
  doesn't change the current namespace of the session. People still disagree about what
  the `ns` in responses means, though
  ([nrepl#171](https://github.com/nrepl/nrepl/issues/171)).
- What requests without a `session` mean. None of the spec's eval examples
  has a `session`, but it never says how such requests behave (nREPL
  gives each of them a temporary session). Calva starts its connection with such a
  request, so in practice they're required as well.
- Code with several forms. There should be one `value` per form, and
  evaluation should stop at the first error
  ([nbb#294](https://github.com/babashka/nbb/issues/294),
  [nrepl#147](https://github.com/nrepl/nrepl/issues/147)).
- There should be exactly one `done` per request, and it's not clear
  what's allowed to arrive after it. The spec allows late output, but it
  doesn't say how clients should handle it.
  ([nrepl#215](https://github.com/nrepl/nrepl/issues/215) reported two
  `done`s for an eval in an unknown namespace, but this doesn't happen
  with nREPL 1.7.0.)
- Every response to a request with a `session` should include that
  session, as Calva ignores responses without it. (For requests without
  one, nREPL includes the ID of the temporary session it created.)
- `value` and `status` in the same message. The spec's own example does
  this, but CIDER didn't handle it properly until cider#3869 (which was
  found thanks to jank).
- `root-ex`, and what `ex` should contain outside of the JVM.
- `ls-sessions`.
- Sessions should outlive connections, and `need-input` should go to the
  connection that sent the eval, even when the session was created on
  another connection ([nrepl#183](https://github.com/nrepl/nrepl/issues/183)).
- Handling of malformed input - broken messages, top-level values that
  aren't dictionaries and fields of the wrong type. In
  [nrepl#477](https://github.com/nrepl/nrepl/issues/477) such a request
  kills the session thread and later evals never get a `done`.
- How the JSON in the examples maps to bencode. There are no nil or
  boolean values in bencode, so servers shouldn't try to send them
  ([nrepl#196](https://github.com/nrepl/nrepl/issues/196),
  [scittle#123](https://github.com/babashka/scittle/issues/123)).
- Session isolation. Sessions must not share state like `*1`, and that's
  the very reason CIDER uses a separate session for its tooling.
- The startup message and the `.nrepl-port` file. Right now they're
  described only in the "Building Servers" section of the nREPL manual,
  and CIDER parses the startup message.
- Conformance levels ([spec#2](https://github.com/nrepl/spec.nrepl.org/issues/2))
  and MUST/SHOULD wording.

## Status Values

We've surveyed nREPL, dialtone, babashka.nrepl, nbb, Basilisp, jank,
let-go, clr.tools.nrepl, shadow-cljs and cider-nrepl on the server side,
and CIDER, Calva, Conjure, vim-fireplace, vim-iced, neat, REPLy,
rebel-readline and `nrepl.cmdline` on the client side.

### The Generic `error` Status

`error` dates back to the nREPL rewrite in 2012, which added it next to
`unknown-op`, `unknown-session`, `no-code` and `interrupt-id-mismatch`
without explaining why. The only written rule about it is in
`design/handlers.adoc`, which says that a malformed request "should" get
an `error`. The draft spec doesn't mention it at all. None of the clients
needs it next to a more specific status, and none of them breaks when
it's missing.

We propose that the specific status and `done` should be required, while
`error` should be allowed, but only in the final message of a response.
REPLy and rebel-readline treat both `error` and `eval-error` as the end
of a response, so nothing but `done` should follow either of them.

### Failed Evaluations

Most servers respond to failed evaluations with `eval-error`, but nbb,
let-go and shadow-cljs (for ClojureScript) send only `ex` or `err`
without any status. CIDER, Calva, REPLy and rebel-readline all rely on
`eval-error`, so it should be required. The spec's own example of a
failed evaluation doesn't include it either.

### `close`

nREPL, Babashka, nbb, ClojureCLR and let-go respond with
`session-closed`, while dialtone, Basilisp and jank respond with just
`done` (so does the spec's example). vim-fireplace forgets a session only
when it sees `session-closed`, and nREPL's own documentation tells
clients to check for it, so it should be required.

### Closing an Unknown Session

nREPL responds with `unknown-session`, dialtone with a plain `done`, and
Babashka, nbb, ClojureCLR and let-go with `session-closed`. No client
cares about this, so we just need to pick one of them.

### Unknown Sessions

nREPL and dialtone respond with `unknown-session`, while the other
servers don't check the session at all. Conjure relies on
`unknown-session` to recover from stale sessions.

### Internal Errors

According to the spec servers should respond with `server-error`, but
only dialtone does this. nREPL sends `<op>-error` and `error` for some
ops, cider-nrepl sends `<op>-error` without `error`, and most servers
don't send anything at all, so the client just hangs. The spec is right
here and the implementations need to catch up. At the very least, a
failing handler should still send `done`.

### When `lookup` Finds Nothing

The spec says that `info` should be omitted, while jank sends a plain
`error` and `done`. cider-nrepl's `info` op answers with `no-info`, which
CIDER and Calva check for, but for `lookup` CIDER just reads `info` (and
checks `lookup-error`), so omitting `info` works for it. Conjure's
`lookup` fallback is the one that really needs `no-info` - without it
`(or msg.info msg)` returns the whole message. The spec should adopt
`no-info`.

### `status` is a Set, but Some Clients Compare Lists

`nrepl.cmdline` expects exactly `["done" "session-idle"]` when
interrupting an idle session, and vim-iced's documentation lookup expects
exactly `["done"]`. The spec should say that the order of the status
values doesn't matter and that successful responses have no additional
status values, while the clients should compare sets instead.

### Including `op` in `unknown-op` Responses

Calva builds its error message from the `op` in the response. nREPL
includes it, but the spec doesn't mention it.

### `need-input` on Its Own

Calva checks `msg.status == 'need-input'`, which in JavaScript matches
only a list with a single element, so `["need-input", "done"]` (or any
other additional status) leaves it waiting forever. The spec should say
that `need-input` is sent in a message of its own and never together
with `done`.
