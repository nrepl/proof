# proof

> the nREPL compatibility suite

proof checks whether an nREPL server will actually work with the clients
people use: CIDER, Calva, Conjure, vim-fireplace, REPLy and friends. It
talks to the server over a socket like any client would, runs a set of
checks, and tells you what's broken and who it breaks for.

The [draft spec](https://spec.nrepl.org) is thin, and where it disagrees
with what clients do, the clients win. A server that follows the spec to
the letter but that CIDER can't connect to isn't much use to anyone. The
places where the two diverge are collected in
[doc/spec-changes.md](doc/spec-changes.md), so the spec can catch up.

## Usage

You'll need Go 1.23 or newer to build it:

```
go build -o bin/proof ./cmd/proof
bin/proof run profiles/babashka.toml
```

A profile says how to start the server and gives the bits of code the
checks evaluate, since every server speaks a different language. proof
starts the server in a scratch directory, waits for its port, runs the
checks and shuts it down again. To check a server that's already running,
point proof at it instead:

```
bin/proof run -address localhost:7888 profiles/babashka.toml
```

Other useful flags:

- `-v` shows notes and the full message transcript for anything that
  didn't pass.
- `-only 'eval.*'` runs a subset of the checks.
- `-json report.json` writes a machine-readable report as well.

`proof list` prints every check. The exit status is 1 when the server
failed something, so it drops straight into CI. 2 means proof couldn't get
going (a bad profile, or the server never came up) and 3 means some checks
couldn't run at all, which usually means the server died halfway through.

To compare servers, write a JSON report for each and build a Markdown
matrix out of them:

```
bin/proof run -json clojure.json profiles/clojure.toml
bin/proof run -json babashka.json profiles/babashka.toml
bin/proof matrix clojure.json babashka.json
```

## How checks are graded

Every check is one rule, and the grading follows a simple principle:

- **fail**: some named client breaks, or users lose data. The check cites
  the client code that depends on the behaviour.
- **warn**: the server differs from the reference implementation or the
  spec, but no client we know of depends on it.
- **skip**: the profile doesn't provide what the check needs.
- Notes cover implementation-defined things (like the format of `ex`) and
  never affect the verdict.

There are two kinds of checks. Most send requests and look at the replies.
The `wire.*` rules watch every message exchanged during the run, whatever
check produced it. That's how problems like a missing `session` or a second
`done` get caught wherever they happen to turn up.

When a check fails, the report says why and links to the code that
breaks, pinned to a commit:

```
FAIL   session.close                close replies with session-closed
       close reply status is [done], with no session-closed
       why: vim-fireplace only forgets a session when it sees session-closed, ...
       see: vim-fireplace session-closed handling https://github.com/tpope/vim-fireplace/blob/...
```

## Writing a profile

Profiles are TOML. Here's a trimmed down one:

```toml
name = "Babashka"
homepage = "https://github.com/babashka/babashka.nrepl"

[launch]
command = ["bb", "nrepl-server", "localhost:0"]
port-pattern = 'Started nREPL server at [^:]+:(\d+)'
startup-timeout = "30s"

[capabilities]
namespaces = true

[snippets.value]
code = "(+ 1 2)"
value = "3"
```

`command` gets environment variables expanded, and a relative program path
is resolved against the profile's directory. `port-pattern` is matched
against the server's output; its first group (or one named `port`) is the
port. Start the server on port 0 if you can, so runs don't fight over
ports. There's also `timeout` for individual requests (10s by default),
`launch.host` (localhost) and `launch.env`.

The snippets, and what they should do:

| Snippet         | Fields                 | Should                                                |
|-----------------|------------------------|-------------------------------------------------------|
| `value`         | `code`, `value`        | evaluate to `value`                                   |
| `stdout`        | `code`, `out`          | print exactly `out` to standard output                |
| `stderr`        | `code`, `err`          | print exactly `err` to standard error                 |
| `throw`         | `code`                 | raise an error                                        |
| `define`        | `code`, `use`, `value` | define something that `use` reads back as `value`     |
| `session-state` | `code`, `use`, `value` | leave session-local state that `use` reads as `value` |
| `multiple`      | `code`, `values`       | be several forms, evaluating to `values` in order     |

Leave a snippet out and the checks needing it are skipped. The only
capability so far is `namespaces`: set it if your language has a notion of a
current namespace, so the `ns` checks apply.

### Known failures

Running proof in your server's CI is more useful once it only complains
about new problems. List the failures you know about, with a reason, and
they stop failing the run:

```toml
[expected-failures]
"eval.multiple-forms" = "only the last value is returned, see #42"
```

They still show up in the report, marked as expected. When one starts
passing, proof fails the run and tells you to remove it, so the list can't
quietly go stale.

The [profiles](profiles) directory has profiles for nREPL itself, Babashka,
Basilisp, ClojureCLR, jank and dialtone (Erlang). The comment at the top of
each says what it needs installed.

## Status

Early days. What's here covers the core of the protocol: `describe`,
unknown ops, `eval`, sessions and the wire format. Still to come:

- `stdin`, `interrupt`, `load-file`, `completions` and `lookup`
- robustness: malformed frames, wrong field types, clients disconnecting
  mid-eval
- replaying the traffic of real clients (CIDER's connect sequence, Calva's
  handshake) as client profiles
- publishing the compatibility matrix somewhere nicer than a CI job summary

## Why "proof"?

nREPL clients tend to be named after drinks: CIDER, neat, mezcaml, chaser.
proof isn't a client, so it bends the rule a little, but it still belongs
at the bar. A spirit's proof is the measure of its strength, and that's
more or less what this does to a server. It's also the whole point of the
tool: run it and you get proof that your server works with the clients
out there. Or a list of reasons it doesn't.

## License

Copyright © 2026 Bozhidar Batsov and contributors.

Distributed under the Apache License 2.0. See [LICENSE](LICENSE).
