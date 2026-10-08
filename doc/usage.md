# Usage

This section of the documentation covers everything you need to check an
nREPL server with proof, from installing proof to running it in your
server's CI, and how to check and test an nREPL client with it. The details of the
profile format are covered separately in [Profiles](profiles.md).

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

The checks in the `cider` group send what CIDER sends while connecting,
evaluating code from a source buffer and evaluating code in its REPL,
with all the extra fields CIDER puts in its requests. If one of them
fails, CIDER won't work properly with your server, and the report says
what CIDER does with the reply it got (e.g. "CIDER gives up
connecting"). The code they evaluate for the user is the `value`
snippet of your profile. Before evaluating code from a source buffer,
CIDER evaluates the buffer's `ns` form, so `cider.eval` also needs the
`clojure` capability.

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
the checks along with their severity (and the scenarios and servers of
`proof serve`), and `proof version` shows the version of proof.

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

## Checking Your Client

If you're working on an nREPL client, proof can check the requests it
sends. `proof proxy` starts a server (or uses one that's already
running) and forwards everything between your client and the server,
recording every message on the way:

```shell
$ proof proxy -listen 127.0.0.1:7888 profiles/clojure.toml
Starting Clojure (nrepl/nrepl 1.7.0)...
Forwarding 127.0.0.1:7888 to localhost:53613. Connect your client to 127.0.0.1:7888 and press Ctrl-C when it's done.
```

Connect your client to this port and use it as you normally would -
evaluate some code, read some input, interrupt something. When you're
done, disconnect the client and press Ctrl-C. proof will then check
everything the client sent:

```
client
  PASS   client.id                    Every request has an id
  FAIL   client.need-input            need-input is answered with stdin in the same session
         need-input went unanswered (during connection 1): {id "2", session "dd88...", status ["need-input"]}
         why: Code reading input waits until it gets some, so an unanswered need-input leaves the eval, and the session it runs in, hanging forever.
         see: nREPL hands stdin to the reader of the request's session https://github.com/nrepl/nrepl/blob/edf294a7.../src/clojure/nrepl/middleware/session.clj#L380-L388
  WARN   client.close                 Sessions are closed before disconnecting
         a session was never closed (during connection 1): {id "1", new-session "dd88...", session "d5b1...", status ["done"]}
         ...
```

The verdicts mean the same things as for servers, only the other way
around. A failure means that some server won't work properly with your
client (or that users will lose data), and the `see` lines link to the
server code in question. A warning means that your client does something
servers tolerate, but that they shouldn't have to.

Keep in mind that proof can only see what goes over the wire. It checks
the requests your client sends, but not what your client does with the
replies. It also checks only what your client actually did - if you never
evaluate code that reads input, nobody will know how your client handles
`need-input`. And the checks about what a client leaves behind (sessions
that were never closed and input that was never sent) apply only to
connections your client closed itself while the server was still around.
A client that's still connected when you press Ctrl-C might simply not
have gotten to them yet, and one whose server went away first never got
the chance.

It's a good idea to try your client with a few servers, as they don't
all support the same ops. Any profile from the [profiles](../profiles)
folder will do, and with `-address` proof will forward your client to a
server that's already running:

```shell
$ proof proxy -address localhost:1667
```

Here are the options supported by `proof proxy`:

| Option | Description |
|---|---|
| `-address host:port` | Forward the client to a running server instead of starting one. |
| `-listen host:port` | Accept the client on this address. The default is `127.0.0.1:0`, which picks a free port. |
| `-v` | Show every message exchanged between the client and the server. |
| `-json file` | Write a JSON report as well. |
| `-record file` | Write the requests the client sent to a file, as the start of a [client profile](hacking.md#adding-a-client-profile). |

The exit codes are the same as for `proof run`, except that 1 means that
the client failed some checks and 3 means that no client sent anything.

You can also run your client's test suite through proof in CI. Start
the proxy in the background, wait for it to accept connections, run the
tests against it and stop it with `SIGINT`:

```shell
proof proxy -listen 127.0.0.1:7888 profiles/babashka.toml &
proxy=$!
until nc -z 127.0.0.1 7888; do
  kill -0 $proxy || exit 2
  sleep 1
done
# run your tests against port 7888 here
kill -INT $proxy
wait $proxy
```

`wait` returns the exit code of proof, so the step fails when your
client fails some checks. The `kill -0` makes sure the step doesn't wait
forever if proof can't start the server, and the connections `nc` makes
don't count, as they don't send anything.

## Testing Your Client Against Other Servers

The proxy checks what your client sends, but not what it does with the
replies. For that there's `proof serve`, a small nREPL server for your
client's tests. Out of the box it behaves like nREPL itself, and each
scenario you give it makes it behave like some other server in one
particular way:

```shell
$ proof serve -listen 127.0.0.1:7888 last-value no-err
Running proof serve (last-value, no-err) on 127.0.0.1:7888. Connect your client and press Ctrl-C when it's done.
```

With these two scenarios it sends only the value of the last form (like
Basilisp, jank and dialtone) and drops whatever the code prints to
stderr (like Basilisp, dialtone and repartee). `proof list` shows all
the scenarios, and every one of them is something a real server does
(or something TCP can do to the replies):

| Scenario | What changes | Who does it |
|---|---|---|
| `split-output` | Output comes one character per message | nREPL splits long output, Basilisp sends `println`'s newline on its own |
| `empty-messages` | Replies to `eval` include messages with nothing but `id` and `session` | jank |
| `last-value` | Only the value of the last form is sent | Basilisp, jank, dialtone |
| `no-err` | What the code prints to stderr never reaches the client | Basilisp, dialtone, repartee |
| `error-with-done` | `eval-error` comes in the same message as `done` | jank |
| `no-op-echo` | Replies to unknown ops don't say which op it was | ClojureCLR, Basilisp, jank, dialtone, repartee |
| `no-close-op` | `describe` doesn't list `close`, even though `close` works | jank |
| `no-interrupt` | There's no `interrupt` op | ClojureCLR, Basilisp, jank |
| `no-stdin` | There's no `stdin` op, and reading input gets `nil` right away | ClojureCLR and Basilisp started in the background, as they read their own stdin, where Basilisp gets an empty string |
| `read-line-throws` | There's no `stdin` op, and reading input throws | jank |
| `string-versions` | `versions.proof` is a plain string rather than a dict | Babashka for `versions.babashka`, ClojureCLR for `versions.clojure.tools.nrepl` |
| `no-session-closed` | `close` replies with `done` alone | Basilisp, jank, dialtone, repartee |
| `shared-state` | Sessions on the same connection share `*1`, `*e` and the current namespace | ClojureCLR, Basilisp, jank |
| `socket-sessions` | A session only exists on the connection that cloned it | ClojureCLR, Basilisp, jank |
| `any-session` | Requests for sessions that don't exist run in a new session | ClojureCLR, Basilisp, jank |
| `ns-fallback` | An eval in a namespace that doesn't exist runs in the current one | jank |
| `ns-error` | An eval in a namespace that doesn't exist fails without `namespace-not-found` | Basilisp |
| `eof-error` | Reading past the end of input fails instead of returning `nil` | nREPL 1.7.0 |
| `unsorted-keys` | The keys of reply dicts aren't sorted | jank |
| `byte-writes` | Replies are written a byte at a time | any server |
| `batched-writes` | Replies are held back and written together until the eval waits or ends | any server |
| `hang-up` | The server closes the connection instead of answering an `eval` | any server that crashes |

`-like` turns on all the scenarios of a server at once. It takes the
server's profile (e.g. `jank` or `profiles/jank.toml`), and `proof list`
shows which scenarios each server gets:

```shell
$ proof serve -listen 127.0.0.1:7888 -like jank
Running proof serve (like jank) on 127.0.0.1:7888. Connect your client and press Ctrl-C when it's done.
```

proof's own checks give `proof serve -like jank` the same results jank
gets in the compatibility matrix, and the same goes for the other
servers. There are a couple of exceptions. No scenario covers
`eval.no-code`, as clients always send some code, and the code is still
Clojure, even with `-like dialtone`. So the checks the profiles of
dialtone and repartee skip for their languages (e.g. `eval.ns`) pass.

Most scenarios change only how the replies look on the wire, not what a
user should end up seeing. Evaluating `(println "hi") (+ 1 2)` should
show `hi` and `3` with `split-output`, `byte-writes` or
`empty-messages` just like it does without them. So the easiest way to
use `proof serve` is to write your tests the way you'd check things by
hand (e.g. "this eval shows this output and this value") and run them
once for every scenario.

proof can't evaluate real Clojure, of course. Instead it understands just
enough of it for tests, and gives the same replies as nREPL 1.7.0 does
for the same code:

| Code | What it does |
|---|---|
| Integers, strings, keywords, `nil`, booleans, vectors, maps and quoted forms | Evaluate to themselves |
| `(+ 1 2)`, `-`, `*`, `/`, `inc`, `dec` | Arithmetic on integers and ratios, where `(/ 1 0)` throws |
| `(str ...)`, `(apply f ... coll)`, `(repeat n x)` | e.g. `(apply str (repeat 100000 "x"))` for a really long value |
| `(print ...)`, `(println ...)`, `(pr ...)`, `(prn ...)`, `(flush)` | Output, which `(binding [*out* *err*] ...)` turns into error output |
| `(read-line)` | Asks for input with `need-input` |
| `(throw (ex-info "message" {}))` | An `eval-error`, with the same `err` and `ex` as nREPL |
| `(Thread/sleep ms)` | Something to `interrupt` |
| `(future ...)` | Runs once the eval is done, so its output arrives after `done` |
| `(def x 1)`, `x`, `#'x`, `(resolve 'x)`, `@#'x` | Definitions, which all sessions share |
| `(ns foo)`, `(in-ns 'foo)`, `*ns*` | Namespaces |
| `*1`, `*2`, `*3`, `*e` | The last results and the last exception in the session |
| `do`, `if`, `when`, `let`, `when-let` | The usual |
| `(require ...)` | Nothing, as there's nothing to load |

Other functions get the error Clojure gives for a symbol it can't
resolve, and syntax proof doesn't read (e.g. sets or anonymous functions)
gets a read error. That's enough for CIDER to connect and work, and it's
all you need for checking output, values, errors, input and interrupts.

When you stop it, `proof serve` checks the requests your client sent,
just like `proof proxy` does, with the same report, exit codes and
`-listen`, `-json`, `-record` and `-v` options. A test suite can run it in CI like
this:

```shell
for args in "" split-output byte-writes -like=basilisp -like=jank; do
  proof serve -listen 127.0.0.1:7888 $args &
  serve=$!
  until nc -z 127.0.0.1 7888; do
    kill -0 $serve || exit 2
    sleep 1
  done
  # run your tests against port 7888 here
  kill -INT $serve
  wait $serve || exit 1
done
```

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
