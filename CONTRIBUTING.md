# Contributing

If you discover issues, have ideas for improvements or want to add a new
check or server, please report them to the
[issue tracker](https://github.com/nrepl/proof/issues) or submit a pull
request. proof is only as good as the knowledge captured in its checks.
A lot of that knowledge is in the heads of the people who build nREPL
servers and clients, so every contribution helps!

## Issues

Here are the kinds of reports that are the most useful to us.

### A Check That's Wrong

If proof fails your server for something that works fine with real
clients, that's a bug in proof and we'd really like to hear about it.
Please include:

- the version of proof you're using (for a binary installed with `go
  install`, `go version -m $(which proof)` will show it)
- your profile
- the output of `proof run -v -only '<check id>' <profile>`
- why you think the behavior is fine (ideally with a link to the client
  code that handles it)

### Something proof Doesn't Check

If you know of something that clients depend on and that proof doesn't
check yet, let us know. All we need for a new check is a link to the
client code that depends on it (or to the bug it caused) and a short
explanation of what a server can get wrong.

### A Gap in the Spec

If you've found a place where the [draft spec](https://spec.nrepl.org)
is silent or disagrees with what clients do, and it's not in
[doc/spec-changes.md](doc/spec-changes.md) yet, please open an issue or a
pull request adding it.

### A Bug in proof

Please describe what you did, what happened and what you expected to
happen.

## Adding Your Server

Pull requests adding profiles for more servers are very welcome! Start
the profile with a comment explaining what needs to be installed and add
the server to the Compatibility workflow, so it runs every day.
[doc/hacking.md](doc/hacking.md#adding-a-server) has all the details.

## Pull Requests

[doc/hacking.md](doc/hacking.md) explains how the code is organized and
how to add a check. When submitting a pull request:

- Keep it focused on a single topic and split the work into focused
  commits with good commit messages.
- Make sure every new check is backed by a link to the client code that
  depends on the behavior (for failures) or to nREPL or the spec (for
  warnings). See the [grading rule](doc/design.md#how-checks-are-graded)
  for details. For client rules it's the other way around - failures
  need a link to the server code that breaks.
- Add a quirk to the fake server for every new check, and an entry for
  it in `internal/checks/checks_test.go`. Client rules get a quirk in the
  scripted client in `internal/checks/client_test.go` instead.
- Make sure new and updated checks pass against nREPL itself
  (`profiles/clojure.toml`).
- Run `gofmt -l .`, `go vet ./...` and `go test -race ./...` before
  submitting your changes.
- Update the documentation when you change something users can see. New
  snippets should be documented in [doc/profiles.md](doc/profiles.md) and
  new options in [doc/usage.md](doc/usage.md).
