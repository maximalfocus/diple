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

```
gofmt -l .
go vet ./...
go test -race ./...
```

CI runs the same on Linux and macOS and cross-compiles darwin/linux ×
amd64/arm64. `scripts/verify-hosts.sh` records a wrapped session in each host
the portability list names and checks the capture; it is the release-boundary
check, not part of CI, because it needs those terminals installed. Hosts that
can be typed into from outside must also show that a gesture made a card, and
one of those failing to take the gesture is a failure, not a lesser pass.

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
