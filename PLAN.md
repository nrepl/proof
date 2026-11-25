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

The first release, which came out on 2026-10-26. Server authors can
run a fixed version of proof in their CI now (jank is already looking
into this, see [jank#1104](https://github.com/jank-lang/jank/issues/1104)),
and it covers the rest of what every editor sends: `interrupt`,
`load-file`, lookups and completions, plus requests that go wrong.

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
- Done: a GitHub Action, so a server's workflow needs nothing but `uses:
  nrepl/proof@v0.1.0` and the path to its profile.
- Done: the CI section of [Usage](doc/usage.md) uses the release instead
  of `@latest`.

### interrupt

- Done: checks for interrupting a running eval, with and without
  `interrupt-id` (Conjure never sends one), the `interrupted` status, an
  idle session, an `interrupt-id` that doesn't match and the session
  working afterwards.
- Done: the evals waiting behind an interrupted one, which nREPL 1.8.0
  and Babashka drop
  ([nrepl#503](https://github.com/nrepl/nrepl/issues/503)), and where an
  interrupted eval's `done` goes when the interrupt comes from another
  connection, which nREPL's main branch gets right now
  ([nrepl#498](https://github.com/nrepl/nrepl/pull/498)). The exception
  of an interrupted eval, which nREPL sends after its `done`
  ([nrepl#506](https://github.com/nrepl/nrepl/issues/506)), is
  `wire.after-done`'s warning.
- Done: `proof serve` interrupts like dialtone and repartee do, and has
  nREPL's bugs as scenarios (`late-exception`, `drop-queued` and
  `interrupt-elsewhere`), which `-like clojure` and `-like babashka`
  turn on.
- Not done: an interrupt sent right after its eval, which can find
  nREPL's session idle
  ([nrepl#504](https://github.com/nrepl/nrepl/issues/504)). That's a
  race, so a check can't count on hitting it.
- Not needed: a step option for client profiles that sends a request
  without waiting for its reply. The `interrupt` requests of CIDER,
  Calva and vim-fireplace are plain enough for `interrupt.running`, and
  Conjure's for `interrupt.no-id`.

### load-file

- Done: checks for the value of `load-file` and for a load that fails,
  where CIDER needs `eval-error`, Calva `ex` and Conjure `err`, plus what
  CIDER, Calva and Conjure send to load a file. The spec and the servers
  don't agree on which field has the contents of the file
  ([spec#3](https://github.com/nrepl/spec.nrepl.org/issues/3)), but every
  server in the matrix takes `file` and passes them all.

### Lookups and Completions

- Done: checks for nREPL's `completions` and `lookup` and cider-nrepl's
  `complete`, `info` and `eldoc`, for servers that list them, sending
  CIDER's `prefix` and `sym` and the `symbol` of Calva, Conjure and
  vim-fireplace. The checks of `completions` and `lookup` skip on
  servers that have `complete` and `info`, as clients use those then.
  Babashka, Basilisp and ClojureCLR don't say `no-info` for a symbol
  that doesn't exist, Basilisp counts columns from 0 and ClojureCLR's
  `file` isn't a URI, which Calva needs to jump to a definition.
- Done: `proof serve` has nREPL's `completions` and `lookup`.
- Not done: scenarios for cider-nrepl's ops in `proof serve` (see
  [After 0.3.0](#after-030)).
- [spec#9](https://github.com/nrepl/spec.nrepl.org/pull/9) (`arglists-str`
  and `no-info`) should be settled by then.

### Requests That Go Wrong

- Done: frames that aren't requests (the server has to keep taking
  connections), fields of the wrong type
  ([nrepl#477](https://github.com/nrepl/nrepl/issues/477)), a client that
  goes away in the middle of an eval
  ([babashka#2227](https://github.com/babashka/babashka/issues/2227)) and
  an eval in one session holding up the others
  ([jank#1022](https://github.com/jank-lang/jank/issues/1022)). A bencode
  string takes jank down, and after a client goes away in the middle of
  an eval, nREPL (1.8.0 and its main branch) never sends the `done` of
  the next eval in that session.
- Not done: an eval whose reply bencode can't carry, e.g. a boolean
  ([babashka#2201](https://github.com/babashka/babashka/issues/2201)), as
  there's no telling which code makes a server send one.

### Before the Release

- CI is green and the binaries build for every platform.
- nREPL passes every check with the `fail` severity, and the matrix makes
  sense for every server in it.
- Every new check has a quirk in the fake server, `proof serve` passes
  the new checks and it has scenarios for the servers that do things
  differently.
- [Spec Changes](doc/spec-changes.md) has everything the new checks found.
- The docs cover the new checks and the release.

## 0.2.0

The second release, which came out on 2026-11-03. The compatibility
matrix has a page of its own now, with nbb and let-go in it, and there
are client profiles for clojure-cli.repl and `bb repl --connect`.

### The Matrix

- Done: the Compatibility workflow publishes the matrix on
  [GitHub Pages](https://nrepl.github.io/proof/), with the version of
  each server, which comes from the new `version` snippet. `proof matrix
  -html` builds the same page out of any reports.
- Done: nbb and let-go, and scenarios for what they do differently in
  `proof serve`. Neither says `eval-error` when the code throws or sends
  what it prints to stderr, nbb stops taking connections after a frame
  that isn't a dict, and let-go shares the current namespace between
  sessions and doesn't list `interrupt` in `describe` (even though it
  has it).

### More Clients

- Done: [clojure-cli.repl](https://github.com/clojure/clojure-cli.repl),
  the new client of the Clojure team. It's meant for the server that
  comes with it, but it works with others too. As of 0.2.3 it still
  doesn't answer `need-input`, so code reading input hangs, and it never
  closes its session.
- Done: `bb repl --connect`, which every server in the matrix gets
  right.

## 0.3.0

The next release. It doesn't have a date yet.

- Done: a clients tab on the matrix page, with how many of the checks
  each client depends on every server passes
  ([#23](https://github.com/nrepl/proof/pull/23)).
- Done: short names for the bundled profiles and the versions of nREPL,
  ClojureCLR and jank in the matrix
  ([#22](https://github.com/nrepl/proof/pull/22)).
- Done: `session.persistent` evaluates something else between a
  definition and its use, the profile of repartee checks for a value per
  expression, and `-like dialtone` and `-like repartee` act like nREPL,
  as [nrepl-beam 0.2.0](https://github.com/nrepl/nrepl-beam/releases/tag/v0.2.0)
  fixed what their scenarios were about
  ([#24](https://github.com/nrepl/proof/pull/24)).

## After 0.3.0

This is what's next for proof.

### The Matrix

- shadow-cljs.
- The next release of nREPL. Its main branch closes the sessions of a
  connection that goes away
  ([nrepl#512](https://github.com/nrepl/nrepl/pull/512)) and stops an
  eval at the first form that throws
  ([nrepl#517](https://github.com/nrepl/nrepl/pull/517)). Both change
  what clients can count on, so the `clojure` profile, `proof serve` and
  [Spec Changes](doc/spec-changes.md) need another pass when it's out.

### More Clients

- The client in `nrepl.cmdline`, which expects an exact list of statuses
  when interrupting an idle session.
- [elin](https://github.com/liquidz/elin), which replaced vim-iced (its
  repository is archived), and rebel-readline.

### proof serve in the Tests of Clients

- CIDER's integration tests jack in to nREPL, Babashka, Basilisp, nbb
  and shadow-cljs. A test that connects to `proof serve` instead, with a
  few `-like` servers and `byte-writes`, would catch problems with other
  servers before users run into them.
- neat, mezcaml and neorepl should do the same.
- Scenarios for cider-nrepl's ops, so `-like` can act like Babashka,
  Basilisp and ClojureCLR there too.

### Upstream

- The pull requests for the spec based on proof's findings,
  [spec.nrepl.org#6-#9](https://github.com/nrepl/spec.nrepl.org/pulls),
  are still open. The fixes for nrepl-beam, neat and mezcaml are all
  merged.
- Issues for the spec about the rest of [Spec Changes](doc/spec-changes.md),
  once the spec pull requests are settled.
- nREPL: a release with
  [nrepl#498](https://github.com/nrepl/nrepl/pull/498). Still open are
  [nrepl#477](https://github.com/nrepl/nrepl/issues/477),
  [nrepl#183](https://github.com/nrepl/nrepl/issues/183) (which has the
  same cause as #498),
  [nrepl#503](https://github.com/nrepl/nrepl/issues/503),
  [nrepl#504](https://github.com/nrepl/nrepl/issues/504) and
  [nrepl#506](https://github.com/nrepl/nrepl/issues/506).
  [nrepl#509](https://github.com/nrepl/nrepl/issues/509) is closed, so
  `robust.disconnect` is worth another run against the main branch.
  Babashka fails that check in a full run too, but not on its own.
- CIDER and Calva accepting `ops` as a list, after which
  `describe.ops-dict` can allow both formats.
- The issues reported on 2026-10-29 are all closed:
  [basilisp#1356](https://github.com/basilisp-lang/basilisp/issues/1356)
  (no `no-info` for unknown symbols),
  [basilisp#1357](https://github.com/basilisp-lang/basilisp/issues/1357)
  (columns from 0),
  [jank#1169](https://github.com/jank-lang/jank/issues/1169) (a request
  that throws stops its connection from answering),
  [conjure#802](https://github.com/Olical/conjure/issues/802) (no
  `interrupt-id` in `interrupt`) and
  [cider#4224](https://github.com/clojure-emacs/cider/issues/4224) (async
  eldoc ignores `no-eldoc`). The expected failures in the profiles go
  away as the fixes get released.
- Not reported yet: nbb stops taking connections after a frame that isn't
  a dict, nbb and let-go don't say `eval-error` or send what's printed to
  stderr, let-go's `interrupt` doesn't stop an eval and its sessions
  share the current namespace, and clojure-cli.repl doesn't answer
  `need-input` or close its session.
- No answer so far on
  [reply#218](https://github.com/trptcolin/reply/issues/218),
  [reply#219](https://github.com/trptcolin/reply/issues/219) and
  [vim-fireplace#429](https://github.com/tpope/vim-fireplace/issues/429).
- ClojureCLR doesn't say `no-info` for unknown symbols either, but
  clr.tools.nrepl takes bug reports on Ask Clojure rather than GitHub.
- Calva waits for an eval of `java.version` in the main session before
  it interrupts, so it looks like it can't interrupt an eval there. That
  needs a run in VS Code before it gets reported.

## Further Down the Road

- Incorporating [Spec Changes](doc/spec-changes.md) into the spec, so
  that the spec and proof agree and proof can eventually be versioned
  together with the spec.
