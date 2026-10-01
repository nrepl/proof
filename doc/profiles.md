# Profiles

A profile tells proof how to start your server and how to talk to it in
its own language. Profiles are written in TOML, and the ones in the
[profiles](../profiles) folder are good examples. This section covers all
the options you can use in them.

Here's a complete profile for an imaginary Clojure dialect:

```toml
# Comments are a good place to say what needs to be installed.
name = "Fizz"
homepage = "https://example.com/fizz"

# Timeout for each request (optional, the default is 10s).
timeout = "10s"

[launch]
command = ["fizz", "nrepl", "--port", "0"]
port-pattern = 'nREPL server started on port (\d+)'
startup-timeout = "60s"

[launch.env]
FIZZ_HOME = "$HOME/.fizz"

[capabilities]
namespaces = true

[snippets.value]
code = "(+ 1 2)"
value = "3"

[snippets.stdout]
code = '(print "proof")'
out = "proof"

[snippets.stderr]
code = '(binding [*out* *err*] (print "proof"))'
err = "proof"

[snippets.throw]
code = '(throw (ex-info "proof" {}))'

[snippets.define]
code = "(def proof-answer 42)"
use = "proof-answer"
value = "42"

[snippets.session-state]
code = ":proof-marker"
use = "*1"
value = ":proof-marker"

[snippets.multiple]
code = "1 2"
values = ["1", "2"]

[expected-failures]
"eval.multiple-forms" = "only the last value is returned, see #42"
```

> [!NOTE]
> proof rejects profiles with unknown options, so a typo like `timout`
> will result in an error instead of being silently ignored.

## General Options

| Option | Required | Description |
|---|---|---|
| `name` | yes | The name of the server in reports and in the compatibility matrix. It's a good idea to include the version you're testing. |
| `homepage` | no | The homepage of the server. |
| `address` | no | The `host:port` of a server that's already running. When it's set proof connects to the server instead of starting it, and `[launch]` is ignored. The `-address` command-line option does the same. |
| `timeout` | no | How long proof waits for each request to finish (i.e. for its `done`). It's a duration like `"10s"` (which is also the default). |

## Starting the Server

The `[launch]` table is required, unless you specify an `address` (either
in the profile or on the command line).

| Option | Required | Description |
|---|---|---|
| `command` | yes | The command and its arguments, as a list. Environment variables (e.g. `$HOME` or `${FOO}`) are expanded. A relative path with a slash in it (e.g. `bin/server`) is resolved against the folder of the profile, while a plain command name is looked up on the `PATH`. |
| `port-pattern` | yes | A regular expression that's matched against each line the server prints (on both stdout and stderr). Its first group (or the group named `port`, if there is one) is the port. |
| `host` | no | The host to connect to. The default is `localhost`. |
| `startup-timeout` | no | How long to wait for the port. The default is `"60s"`. |
| `env` | no | Additional environment variables for the server, as a table. The values are expanded the same way as in `command`. |

Here are a few things that are good to know about how proof runs servers:

- If your server supports it, start it on port 0 and make sure it prints
  the port it actually bound. This way multiple runs will never compete
  for the same port.
- Every server runs in a new temporary directory, so files like
  `.nrepl-port` won't end up in the directory you ran proof from.
- The stdin of the server is kept open during the run, as servers that
  come with a terminal REPL (e.g. jank's) usually exit when it's closed.
- On Unix the server gets its own process group and proof stops the whole
  group at the end of the run (or when you interrupt proof). This means
  that commands that start the actual server as a child process (e.g. the
  Clojure CLI) get cleaned up properly.

## Capabilities

Some checks make sense only for some languages. The `[capabilities]`
table declares what your language supports, and checks that need a
capability your profile doesn't declare are skipped.

| Capability | When to set it | Checks that need it |
|---|---|---|
| `namespaces` | Your language has a notion of a current namespace that can be set with the `ns` field of a request (like Clojure). | `eval.ns`, `eval.unknown-ns` |

## Snippets

Snippets are the pieces of code the checks evaluate, written in the
language of your server. Each snippet is a table under `[snippets]` with
a `code` option and whatever the result is compared against. If a
snippet is missing, all the checks that need it are skipped.

| Snippet | Options | What it should do | Checks that use it |
|---|---|---|---|
| `value` | `code`, `value` | evaluate to `value` | `eval.value`, `eval.survives-error`, `eval.ns`, `eval.unknown-ns`, `session.ephemeral`, `session.unknown`, `session.closed` |
| `stdout` | `code`, `out` | print `out` to the standard output | `eval.stdout`, `eval.stdout-order` |
| `stderr` | `code`, `err` | print `err` to the standard error | `eval.stderr` |
| `throw` | `code` | raise an error | `eval.error-status`, `eval.error-report`, `eval.survives-error` |
| `define` | `code`, `use`, `value` | define something that `use` reads back as `value` | `session.persistent` |
| `session-state` | `code`, `use`, `value` | leave some state in the session that `use` reads back as `value` | `session.across-connections`, `session.isolated` |
| `multiple` | `code`, `values` | consist of several forms that evaluate to `values` (in order) | `eval.multiple-forms` |

A few tips for writing snippets:

- `value` and `values` are compared as strings, so they have to match
  what your server returns exactly. A Clojure string will come back
  quoted (`"\"hi\""`), while Erlang's `ok` comes back as `ok`.
- `out` and `err` are compared after joining all the output chunks, so
  it doesn't matter how your server splits its output into messages. It's
  better to print without a trailing newline, as losing output that
  doesn't end with a newline is a common bug.
- `define` and `session-state` may look similar, but they check different
  things. `define` checks that later evaluations see earlier definitions,
  so a global definition is fine. `session-state` on the other hand has to
  be something that belongs to the session, so proof can check that
  sessions are isolated from each other and that they survive reconnects.
  In Clojure that's `*1`, while in Erlang (where variable bindings belong
  to the session) you can use a binding:

  ```toml
  [snippets.session-state]
  code = "ProofMarker = proof_marker."
  use = "ProofMarker."
  value = "proof_marker"
  ```

- proof evaluates the `use` of `session-state` in a new session as well,
  where it must not return `value` (an error is fine).

## Expected Failures

You can list checks that your server is known to fail in the
`[expected-failures]` table, along with the reason for the failure:

```toml
[expected-failures]
"eval.multiple-forms" = "only the last value is returned, see #42"
"wire.canonical" = "dict keys aren't sorted yet"
```

Expected failures are still shown in the report, but they don't fail the
run. On the other hand, if a check on the list passes (or produces just a
warning), proof fails the run and asks you to remove the check from the
list. This way the list can only get shorter over time and you'll know
right away when something gets fixed.

Some other things worth knowing:

- Only failures need to be listed, as warnings never fail a run.
- The `wire.*` checks can be listed like any other check.
- Unknown check IDs result in an error (before the server is started).
- When you run proof with `-only` it doesn't complain about expected
  failures that passed, as the `wire.*` checks see only part of the
  messages in such runs.

You can see all the check IDs with `proof list`.
