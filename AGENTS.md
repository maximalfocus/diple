# diple — contributor instructions

Diple is a transparent terminal layer under an AI coding-agent CLI. It wraps the
agent in a pseudo-terminal, forwards bytes both ways, and keeps a VT screen
model with scrollback. Everything Diple adds is drawn with the terminal's own
palette; the agent's output is never repainted with different attributes.

## Layout

- `cmd/diple` — the `diple` command. `diple <agent> [args…]` is the wrapped form;
  `diple blocks <transcript>` prints an adapter's turns and blocks.
- `internal/agent` — locating the real agent executable and the non-interactive
  invocation forms that bypass the wrapper.
- `internal/screen` — the VT screen model: cells with original attributes,
  scrollback, alternate screen, and re-emission of rows.
- `internal/record` — session fixture recording and replay.
- `internal/wrap` — the session: PTY forwarding, wheel-owned scrollback, input
  filtering, turn navigation and transcript search, terminal restore, and the
  transcript tracker.
- `internal/adapter` — the per-CLI interface (transcript discovery and parsing,
  rendering mode, alignment) and its registry; `internal/adapter/claude` is the
  Claude Code adapter with its pinned fixtures under `testdata/<version>/`.
- `internal/blocks` — Markdown to blocks: paragraphs, headings, list items, code
  blocks and lines, diff lines, table rows, tool calls.
- `internal/align` — matching a turn's blocks to rendered rows by letters and
  digits only, in order, tolerant of wrapping and decoration.
- `internal/card` — cards, anchors, the tray, and its per-session persistence
  under the user's state directory.

## Pass-through and composited

The session forwards the agent's bytes untouched while Diple owns nothing on
screen. As soon as a selection, toolbar, editor, search field,
highlight, or a non-empty tray shows, it composites: the wrapped process is told `rows − tray height`,
the physical screen is built from the screen model with the tray inserted
above the agent's input box, and only rows that changed are repainted. When
the last Diple-owned thing disappears the live screen is repainted from the
model and the agent's cursor, attribute, and modes are restored. Diple draws
only with the default colours, the 16 indexed colours, and bold, dim,
reverse, and underline; `--plain` keeps reverse and underline only.

## Rendering modes

An agent runs inline (it prints into the terminal, Diple's scrollback is the
history) or fullscreen (alternate screen with its own viewport and mouse
tracking, the agent viewport is the history). Claude Code's `tui: fullscreen`
setting selects the latter. Every adapter reports the mode from the screen
model, and alignment runs against whichever history the mode provides.

## Turn navigation and search

`[` and `]` move the history view between assistant turns and `/` opens a
transient one-line field that searches the transcript. All three are Diple's
gestures only while Diple already owns navigation — the viewport is scrolled
off live, a block selection is open, or the tray has focus — and reach the
agent unchanged otherwise, so a slash command still starts with `/` and the
native box keeps every key it had. Inline, a jump scrolls Diple's own
scrollback. Fullscreen, the agent owns its viewport: Diple forwards wheel
notches and re-aligns whatever becomes visible until the turn shows or the
viewport stops moving, and an agent that never asked for mouse reports simply
cannot be scrolled. Alignment therefore matches turns to the turn-marker
regions the rows actually contain, so a viewport showing the middle of a
conversation aligns the turns it shows and leaves the rest without rows.

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
