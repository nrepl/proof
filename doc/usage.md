# Usage

This section of the documentation covers everything you need to check an
nREPL server with proof, from installing proof to running it in your
server's CI. The details of the profile format are covered separately in
[Profiles](profiles.md).

## Installation

proof is a single Go binary, so installing it is a very simple process.
With Go 1.23 or newer you can do:

```shell
$ go install github.com/nrepl/proof/cmd/proof@latest
```

That puts `proof` in `$(go env GOPATH)/bin`.

> [!NOTE]
> There are no tagged releases yet, so `@latest` is simply whatever is on
> `main`. If you always need the same build (e.g. in CI), you can install a
> specific commit with `@<commit-sha>`.

Alternatively you can build proof from source:

```shell
$ git clone https://github.com/nrepl/proof
$ cd proof
$ go build -o bin/proof ./cmd/proof
```

## Checking Your Server

proof needs a profile for your server. That's a small TOML file that
explains how to start the server and provides a few snippets of code in
its language. The [profiles](../profiles) folder has profiles for nREPL
itself, Babashka, Basilisp, ClojureCLR, jank, dialtone (Erlang) and
repartee (Elixir), and it's a good idea to start from the one that's
closest to your server. If you're working on a Clojure dialect you can
probably use `clojure.toml` almost as is, otherwise `dialtone.toml` is a
better starting point.

Here's a minimal profile:

```toml
name = "My server"

[launch]
command = ["my-server", "--port", "0"]
port-pattern = 'nREPL server started on port (\d+)'

[snippets.value]
code = "(+ 1 2)"
value = "3"
```

proof will start the server in a temporary directory, wait for its
output to match `port-pattern`, connect to it, run the checks and finally
shut it down:

```shell
$ proof run my-server.toml
```

You can also start the server yourself (e.g. if it takes a long time to
start or you want to run it in a debugger) and point proof to it. The
profile is still needed for the snippets:

```shell
$ proof run -address localhost:7888 my-server.toml
```

With the minimal profile above proof can only run the checks that need
the `value` snippet. All the others will be skipped and the report will
tell you which snippet they were missing. You'll need to add the rest of
the [snippets](profiles.md#snippets) to get the full picture.

## Reading the Report

The report groups the checks by area, with one line per check:

```
eval
  PASS   eval.value                   eval returns the value
  FAIL   eval.multiple-forms          Each form in the code gets its own value
         evaluating "1 2" gave values ["2"], want ["1" "2"]
         why: Clients show one result per value message; a server that only returns the last one loses the rest.
         see: babashka/nbb#294 https://github.com/babashka/nbb/issues/294
         see: nrepl/nrepl#147 https://github.com/nrepl/nrepl/issues/147
```

Each check gets one of the following verdicts:

| Verdict | Meaning |
|---|---|
| `PASS` | Everything is fine. |
| `FAIL` | Something a client depends on is broken, or users will lose data. The `why` line explains which clients are affected and the `see` lines link to the relevant client code (pinned to a specific commit), the reference implementation or the GitHub issue the check is based on. |
| `WARN` | Your server behaves differently from the reference nREPL implementation or the spec, but no client we know of depends on this behavior. It's a good idea to fix it, but it's not a big deal. |
| `SKIP` | The profile doesn't provide something the check needs, or the check depends on another check that failed (there's no point in checking evaluation in a session if `clone` is broken). |
| `ERROR` | proof couldn't run the check at all, most likely because it couldn't connect to the server. This doesn't say anything about your server, unless the server died. In this case proof will show its output at the end of the report. |

The `wire.*` checks at the end of the report work a bit differently from
the rest. Instead of sending requests to the server, they look at all the
messages the server sent during the run. This way they can catch problems
like a missing `session` field or a duplicate `done` no matter which
check triggered them. If the same problem shows up many times, it's
reported only once (along with a count).

If you run proof with `-v` you'll also see some notes about details that
are left to the implementation (e.g. the format of `ex`) and the full
message exchange for every check that didn't pass:

```shell
$ proof run -v -only 'session.close' my-server.toml
```

