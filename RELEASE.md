# Release checklist

The initial release is the release boundary in the product contract: before it
the repository stays private and no domain is registered; at it the name is
re-checked, the domain is registered, and the repository is made public.

## Done in the repository

- [x] Every requirement of the initial release is implemented and landed.
- [x] `gofmt -l .`, `go vet ./...`, and `go test -race ./...` are clean, and CI
      runs them on Linux and macOS and cross-compiles darwin/linux ×
      amd64/arm64.
- [x] Adapters for Claude Code, Codex CLI, and pi, each with fixtures recorded
      from a real session at a pinned version.
- [x] Public README, contributor guide, and adapter-authoring guide.
- [x] Homebrew formula in `packaging/homebrew/diple.rb`, with the release URLs
      and checksums left for the release itself.
- [x] Name re-check, run at the release boundary: `brew search diple` returns
      only `dipc`; the npm registry has no `diple` package (404); a GitHub
      repository search for `diple` returns only unrelated projects whose names
      merely contain the string (diplexers, diplexcoin). `diple.sh` is held by a
      third party, so the domain to register is `getdiple.sh`.

## Host verification

`scripts/verify-hosts.sh` records a wrapped session in a host and checks the
capture: the mouse envelope was asked for and given back, the agent's bytes
were forwarded unchanged while the tray was empty, Diple drew no 24-bit colour,
and the terminal was left as it was found. R-014 puts every host in one of two
classes and the check enforces the difference. A **driven** host offers a
documented way to type into a running window from outside, so its capture must
also carry a gesture and the card that gesture made. A **pass-through only**
host offers none, so forwarding, the envelope, and restore are the whole of
what it can show. A driven host whose gesture does not arrive fails the run
instead of falling back to the pass-through check, and a host that is absent is
reported apart from one that is present and could not be driven.
`scripts/verify-hosts.sh --plain` additionally requires that Diple's own
drawing used only reverse and underline.

Each row was recorded twice, once in each drawing mode, and names the host
version it ran against as R-012 does for an adapter's CLI.

| Host | Class | Result | Version | Machine |
|---|---|---|---|---|
| WezTerm | driven | pass, gesture | 20240203-110809-5046fc22 | Linux amd64 |
| kitty | driven | pass, gesture | 0.45.0 | Linux amd64 |
| `tmux` | driven | pass, gesture | 3.6 | Linux amd64 |
| `herdr` | driven | pass, gesture | 0.8.2 | Linux amd64 |
| Ghostty | pass-through | pass | 1.3.0 | Linux amd64 |
| Terminal.app | pass-through | re-run needed | — | macOS arm64 |
| iTerm2 | pass-through | pending | — | macOS arm64 |

Ghostty needs no first launch on Linux, where it takes `-e` on PATH rather than
from inside a bundle; its Linux capture is what R-014 asks of a pass-through
host, and it is the same check the macOS bundle would produce.

Terminal.app passed an earlier run whose captures were machine-specific and
whose host version was not recorded, so under R-014 it needs one re-run to name
its version: `scripts/verify-hosts.sh terminal.app`, which drives nothing and
needs nobody at the display.

iTerm2 is installed on the macOS machine but has never been opened by a person
there, and it sits behind a first-run window until one does. Once it has been
opened once, its row is one command: `scripts/verify-hosts.sh iterm2`.

## Left for the release itself

- [ ] The iTerm2 capture, after its first launch, and the Terminal.app re-run
      that records its version.
- [ ] Register `getdiple.sh`.
- [ ] Cut the release, fill the formula's URLs and checksums, publish the tap.
- [ ] Make the repository public. This is a separate, explicitly invoked step —
      publication is never part of an implementation run.
