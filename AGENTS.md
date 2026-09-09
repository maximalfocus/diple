# diple — contributor instructions

Diple is a transparent terminal layer under an AI coding-agent CLI. It wraps the
agent in a pseudo-terminal, forwards bytes both ways, and keeps a VT screen
model with scrollback. Everything Diple adds is drawn with the terminal's own
palette; the agent's output is never repainted with different attributes.

## Layout

- `cmd/diple` — the `diple` command. `diple <agent> [args…]` is the wrapped form.
- `internal/agent` — locating the real agent executable and the non-interactive
  invocation forms that bypass the wrapper.
- `internal/screen` — the VT screen model: cells with original attributes,
  scrollback, alternate screen, and re-emission of rows.
- `internal/record` — session fixture recording and replay.
- `internal/wrap` — the session: PTY forwarding, wheel-owned scrollback, input
  filtering, terminal restore.

## Rules

- One static binary, no runtime dependencies. Go modules compile in; nothing is
  fetched or spawned at run time except the wrapped agent.
- Fail open: any failure of Diple's own logic degrades to plain pass-through and
  never leaves the host terminal in raw mode.
- Forward the agent's bytes unmodified while live. Diple's only additions to the
  output stream are its own mode envelope and the regions it owns.
- No network calls, no telemetry, no model calls.
- Keep the conceptual model in `AGENTS.md` and the code aligned: a new concept is
  a recorded design decision in the issue or PR, never a quiet special case.

## Verification

```
gofmt -l .
go vet ./...
go test ./...
```

CI runs the same on Linux and macOS and cross-compiles darwin/linux × amd64/arm64.

## Commits and pull requests

Commit subjects are `type: summary` in the imperative, at most 72 characters.
Types: `feat` `fix` `docs` `test` `ci` `build` `refactor` `perf` `chore`

Every pull request declares exactly one `Delivery-Type: <type>` line in its body
using one of those types; the squash-merge subject is composed from it. Issue
and pull-request titles stay untyped.
