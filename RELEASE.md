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
- [x] Host and theme verification re-run against the gesture R-005 and R-015
      describe: all seven hosts, both drawing modes, each carrying the card and
      the copy the gesture made.
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

Diple's gestures are ordinary SGR mouse reports and ordinary keys, so the
gesture handed to a driven host is the real one R-005 and R-015 describe: a
drag across the agent's output, which selects and copies, then the pointer
resting on a block until it rises and a press on it, which opens the note. The
check reads the copy from the session's own count rather than from the bytes,
because the clipboard ladder ends in OSC 52 on some hosts and in the platform's
own command on others; which rung carried it is the ladder's business, and it
has its own tests and its own live check.

The canned agent is installed under the name of an adapter Diple knows and the
session runs in a directory of its own. Both matter: an agent with no adapter
is passed through untouched by design, so there would be no block to raise, and
a checkout that has been worked in with the real agent has transcripts under it
that the canned rows would align against, leaving every block without rows for
a reason that has nothing to do with the host.

`scripts/verify-hosts.sh --manual <host>` starts the session and waits for the
person at the keyboard to make the gesture, then checks the capture the same
way. It is how a pass-through host is shown to deliver R-005 and R-015, since
nothing can type into one from outside. Setting `DIPLE_DRIVE_CMD` to a command
that synthesises the gesture runs that instead of waiting; the repository ships
no such command, because it is per-platform and needs the machine's own input
permission.

Each row names the host version it ran against, as R-012 does for an adapter's
CLI, and says which drawing modes it was recorded in.

| Host | Class | Result | Version | Machine |
|---|---|---|---|---|
| WezTerm | driven | pass, gesture + card + copy, both modes | 20240203-110809-5046fc22 | macOS 26.6 arm64 |
| kitty | driven | pass, gesture + card + copy, both modes | 0.48.2 | macOS 26.6 arm64 |
| `tmux` | driven | pass, gesture + card + copy, both modes | 3.7c | macOS 26.6 arm64 |
| `herdr` | driven | pass, gesture + card + copy, both modes | 0.7.4 | macOS 26.6 arm64 |
| Terminal.app | held | passed by hand at this version, claimed no longer | 2.15 | macOS 26.6 arm64 |
| Ghostty | held | passed by hand at this version, claimed no longer | 1.3.1 | macOS 26.6 arm64 |
| iTerm2 | held | passed by hand at this version, claimed no longer | 3.7.0 | macOS 26.6 arm64 |

The last three passed by hand, at those versions, before R-014 narrowed its
claim to the hosts something can type into from outside. Diple still passes
them through and tier 3 still replays their captures, but no list claims them
and no gate waits on them until S-020 verifies them by hand again.

The copy was read back from the machine rather than inferred: with a sentinel on
the pasteboard before each run, `pbpaste` after it returned the dragged text.
kitty and WezTerm carried it through OSC 52; `tmux`, `herdr`, Terminal.app and
Ghostty through the platform rung. The first `tmux` run is what found the
multiplexer defect in the ladder — OSC 52 went to `tmux`, `tmux` forwarded it to
Terminal.app, Terminal.app ignored it, and the sentinel was still there.

Terminal.app and Ghostty settle the question R-005 was reworked for. Both spend
no Option on Diple, because Diple now claims no modifier at all: the pointer
rested on a block, the block rose, a press on it opened the note, and the card
came out — in a host that nothing can type into from outside.

All three pass-through hosts were driven with real synthetic mouse and key
events against the running window, since nothing can type into one from
outside. Two things a run needs to know: the screen must be unlocked, because
the window server delivers nothing to a locked one; and a host that asks before
running a script asks again for every path it has not seen, which is why the
launcher is kept at a stable path under the captures directory.

## Check enforcement

Every pull request runs the verification tiers `CONTRIBUTING.md` describes,
and `gate` is the check a landing reads. GitHub enforces the landing path on
`main` with two rulesets, and neither has a bypass, not even for an
administrator. `require-pull-request` takes a change only through a pull
request, merged by squash, keeps history linear, and refuses force-pushes and
deleting the branch. `require-gate` refuses to merge a pull request until
`gate` has passed on its head. No server check reads a pull request's body, so
the landing procedure still checks the rest: `scripts/gate-status.sh <pr>` must
report `gate` green on the pull request's current head and, for a change
outside the documentation allowlist, a passing macOS acceptance run of that
head recorded in its body.

## Left for the release itself

- [ ] Register `getdiple.sh`.
- [ ] Cut the release, fill the formula's URLs and checksums, publish the tap.
- [x] Once the repository is public, configure a `main` ruleset that requires
      the `gate` check and forbids direct pushes, and verify both: a direct
      push to `main` is refused, and a pull request whose `gate` is red cannot
      merge. Done with the two rulesets above: a direct push to `main` was
      rejected for both rules, and GitHub refused to merge #48, whose `gate`
      was red, which was then closed unmerged.
- [ ] Make the repository public. This is a separate, explicitly invoked step —
      publication is never part of an implementation run.
