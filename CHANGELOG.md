# Changelog

## main (unreleased)

### Features

- [#18](https://github.com/nrepl/proof/pull/18): The `version` snippet, so reports and the compatibility matrix show which version of a server proof checked.
- [#18](https://github.com/nrepl/proof/pull/18): `proof matrix -html`, which builds the compatibility matrix as a web page, and the daily matrix of the bundled profiles on [GitHub Pages](https://nrepl.github.io/proof/).
- [#19](https://github.com/nrepl/proof/pull/19): Profiles for nbb and let-go, and scenarios for `proof serve` that act like them.

## 0.1.0 (2026-10-26)

Initial release.

### Features

- Checks for what clients need from a server's replies to `describe`, unknown ops, sessions, `eval` and `stdin`, and for every message the server sends (the `wire.*` checks).
- [#4](https://github.com/nrepl/proof/pull/4): Client profiles, which send what CIDER, Calva, Conjure, vim-fireplace, REPLy and neorepl send while connecting and evaluating code ([#6](https://github.com/nrepl/proof/pull/6), [#8](https://github.com/nrepl/proof/pull/8)).
- Profiles for nREPL, Babashka, ClojureCLR, Basilisp, jank, dialtone and repartee, which come with proof, and expected failures that can't go stale.
- `proof matrix`, which builds a compatibility matrix out of the JSON reports of several servers.
- [#1](https://github.com/nrepl/proof/pull/1): `proof proxy`, which checks what a client sends to a server, with `-record` for starting a client profile ([#5](https://github.com/nrepl/proof/pull/5)).
- [#2](https://github.com/nrepl/proof/pull/2): `proof serve`, a server for the tests of clients that can behave like other servers ([#3](https://github.com/nrepl/proof/pull/3)).
- [#10](https://github.com/nrepl/proof/pull/10): Binaries for Linux, macOS and Windows on every release, and a GitHub Action for running proof in the CI of a server.
- [#11](https://github.com/nrepl/proof/pull/11): Checks for `interrupt`, and scenarios for nREPL's interrupt bugs in `proof serve`.
- [#12](https://github.com/nrepl/proof/pull/12): Checks for `load-file`, and `load-file` in `proof serve`.
- [#13](https://github.com/nrepl/proof/pull/13): Checks for completions and lookups, and nREPL's `completions` and `lookup` in `proof serve`.
- [#14](https://github.com/nrepl/proof/pull/14): Checks for requests that go wrong: frames that aren't requests, fields of the wrong type and clients that go away in the middle of an eval.
