# Contributing to diple

## Getting the code running

```
go build ./cmd/diple
./diple claude          # one wrapped session, no shims installed
./diple --record s.jsonl claude   # capture a session as a fixture
```

Go is the only dependency, and the binary has no runtime dependencies: nothing
is fetched or spawned at run time except the wrapped agent and a command a
user explicitly attaches to a free card.

## Verification

A change lands only when every test that bears on it has passed, and which
tests bear on it is decided by the paths it touches, not by its author. Tests
sit in five tiers, each defined by what it needs in order to run. CI runs the
first four, and one check, `gate`, fails unless all of them and the
host-evidence check succeeded.

1. **Static and build.** `gofmt -l .`, `go vet ./...`, `go mod tidy -diff`,
   and the darwin/linux × amd64/arm64 cross-compiles.
2. **Unit and session.** `go test -race -shuffle=on -count=1 ./...` on Linux
   and macOS. Session tests use an injected clock and an in-memory terminal.
   Tests under `internal/` drive timed behaviour by injected scheduling and
   never sleep or touch the network; a bounded deadline that only detects a
   hang is fine, and a test of supported subprocess behaviour may wait for the
   process under a timeout. `internal/acceptance` fails on a `time.Sleep`.
3. **Replay.** The adapters' pinned fixtures, and one committed capture per
   host and drawing mode under `testdata/hosts/<host>/<version>/`, replayed
   against the current build on every pull request by
   `go run ./internal/hostcheck replay`. Replay checks recorded input against
   today's code; it does not prove a host still delivers it. Record captures
   with `scripts/verify-hosts.sh --baseline` and `--plain --baseline`, which
   run the canned agent; read them for private data before committing, and
   refresh them when a host's version or the expected behaviour changes,
   keeping the earlier valid ones.
4. **Live headless hosts.** CI drives `tmux` and `herdr` on Linux and macOS
   through `scripts/verify-hosts.sh` in both drawing modes. `herdr` is tier 4
   because it runs as a headless server, `herdr --session <name> server`, that
   its CLI addresses by session name with no terminal attached. Both are
   provisioned explicitly, and an absent one fails. For live copy readback the
   Linux runners' clipboard service is an Xvfb display read with `xclip`, and
   the macOS runners' is the system pasteboard read with `pbpaste`.
5. **Live windowed hosts.** WezTerm, Kitty, Ghostty, iTerm2 and Terminal.app
   need a desktop, so developers run them with the same script at the versions
   `RELEASE.md` records; `--manual` leaves the gesture to the person at the
   keyboard in a pass-through host. Machines aggregate: a host missing on one
   machine is run on another, never waived.

Hosts that can be typed into from outside must also show that a gesture made
a card, and one of those failing to take the gesture is a failure, not a
lesser pass.

### Host evidence

A change that touches anything outside the documentation allowlist —
`LICENSE`, the top-level Markdown files, and Markdown under `docs/` apart from
`docs/acceptance/` — needs tier-5 evidence for every host in both drawing
modes, bound to the pull request's final head. `internal/hostcheck classify`
decides, and changing the classifier needs evidence too.

```
scripts/verify-hosts.sh --evidence /tmp/ev wezterm kitty ghostty tmux herdr
scripts/verify-hosts.sh --plain --evidence /tmp/ev wezterm kitty ghostty tmux herdr
scripts/evidence.sh upload /tmp/ev
```

`--evidence` builds from a clean checkout of `HEAD`, so commit and push first.
It files each passing capture with a report carrying the head SHA, the host,
its version, the drawing mode, the machine, the capture's digest, and the host
check's `PASS`. `upload` pushes the bundle to `refs/evidence/<sha>/…` in this
private repository, outside every branch so no commit has to contain its own
SHA, and prints the line to put in the pull request's body, one per machine:

```
Host-Evidence: refs/evidence/<sha>/<stamp> <object id>
```

CI fetches each bundle with the workflow's own `GITHUB_TOKEN`, which has
`contents: read` and nothing more, and checks that it is the object named,
that every report is for the head, passed, and is under fourteen days old,
and that every capture matches its digest and replays. Then it requires every
host in both drawing modes. Missing, incomplete, unreadable, stale or expired
evidence fails and needs a fresh run and upload; editing the body re-runs
`gate`. A bundle is kept until its pull request lands, and
`scripts/evidence.sh prune <sha>` deletes those of a landed head. No credential
is written into a report. A report attests to a session a developer ran;
replay cannot prove that a host operated physically.

### Acceptance cases

From S-014 on, a slice lists its acceptance cases in
`docs/acceptance/<slice>.md`, and the test that proves a unit case names it in
its doc comment. `docs/acceptance/README.md` has the format; the tier-2 test in
`internal/acceptance` rejects a unit case no test names and a named case no
list has. Host cases are proved by host evidence.

### macOS acceptance

A change outside the documentation allowlist also passes acceptance on a
macOS machine before it lands, whatever Linux shows. From a clean checkout of
the final pull-request head, run the tier-1 and tier-2 commands,
`scripts/verify-hosts.sh` for the hosts that machine has, and the issue's
acceptance cases, then record in the pull request's body the machine, its
macOS version, the commands and their results, ending with the line:

```
macOS-Acceptance: <head sha> PASS
```

CI's macOS runners do not substitute for this run. A failed or missing run,
or a machine that cannot be reached, blocks the landing, and a new head needs
a new run.

### Landing

GitHub takes a change into `main` only through a pull request merged by
squash, and refuses to merge one until `gate` has passed on its head;
`RELEASE.md` names the rulesets. No server check reads the body, so the
maintainer lands a pull request only when `scripts/gate-status.sh <pr>` reports
`gate` green on its current head and, for a change outside the documentation
allowlist, a passing macOS acceptance run of that head; anything else refuses.
A red `main` lets only its own fix land.

## Layout

`AGENTS.md` is the map: what each package owns, how pass-through and
compositing relate, what the rendering modes mean, how cards and the tray
work, how the keyboard and the agent's own dialogs interact, and what the
shims do. Read it before changing behaviour — it is the conceptual model this
codebase is kept aligned with, and a new concept belongs in it.

## What a change looks like

- Keep the conceptual model whole. A new concept, a special case, a boundary
  move, or a convenience feature that fragments the model is a design decision
  and is recorded in the issue or the pull request, never slipped in.
- Add tests at the boundary the change is about: a fixture replay for an
  adapter, a session test for a gesture, a unit test for a parser.
- Never repaint what the agent painted. Output and history are re-emitted with
  their original attributes; Diple draws only what it owns, with the default
  colours, the sixteen indexed colours, and bold, dim, reverse, and underline.
- Fail open. Any failure of Diple's own logic degrades to plain pass-through
  and never leaves the terminal in raw mode.

## Commits and pull requests

Commit subjects are `type: summary` in the imperative, at most 72 characters,
with types `feat` `fix` `docs` `test` `ci` `build` `refactor` `perf` `chore`.
Every pull request declares exactly one `Delivery-Type: <type>` line in its
body; the squash subject is composed from it. Issue and pull-request titles
stay untyped.

Say in the pull request what you verified and how, including the commands you
ran and anything you could not prove.