```
  FAIL   session.close                close replies with session-closed
         close reply status is [done], with no session-closed
         ...
         connection 1:
           ->      0ms {id "proof-1-1", op "clone"}
           <-      2ms {id "proof-1-1", new-session "8c1e...", status ["done"]}
           ->      2ms {id "proof-1-2", op "close", session "8c1e..."}
           <-      3ms {id "proof-1-2", session "8c1e...", status ["done"]}
```

Most of the time the message exchange is all you need to figure out
what went wrong.

## Command-line Options

Here are the options supported by `proof run`:

| Option | Description |
|---|---|
| `-address host:port` | Connect to a running server instead of starting one. |
| `-only pattern` | Run only the checks whose ID matches a pattern (e.g. `'eval.*'`). The `wire.*` checks always run, but they only see the messages from the selected checks. |
| `-v` | Show notes and the message exchange for all checks that didn't pass. |
| `-json file` | Write a JSON report as well. |

`proof run` uses the following exit codes:

| Code | Meaning |
|---|---|
| 0 | All checks passed, or failed only where the profile expected them to. |
| 1 | Some checks failed (or an expected failure started passing). |
| 2 | proof couldn't run (e.g. the options or the profile are invalid, or the server didn't start). |
| 3 | Some checks couldn't run at all. Usually this means that the server died during the run. |

There are a couple of other commands as well. `proof list` shows all
the checks along with their severity and `proof version` shows the
version of proof.

## Running proof in CI

Once your server passes the checks it can pass, it's a good idea to run
proof in your CI to catch regressions. If there are some failures you
can't fix yet, you can list them in the profile, so they won't fail your
builds:

```toml
[expected-failures]
"eval.multiple-forms" = "only the last value is returned, see #42"
```

Expected failures still show up in the report. When one of them starts
passing, proof will fail the run and ask you to remove it from the list.
See [Profiles](profiles.md#expected-failures) for more details.

Here's an example GitHub Actions job:

```yaml
proof:
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v7
    # set up and build your server here
    - uses: actions/setup-go@v7
      with:
        go-version: stable
    - run: go install github.com/nrepl/proof/cmd/proof@latest
    - run: proof run -v test/proof.toml
```

## Comparing Servers

`proof matrix` can combine several JSON reports into a Markdown table
with one row per check and one column per server:

```shell
$ proof run -json clojure.json profiles/clojure.toml
$ proof run -json my-server.json my-server.toml
$ proof matrix clojure.json my-server.json > matrix.md
```

The columns appear in the order of the reports on the command line, so
you'll probably want to put the reference implementation first.

> [!TIP]
> proof's own CI builds such a matrix for all the profiles in the
> repository on a daily basis. You can find it in the summary of the
> Compatibility workflow on the
> [Actions tab](https://github.com/nrepl/proof/actions).

## Troubleshooting

This section lists the most common problems you may encounter while
setting up a profile and their solutions.

### No Line Matched the Port Pattern

proof shows everything the server printed while starting, so you can
compare it against your `port-pattern`.

- Make sure the server prints the port it actually bound and not the one
  it was asked for. That's an easy mistake to make when a server is
  started on port 0.
- Slow servers (e.g. JVM ones that download their dependencies on the
  first run) might need a bigger `startup-timeout`.

### The Server Exits Right Away

proof keeps the stdin of the server open, as servers that come with a
terminal REPL tend to exit when stdin is closed. If your server still
exits, try running its command manually from an empty directory, as proof
runs each server in a new temporary directory.

### Every Check Fails with "couldn't connect"

The server may be listening on a different interface than the one proof
is connecting to. proof uses `localhost` unless the profile sets
`launch.host`, so for a server that binds only `127.0.0.1` or `::1` you
might have to set it explicitly.

### Values Don't Match

proof compares the values as strings, so the `value` in a snippet has to
be exactly what your server returns, quotes and all (e.g. in Clojure a
string comes back as `"\"hello\""`). Run proof with `-v` to see the
message exchange.

### Requests Time Out

By default proof waits 10 seconds for each request to finish. If your
server is just slow, you can increase `timeout` in the profile. If a
simple request times out, though, most likely the server never sent
`done` for it, which is exactly the kind of problem proof is supposed to
catch.
