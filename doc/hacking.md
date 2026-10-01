# Hacking on proof

This section is dedicated to people who'd like to hack on proof itself
(e.g. to add new checks or fix bugs). It's the practical companion of
[Design](design.md), which explains the reasoning behind the overall
approach.

## Building proof

Building proof locally is a very simple process:

1. Clone the repo
2. Make sure you have Go 1.23 or newer installed
3. Build it and run the tests:

```shell
$ go build -o bin/proof ./cmd/proof
$ go test ./...
```

The tests don't need any nREPL servers, as the checks are tested against
a fake server written in Go (more on this later), so they run anywhere in
a few seconds.

To try proof against real servers you'll need some of them installed.
Babashka is the easiest one to get going with (`bb` is a single binary
that starts instantly). nREPL itself is the most important one, as it's
the reference everything else gets compared to. You'll need the
[Clojure CLI](https://clojure.org/guides/install_clojure) for it. The
comment at the top of each profile in the [profiles](../profiles) folder
says what you need to install for it.

```shell
$ bin/proof run profiles/babashka.toml
$ bin/proof run profiles/clojure.toml
```

## Codebase Overview

| Path | Contents |
|---|---|
| `cmd/proof` | The command-line interface. |
| `bencode` | The strict bencode implementation. It distinguishes between messages that can't be decoded and messages that are only not entirely valid. |
| `nrepl` | An nREPL client that records all the messages it sends and receives. |
| `internal/profile` | Loading and validating profiles. |
| `internal/server` | Starting servers and figuring out their ports. |
| `internal/check` | The checks framework (`Check`, `T`, `Rule`), grading and expected failures. It doesn't know anything about specific ops. |
| `internal/checks` | The checks (`describe.go`, `op.go`, `session.go` and `eval.go`), the wire checks (`wire.go`), the links to client code (`refs.go`) and the fake server used to test all of them (`fake_test.go`). |
| `internal/report` | Text and JSON reports and the compatibility matrix. |
| `profiles` | The profiles for the servers in the compatibility matrix. |
| `doc/spec-changes.md` | All the gaps and disagreements found in the draft spec. |

## Testing

All packages have unit tests, but the most interesting ones are in
`internal/checks`. `fake_test.go` contains a small nREPL server that can
be configured to misbehave in many different ways (see `quirks`), and
`checks_test.go` runs all the checks against it:

- When no quirks are enabled, all checks must pass. This verifies that
  the checks don't complain about a server that does everything right.
- Each quirk has an entry in a table that lists the checks it should
  make fail, and all the other checks must still pass. This verifies that
  each check catches what it's supposed to catch and nothing else.

In other words - every check needs a quirk that makes it fail. Otherwise
we have no evidence that the check can fail at all.

Before submitting any changes make sure the code is formatted properly
and the tests pass with the race detector enabled:

```shell
$ gofmt -l .
$ go vet ./...
$ go test -race ./...
```

The fake server only shows that the checks are consistent with
themselves, though. They also have to be right about real servers, so you
should run new (or updated) checks against the reference implementation
(`profiles/clojure.toml`) and ideally against a couple of other servers.
nREPL must pass all checks with the `fail` severity. If it doesn't,
either the check is wrong or there's a bug in nREPL, and you should
figure out which before going any further.

## Adding a Check

Let's take a look at `eval.stderr`:

```go
{
	ID:       "eval.stderr",
	Title:    "Error output arrives as err",
	Severity: check.Fail,
	Why:      "Clients show err as the user's error output; anything missing is lost.",
	Refs:     []check.Ref{specEval, issue("babashka/babashka.nrepl", 28)},
	Snippets: []string{"stderr"},
	Requires: []string{"session.clone"},
	Run: func(t *check.T) {
		s, resp := evalSnippet(t, "stderr")
		if got := resp.Err(); got != s.Err {
			t.Violatef("evaluating %q produced err %q, want %q", s.Code, got, s.Err)
		}
	},
},
```

Here's what each of its fields means:

| Field | Description |
|---|---|
| `ID` | Has the form `area.name`. The area determines the group the check appears in within the report. Profiles refer to checks by their IDs in `expected-failures`, so you should avoid renaming checks. |
| `Title` | Describes what a server should do. It should be true when the check passes. |
| `Severity` | Follows the [grading rule](design.md#how-checks-are-graded) - use `check.Fail` only when some client breaks or users lose data, and `check.Warn` otherwise. If you can't name the affected client, it's a warning. |
| `Why` | A sentence explaining who's affected and how. It appears next to every failure, so write it for a server author who has never heard of the problem. |
| `Refs` | The evidence for the check. A check that fails needs at least one link to client code that depends on the behavior in question. The links live in `refs.go` and are pinned to specific commits (see [Links to Client Code](#links-to-client-code)). |
| `Snippets`, `Needs` | The snippets and capabilities the check uses. Checks are skipped when the profile doesn't provide them. `t.Snippet` panics if a check uses a snippet that it didn't declare, which the tests catch, so the two can't get out of sync. |
| `Requires` | The checks this check depends on. Everything that creates a session requires `session.clone`, and everything that looks at the response of `describe` requires `describe.reply`. |

Checks are grouped by area in `describe.go`, `op.go`, `session.go` and
`eval.go`, and they run in the order listed in `all.go`. If you're adding
a new area, put its checks in a new file and add them to `all.go`.

In `Run` a check talks to the server via `t`:

| Function | Description |
|---|---|
| `t.Connect()` | Opens a new connection. |
| `t.Session(c)` | Creates a session and returns it as request fields. Skips the check if `clone` doesn't work. |
| `t.Request(c, msg)` | Sends a request and waits for its `done`. If there's no `done`, the check fails right away. |
| `t.Eval(c, code, fields)` | Sends an `eval` request with some additional fields (e.g. the session). |
| `evalSnippet(t, name)` | Evaluates a snippet in a new session on a new connection. Most eval checks use this. |
| `t.Snippet(name)` | Returns one of the snippets the check declared. |
| `t.Violatef(...)` | Records a violation of the rule and continues. |
| `t.Stopf(...)` | Records a violation of the rule and stops the check. |
| `t.Notef(...)` | Records a note that doesn't affect the verdict. |
| `t.Skipf(...)` | Stops the check without a verdict. |

Responses are returned as `nrepl.Response`, which has helpers like
`Values()`, `Out()`, `Err()`, `Status()`, `HasStatus(...)` and `Str(key)`.

After you've written the check, you'll need to teach the fake server to
misbehave in a way the check should catch. Add a quirk to `quirks` in
`fake_test.go` and use it where the fake server builds its response (for
`eval.stderr` that's `dropErr`, which skips sending `err`). Then add an
entry for it to the table in `checks_test.go`:

```go
{"stderr dropped", quirks{dropErr: true}, map[string]check.Verdict{"eval.stderr": F}},
```

If the quirk makes other checks fail as well, the test will tell you
about it. If it's really the same problem showing up in several places,
list the other checks too. Otherwise make the quirk more specific.

If your check needs a new kind of snippet, document it in the snippets
table in [Profiles](profiles.md#snippets) and add it to all the profiles
in the `profiles` folder.

Finally, if the spec doesn't say anything about the behavior you're
checking (or it says something else), add a note about it to
[Spec Changes](spec-changes.md).

## Adding a Wire Check

The wire checks live in `wire.go` (in the code they're called rules, see
`check.Rule`). The `Inspect` function of a wire check gets the messages
of one connection at a time and reports any problems it finds:

```go
Inspect: eachMessage(func(m nrepl.Message, report check.Reporter) {
	if m.HasStatus("need-input") && len(m.Status()) != 1 {
		report("need-input arrived with other flags", m.String())
	}
}),
```

`report` takes a description of the problem and an example of it.
Problems with the same description are counted together and only the
first example is kept, so the description shouldn't contain anything that
changes from message to message (e.g. IDs or values). Such details belong
in the example.

There are a few helpers for going over the messages of a connection:

- `eachReceived` - all received messages, including ones that couldn't be
  decoded
- `eachMessage` - all received messages that were decoded to a dictionary
- `eachReply` - all received messages along with the request their `id`
  refers to, and whether an earlier message for the same request already
  had `done` or an error status

Just like the regular checks, every wire check needs a quirk and an entry
in the table in `checks_test.go`.

## Adding a Server

To add a server to the compatibility matrix:

1. Create `profiles/<server>.toml`. Start it with a comment explaining
   what needs to be installed. [Profiles](profiles.md) covers the format.
2. Run it locally and make sure the results make sense. It's fine for a
   server to fail checks (that's what the matrix is for), but make sure
   that it's really the server that's failing and not the profile. Running
   proof with `-v` will show you the full message exchange.
3. Add the server to `.github/workflows/compat.yml` - add it to the list
   of profiles and add the steps that install it. Try to install it the
   same way a user would.
4. Add it to the lists of profiles in the [README](../README.md) and in
   [Usage](usage.md).

## Links to Client Code

The links to the code of clients and nREPL live in `refs.go`. Each
project has a base URL that's pinned to a specific commit, and each link
combines a base with a path and a range of lines:

```go
ciderBase = "https://github.com/clojure-emacs/cider/blob/9e049baa1c2c136724d7538b1df6898ee77de6e7/lisp/"
ciderEvalError = ref("CIDER eval-error handling", ciderBase+"nrepl-client.el#L898-L899")
```

When you need to link to something new, find the relevant code at a
recent commit, link to it using the full SHA of the commit and point to
the lines that actually depend on the behavior in question. If you
update the base of a project to a newer commit, make sure that all the
links using it still point to the right lines.

## Coding Conventions

- The code should always be formatted with `gofmt` and pass `go vet`.
- Comments and messages should be written in plain English for the
  people who'll read them later. Don't use `--` in comments or in the documentation.
- Violation messages should say what happened and what was expected,
  including the actual values (e.g. `evaluating "1 2" gave values ["2"],
  want ["1" "2"]`).
- Every change should come with tests. For checks that means a quirk in
  the fake server.

## Continuous Integration

proof uses a couple of GitHub Actions workflows:

- CI checks the formatting, runs `go vet` and the tests on Linux and
  macOS (with both the oldest supported Go version and the latest
  stable one) and makes sure proof builds on Windows.
- Compatibility runs all the profiles against their servers every day
  (and on every push to `main`) and builds the compatibility matrix in
  the job summary. As servers failing checks is perfectly normal, this
  workflow fails only when proof itself can't do its job (e.g. a server
  can't be installed or doesn't start).
