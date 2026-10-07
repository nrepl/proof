# Design

This section of the documentation explains the general approach behind
proof and the reasoning for some of its design decisions. It's aimed at
contributors and at server authors who'd like to know how much they can
trust its verdicts. If you're interested in working on proof itself,
check out [Hacking](hacking.md) as well.

## Goals

Building a basic nREPL server is not hard, but making it play nice with
CIDER, Calva and the other clients out there is a different matter. The
relevant details are spread over the reference implementation, the draft
spec, many years of client code and lots of GitHub issues. Server authors
usually discover them one bug report at a time.

proof aims to capture this knowledge in checks that server authors can
run in a few seconds and that tell them clearly what's broken and for
whom. When you run the same checks against many servers you also get a
compatibility matrix, which shows where the different implementations
agree, where they don't and where the spec needs some work.

The same knowledge cuts both ways, so proof can also check what a client
sends to a server (see [Checking Clients](#checking-clients) below).

## Compatibility, Not Conformance

The [nREPL protocol spec](https://spec.nrepl.org) is still a draft. It
doesn't define most status values, it says nothing about requests that
don't specify a `session` and in a few places it disagrees with all the
clients in use today. For instance, the spec recommends returning the
ops in `describe` as a list, but both CIDER and Calva expect them to be a
map. A server that follows the spec here would break both clients.

That's why proof checks compatibility with the existing clients and
treats the spec as one of several inputs. When the spec and the clients
disagree the clients win and the difference is recorded in
[Spec Changes](spec-changes.md), so that the spec can be updated down the
road. If the clients change (e.g. CIDER starts accepting a list of ops),
the relevant checks will be relaxed as well.

This is also the reason why proof is called a compatibility suite and not
a conformance suite. You can only conform to an official spec, and nREPL
doesn't have one yet.

## How Checks Are Graded

Every check verifies a single rule and all checks are graded the same
way:

| Verdict | When |
|---|---|
| fail | Some client won't work properly (or users will lose data) if a server gets this wrong. The check names the affected clients and links to the client code that depends on the behavior. |
| warn | The server behaves differently from the reference implementation or the spec, but no client we know of depends on this behavior. |
| note | The detail in question is up to the implementation (e.g. the format of `ex`). Notes never affect the verdict. |

The idea is that a failure should always be worth fixing and never be a
matter of taste. "nREPL does it differently" is a warning at most. If
you think some failure is unjustified, the links in the report show you
what you need to argue with.

All links to the source of clients and nREPL are pinned to a specific
commit, so they keep pointing to the right lines as those projects
evolve. The draft spec and relevant GitHub issues are linked as well, for
context.

The severity of a check applies to the check as a whole, not to
individual assertions in it. The only exception is a request that never
gets its `done` - this is always a failure, regardless of the severity of
the check, as all clients wait for `done`.

## Black-box Testing

proof talks to servers only over the network, the same way clients do.
It knows nothing about the internals of the servers and it doesn't care
what language they are written in.

The code that gets evaluated is a different matter, though - obviously
proof can't send `(+ 1 2)` to an Erlang server. That's why profiles
provide snippets in the language of the server, e.g. an expression and
its value, some code that prints something or code that raises an
error. This idea comes from
[jupyter_kernel_test](https://github.com/jupyter/jupyter_kernel_test),
which tests Jupyter kernels in a similar manner. Checks that need a
snippet the profile doesn't provide are skipped, so you can start with a
small profile and extend it gradually.

Some checks need more than snippets. For instance, `eval.unknown-ns`
makes sense only for languages that have namespaces. Profiles declare
such capabilities explicitly and checks that need a capability are
skipped if it's missing.

## Checks and Wire Checks

Most checks send some requests to the server and examine the responses -
e.g. "does `clone` return a `new-session`?" or "does an evaluation that
throws an exception produce an `eval-error`?". Each check uses new
connections and new sessions, so it can't be affected by state left
behind by other checks. The checks run one at a time.

Some checks build on others. There's no point in checking evaluation in a
session if `clone` doesn't work, so checks can declare the checks they
depend on. If one of those has already failed, the dependent checks are
skipped with a reference to the failed check. This way a broken server
gets one failure for the actual problem instead of a dozen copies of it.

The wire checks (`wire.*`) don't send any requests. Instead they look at
all the messages the server sent during the run, no matter which check
triggered them, and catch problems that can occur in any response (e.g.
a missing `session`, a second `done` or `value` and `out` in the same
message). If the same problem shows up many times, it's reported only
once along with a count, so a server that makes the same mistake in every
message doesn't produce a huge report.

This split keeps the regular checks simple. A check doesn't have to
verify that every response has the right `id`, for instance, as the wire
checks take care of that for all of them.

## Strict About the Wire, Relaxed About the Rest

proof has its own bencode implementation, as the popular Go libraries
accept a lot of things they shouldn't (e.g. dictionary keys that aren't
sorted, numbers with leading zeros and trailing garbage). Those are
exactly the problems proof is supposed to report. Its decoder distinguishes between hard errors (a
client won't be able to decode the message at all, which fails
`wire.bencode`) and smaller issues (the message can be decoded, but it's
not valid bencode, which results in a warning from `wire.canonical`).

Apart from that proof tries not to be pickier than the clients. Output is
compared after joining all its chunks, as servers are free to split it
any way they like. Checks look for the messages they need instead of
expecting an exact sequence of messages, as servers can interleave output
and status messages in many legitimate ways. (HTTP/2's h2spec, for
instance, has open bug reports about false failures caused by unrelated
frames arriving between the ones it expected.)

## Checking Clients

`proof proxy` sits between a client and a server, passes everything
along as is and records it on the way. Once it's stopped, it grades
the requests of the client against the client rules (`client.*`), which
work just like the wire checks, only in the other direction.

The [grading rule](#how-checks-are-graded) is the same as for servers,
only turned around. A client rule fails only when some server breaks (or
users lose data), and it links to the server code in question. For
instance, `client.field-types` fails when `line` isn't an integer,
because that kills the session's thread in nREPL
([nrepl#477](https://github.com/nrepl/nrepl/issues/477)). A warning means
that the client does something the spec or the reference implementation
doesn't expect, but that servers tolerate. Before a rule was added, the
mistake it catches was sent to nREPL, Babashka, Basilisp and jank to see
how they react.

As a proxy can only see the wire, proof checks what a client sends and
not what it does with the replies. Whether a client copes with output
that arrives after `done`, or with output split into many messages, is a
different problem, which needs a server that misbehaves on purpose (see
[Future Plans](#future-plans)).

Some rules are about what a client leaves behind - sessions that were
never closed and `need-input` that was never answered. They apply only
to connections the client closed itself, before the server did. If proof
is stopped while a client is still connected, the client might simply
not have gotten to them yet, and if the server hangs up first, it never
got the chance.

## Expected Failures

To be useful in the CI of a server, proof has to be able to pass while
some known problems are still unresolved. That's why profiles can list
expected failures (along with their reasons), which don't fail the run.

The problem with such lists is that they tend to get out of date.
Something gets fixed, nobody removes it from the list and then nobody
would notice if it broke again. That's why an expected failure that starts
passing fails the run as well, so the list can only get shorter. The
idea comes from the
[MCP conformance suite](https://github.com/modelcontextprotocol/conformance),
which handles its baselines the same way.

## Running Servers

Unless you point it to a running server, proof starts the server itself.
A few of the design decisions here were driven by servers that didn't
behave as expected:

- Each server runs in a new temporary directory, so port files don't end
  up in the directory proof was started from.
- Servers are started on port 0 when possible and proof reads the actual
  port from their output.
- The stdin of the server is kept open, as servers that come with a
  terminal REPL (e.g. jank's) exit when it's closed.
- On Unix each server runs in its own process group, so commands that
  start the actual server as a child process (e.g. the Clojure CLI or
  rebar3 scripts) are cleaned up properly, even if proof is interrupted.
- The connections of each check are kept open for a moment after the
  check is done, so that late messages (e.g. a second `done` or output
  after `done`) still reach the wire checks. This happens in the
  background, so it doesn't slow down the run.

## Code Organization

Here's how the codebase is organized:

```
cmd/proof            the command-line interface
internal/report      text and JSON reports, the compatibility matrix
internal/checks      the checks, the wire checks, the client rules and a fake server for testing them
internal/check       running and grading checks, expected failures
internal/server      starting servers
internal/proxy       forwarding and recording the traffic between a client and a server
internal/profile     loading profiles
nrepl                a client that records all messages
bencode              a strict bencode implementation
```

Each package depends only on the ones below it. `internal/check` knows
how to run and grade checks, but it knows nothing about specific ops -
that knowledge lives in `internal/checks`.

## Prior Art

Many of the ideas in proof were borrowed from test suites for other
protocols:

- [jupyter_kernel_test](https://github.com/jupyter/jupyter_kernel_test)
  for the language-specific snippets and skipping what a profile doesn't
  cover
- the [MCP conformance suite](https://github.com/modelcontextprotocol/conformance)
  for expected failures that can't get out of date and for checking every
  message, not just the ones a test asserts on
- [irctest](https://github.com/progval/irctest), which tests IRC servers
  against an informal protocol much like nREPL's, for the requirement that
  every test references its source
- [toml-test](https://github.com/toml-lang/toml-test) for shipping a
  language-agnostic test suite as a single Go binary
- [h2spec](https://github.com/summerwind/h2spec) and
  [Autobahn](https://github.com/crossbario/autobahn-testsuite) for
  connecting the verdicts to the spec and for grading strictness separately from
  correctness

The closest thing the nREPL world had before proof was the integration
test suite of [neat](https://github.com/nrepl/neat), which runs a few
basic checks against several servers. proof grew out of the same desire
to have a single test suite that every server can be checked against.

## Future Plans

At this point proof covers the core of the protocol (`describe`, unknown
ops, sessions, `eval`, `stdin` and the wire format) and the requests of
clients. Here's what's planned next:

- checks for `interrupt`, which every interactive client relies on
- checks for `completions`, `lookup` and `load-file` (for servers that
  support them)
- robustness checks (malformed messages, fields of the wrong type, clients
  disconnecting in the middle of an evaluation)
- client profiles that replay what specific clients send (e.g. when CIDER
  or Calva connect to a server), so a report can tell you
  directly whether CIDER will work with your server (`proof proxy`
  already sees this traffic, it just doesn't save it yet)
- a server that misbehaves on purpose (late output, output split into
  many messages, unusual status values), for client test suites to run
  against
- more servers in the compatibility matrix and a proper home for the
  matrix itself
- incorporating [Spec Changes](spec-changes.md) into the spec, so that
  the spec and proof agree and proof can eventually be versioned together
  with the spec
