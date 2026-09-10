# Release checklist

The initial release is the release boundary in the product contract: before it
the repository stays private and no domain is registered; at it the name is
re-checked, the domain is registered, and the repository is made public.

## Done in the repository

- [x] Every requirement of the initial release is implemented and landed,
      through the selection and clipboard parity R-015 adds.
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
also carry a gesture, the card that gesture made, and the copy it took. A
**pass-through only** host offers none, so forwarding, the envelope, and
restore are the whole of what an unattended run can show there. A driven host
whose gesture does not arrive fails the run instead of falling back to the
pass-through check, and a host that is absent is reported apart from one that
is present and could not be driven. `scripts/verify-hosts.sh --plain`
additionally requires that Diple's own drawing carried no colour.

Diple's gestures are ordinary SGR mouse reports, so the gesture handed to a
driven host is the real one R-005 and R-015 describe: a drag across the agent's
output, which selects and copies, then a free card written from the tray. The
check reads the copy from the session's own count rather than from the bytes,
because the clipboard ladder ends in OSC 52 on some hosts and in the platform's
own command on others; which rung carried it is the ladder's business, and it
has its own tests and its own live check.

`scripts/verify-hosts.sh --manual <host>` starts the session and waits for the
person at the keyboard to make the gesture. It is how a pass-through host is
shown to deliver R-005 and R-015, since nothing can type into one from outside.

Each row was recorded twice, once in each drawing mode, and names the host
version it ran against as R-012 does for an adapter's CLI.

| Host | Class | Result | Version | Machine |
|---|---|---|---|---|
| WezTerm | driven | pass, gesture + card + copy | 20240203-110809-5046fc22 | macOS 26.6 arm64 |
| kitty | driven | pass, gesture + card + copy | 0.48.2 | macOS 26.6 arm64 |
| `tmux` | driven | pass, gesture + card + copy | 3.7c | macOS 26.6 arm64 |
| `herdr` | driven | pass, gesture + card + copy | 0.7.4 | macOS 26.6 arm64 |
| Terminal.app | pass-through | pass, forwarding/envelope/restore | 2.15 | macOS 26.6 arm64 |
| iTerm2 | pass-through | **not re-run** | 3.7.0 | — |
| Ghostty | pass-through | **not re-run** | 1.3.1 | — |

The four driven hosts were re-verified against this release's gesture in both
drawing modes. Terminal.app was re-verified in its class.

**Outstanding, and the reason this checklist is not complete:** R-005 and R-015
require the gesture and the copy to be shown live in every host, the
pass-through class included, because a gesture no host delivers is no gesture.
That needs `scripts/verify-hosts.sh --manual terminal.app iterm2 ghostty`, run
by a person at the keyboard, and for iTerm2 and Ghostty it needs a session that
can launch them: launching a GUI application that is not already running was
not possible from the environment this release was prepared in, and `open -a`
returned without starting either.

## Left for the release itself

- [ ] Register `getdiple.sh`.
- [ ] Cut the release, fill the formula's URLs and checksums, publish the tap.
- [ ] Make the repository public. This is a separate, explicitly invoked step —
      publication is never part of an implementation run.
