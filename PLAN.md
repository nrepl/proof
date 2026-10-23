# Plan

This is what's planned for proof, along with what's done so far. It
changes as we go, so it's a good idea to check it before starting on
something big.

## Done So Far

- 2026-09-30: the first version, with the core checks (`describe`,
  unknown ops, sessions, `eval`, `stdin` and the wire format), server
  profiles and a daily compatibility matrix for nREPL, Babashka,
  ClojureCLR, Basilisp, jank and dialtone. The profile for repartee came
  a day later.
- 2026-10-07: `proof proxy` for checking what clients send
  ([#1](https://github.com/nrepl/proof/pull/1)).
- 2026-10-08: `proof serve` for testing clients and `-like` for acting
  like a whole server ([#2](https://github.com/nrepl/proof/pull/2),
  [#3](https://github.com/nrepl/proof/pull/3)), client profiles for
  CIDER, Calva, Conjure and vim-fireplace
  ([#4](https://github.com/nrepl/proof/pull/4),
  [#6](https://github.com/nrepl/proof/pull/6)) and `-record`
  ([#5](https://github.com/nrepl/proof/pull/5)).
- 2026-10-09: nREPL 1.8.0 as the reference
  ([#7](https://github.com/nrepl/proof/pull/7)).
- 2026-10-10: client profiles for REPLy and neorepl
  ([#8](https://github.com/nrepl/proof/pull/8)).

## 0.1.0

The first release. Once it's out, server authors can run a fixed
version of proof in their CI (jank is already looking into this, see
[jank#1104](https://github.com/jank-lang/jank/issues/1104)), and it
covers the rest of what every editor sends: `interrupt`, `load-file`,
lookups and completions, plus requests that go wrong.

### Rough Edges

- Done: `wire.error-terminal` no longer fails Babashka for a message
  with nothing but `changed-namespaces` that arrives after `eval-error`.
  The clients it names (REPLy and rebel-readline) lose only output,
  values and `need-input` there, so that's what it looks for now.
- Done: `proof run babashka` and `proof proxy babashka` work, just like
  `proof serve -like babashka`, as the profiles are embedded in the
  binary. People who install proof with `go install` don't have the
  `profiles` folder at all.
- Done: `proof run profiles/babashka.toml -only 'eval.*'` works, as
  flags can come after the profile (and after the scenarios of `proof
  serve`).
- Done: `proof run -h` says how to use `proof run`, rather than Go's bare
  "Usage of run:", and the same goes for `proxy` and `serve`.

### Releases

- Done: `proof version` takes the version from the build info, so a
  binary installed with `go install ...@v0.1.0` knows its version.
- Done: a CHANGELOG.
- Done: binaries for Linux, macOS and Windows on every release, so the
  CI of a server doesn't need Go. The Release workflow builds them when
  a version gets tagged.
- A GitHub Action, so a server's workflow needs nothing but `uses:
  nrepl/proof@v0.1.0` and the path to its profile.
- The CI section of [Usage](doc/usage.md) uses the release instead of
  `@latest`.

### interrupt

- Checks for interrupting a running eval (the eval gets `interrupted`
  and `done`, the interrupt gets a `done` of its own), interrupting an
  idle session (`session-idle`), an `interrupt-id` that doesn't match
  and the session working afterwards.
- An interrupt sent right after its eval. nREPL marks a session as
  running only once the eval leaves the session's queue, so such an
  interrupt can find the session idle
  ([nrepl#504](https://github.com/nrepl/nrepl/issues/504)). The evals
  waiting behind an interrupted one
  ([nrepl#503](https://github.com/nrepl/nrepl/issues/503)) and the
  exception of an interrupted eval, which nREPL sends after its `done`
  ([nrepl#506](https://github.com/nrepl/nrepl/issues/506)), need checks
  too.
- Where an interrupted eval's `done` goes when the interrupt comes from
  another connection
  ([nrepl#498](https://github.com/nrepl/nrepl/pull/498)). Once an nREPL
  release has the fix, `profiles/clojure.toml` and `proof serve` move to
  it.
- A step option for client profiles that sends a request without waiting
  for its reply, so profiles can send `interrupt` and `stdin` while an
  eval is running, the way CIDER, Calva and Conjure do.

### load-file

- Checks for `load-file` with the fields CIDER, Calva and Conjure send.
  The spec and the servers don't agree on which field has the contents
  of the file ([spec#3](https://github.com/nrepl/spec.nrepl.org/issues/3)).

### Lookups and Completions

- Checks for nREPL's `lookup` and `completions` and for cider-nrepl's
  `info`, `complete` and `eldoc`, for servers that list them in
  `describe`. nREPL has the former, Basilisp the latter and Babashka
  both. CIDER asks for cider-nrepl's ops by their `cider/` names, falls
  back to the old names and then to nREPL's ops, and the checks should
  do the same.
- Client profile checks for the completion and documentation requests of
  CIDER and Calva.
- [spec#9](https://github.com/nrepl/spec.nrepl.org/pull/9) (`arglists-str`
  and `no-info`) should be settled by then.

### Requests That Go Wrong

- Malformed messages, top-level values that aren't dicts and fields of
  the wrong type ([nrepl#477](https://github.com/nrepl/nrepl/issues/477)).
- An eval whose reply bencode can't carry, e.g. a boolean
  ([babashka#2201](https://github.com/babashka/babashka/issues/2201)).
- A client that goes away in the middle of an eval
  ([babashka#2227](https://github.com/babashka/babashka/issues/2227)).
- Two clients evaluating code at the same time
  ([jank#1022](https://github.com/jank-lang/jank/issues/1022)).

After each of them the session has to keep answering, and the server as
well.

### Before the Release

- CI is green and the binaries build for every platform.
- nREPL passes every check with the `fail` severity, and the matrix makes
  sense for every server in it.
- Every new check has a quirk in the fake server, `proof serve` passes
  the new checks and it has scenarios for the servers that do things
  differently.
- [Spec Changes](doc/spec-changes.md) has everything the new checks found.
- The docs cover the new checks and the release.

## Alongside 0.1.0

These don't have to wait for the release, and the release doesn't have
to wait for them either.

### The Matrix

- Publish the compatibility matrix on GitHub Pages from the
  Compatibility workflow, with the version of each server.
- Add nbb and let-go, which are easy to install in CI, and then
  shadow-cljs.

### More Clients

- [clojure-cli.repl](https://github.com/clojure/clojure-cli.repl), the
  new client of the Clojure team, which doesn't handle `need-input` as
  of 0.1.1.
- `bb repl --connect`.
- The client in `nrepl.cmdline`, which expects an exact list of statuses
  when interrupting an idle session.
- vim-iced and rebel-readline.

### proof serve in the Tests of Clients

- CIDER's integration tests jack in to nREPL, Babashka, Basilisp, nbb
  and shadow-cljs. A test that connects to `proof serve` instead, with a
  few `-like` servers and `byte-writes`, would catch problems with other
  servers before users run into them.
- neat, mezcaml and neorepl should do the same.

### Upstream

- The open pull requests based on proof's findings:
  [spec.nrepl.org#6-#9](https://github.com/nrepl/spec.nrepl.org/pulls),
  [nrepl-beam#1](https://github.com/nrepl/nrepl-beam/pull/1) and
  [#2](https://github.com/nrepl/nrepl-beam/pull/2),
  [neat#2](https://github.com/nrepl/neat/pull/2) and
  [mezcaml#2](https://github.com/nrepl/mezcaml/pull/2).
- Issues for the spec about the rest of [Spec Changes](doc/spec-changes.md),
  once the spec pull requests are settled.
- nREPL: a release with
  [nrepl#498](https://github.com/nrepl/nrepl/pull/498), then
  [nrepl#477](https://github.com/nrepl/nrepl/issues/477) and
  [nrepl#183](https://github.com/nrepl/nrepl/issues/183), which has the
  same cause as #498.
- CIDER and Calva accepting `ops` as a list, after which
  `describe.ops-dict` can allow both formats.
- Issues for what the matrix finds in Basilisp and ClojureCLR, a few at
  a time. jank's can go in a reply to jank#1104.

## Further Down the Road

- Incorporating [Spec Changes](doc/spec-changes.md) into the spec, so
  that the spec and proof agree and proof can eventually be versioned
  together with the spec.
