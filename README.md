# proof

[![CI](https://github.com/nrepl/proof/actions/workflows/ci.yml/badge.svg)](https://github.com/nrepl/proof/actions/workflows/ci.yml)

> the nREPL compatibility suite

proof checks whether an nREPL server will actually work with the clients
people use (CIDER, Calva, Conjure, vim-fireplace, REPLy and so on). It
talks to the server over a socket like any client would, runs a set of
checks against it and tells you what's broken and which clients it breaks.

The [nREPL protocol spec](https://spec.nrepl.org) is still a draft and in
a few places it disagrees with what clients actually do. When that
happens proof sides with the clients. After all, a server that follows
the spec to the letter, but that CIDER can't connect to, is not much use
to anyone. All such differences are tracked in
[doc/spec-changes.md](doc/spec-changes.md), so the spec can catch up
eventually.

## Quick Start

proof is a single binary. You can install it with Go 1.23 or newer:

```shell
$ go install github.com/nrepl/proof/cmd/proof@latest
```

Next you'll need a profile for your server. That's a small TOML file that
tells proof how to start the server and gives it a few snippets of code in
the server's language:

```toml
name = "My server"

[launch]
command = ["my-server", "--port", "0"]
port-pattern = 'nREPL server started on port (\d+)'

[snippets.value]
code = "(+ 1 2)"
value = "3"
```

The [profiles](profiles) folder has complete profiles for nREPL itself,
Babashka, Basilisp, ClojureCLR, jank, dialtone (Erlang) and repartee
(Elixir), which you can use as a starting point.

Now you can run the checks:

```shell
$ proof run my-server.toml
```

proof will start your server, run the checks against it and shut it down
afterwards. For every check that didn't pass you'll see what happened,
which clients are affected and links to the client code in question:

```
session
  PASS   session.clone                clone returns a new session
  PASS   session.persistent           Definitions persist within a session
  FAIL   session.isolated             Sessions don't share state
         a fresh session sees state set in another one: "*1" gave [":proof-marker"]
         why: CIDER keeps a separate tooling session so its own evals don't clobber the user's *1, *2 and friends; shared state corrupts them.
         see: CIDER clones a main and a tooling session on connect https://github.com/clojure-emacs/cider/blob/9e049baa.../lisp/nrepl-client.el#L740-L760
```

A failure always means that some client won't work properly with your
server (or that users will lose data). If your server simply does
something differently from the reference nREPL implementation, you'll
get a warning instead.

## Documentation

- [Usage](doc/usage.md) - checking your server, reading the report,
  running proof in CI and comparing servers
- [Profiles](doc/profiles.md) - all the profile options, the snippets and
  known failures
- [Design](doc/design.md) - the general approach and how checks are graded
- [Hacking](doc/hacking.md) - building and testing proof, adding checks
  and servers
- [Spec Changes](doc/spec-changes.md) - gaps and disagreements found in
  the draft spec so far
- [Contributing](CONTRIBUTING.md)

## Status

proof is still in its early days. Right now it covers the core of the
protocol (`describe`, unknown ops, `eval`, sessions, `stdin` and the wire format),
and `proof list` will show you all the checks.

Here's what's coming next:

- checks for `interrupt`, `load-file`, `completions` and `lookup`
- robustness checks (malformed messages, fields of the wrong type,
  clients disconnecting in the middle of an evaluation)
- replaying what real clients send (e.g. when CIDER or Calva connect to a
  server) as client profiles
- publishing the compatibility matrix somewhere nicer than a CI job
  summary

## Why "proof"?

Many nREPL clients are named after drinks (CIDER, neat, mezcaml, chaser,
etc). proof is not a client, so it bends the rule a bit, but it still
belongs at the bar. The proof of a spirit is a measure of its strength,
which is more or less what this tool measures for nREPL servers. And
running it gives you proof that your server works with the existing
clients (or a list of the reasons why it doesn't). :-)

## License

Copyright © 2026 Bozhidar Batsov and contributors.

Distributed under the Apache License 2.0. See [LICENSE](LICENSE).
