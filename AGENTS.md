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
  rendering mode, alignment), the shared turn-to-region matching every adapter
  aligns with, and the registry; `internal/adapter/claude`,
  `internal/adapter/codex`, and `internal/adapter/pi` are the agent adapters,
  each with its pinned fixtures under `testdata/<version>/`.
- `internal/blocks` — Markdown to blocks: paragraphs, headings, list items, code
  blocks and lines, diff lines, table rows, tool calls.
- `internal/align` — matching a turn's blocks to rendered rows by letters and
  digits only, in order, tolerant of wrapping and decoration.
- `internal/card` — cards of every kind, anchors, attachments, the tray, and
  its per-session persistence and stash slot under the user's state directory.
- `internal/shim` — the same-named executables that put Diple ahead of the
  real CLIs on PATH, the `PATH` block in the user's startup files, and the
  report behind `diple status`.
- `internal/keys` — the binding table: every gesture Diple owns, its default
  key, and the user's `bindings.conf` read over the defaults.
- `internal/attach` — what an instruction card's attachment refers to: a path
  is only a reference, a command is run once and its output travels with the
  card.

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

## Cards and the tray

A note is anchored in the agent's output; a question and an instruction are
free text; an overall card is a tray's one closing remark and always holds the
last place, so nothing reorders past it and a second one edits the first.
`Alt+N`, or `+` with the tray focused, offers the free kinds on the same
overlay row the block toolbar uses, and the same one-line editor writes them.
While an instruction card is being written, a line that reads `@path` or
`!command` becomes an attachment instead of the card's text and the field
stays open: a path is compiled as a reference, and a command is run once, then,
and what it printed travels with the card. `diple stash` sets an agent's saved
tray aside in one slot and `diple unstash` gives it to that agent's next
session.

## Adapters

An adapter is the only place an agent's own facts live: where its transcript
is and how it parses, the rendering mode, how a turn's blocks line up with
rows, where its input box starts, when it is busy, and when it is asking a
question of its own. Alignment itself is shared — `adapter.Assign` matches
turns to candidate regions in order and steps past the rows each match
occupies — so an adapter passes its decoration facts and nothing more. An
agent that marks its turns gives one candidate per marker; pi marks nothing,
so every paragraph start is a candidate, its rendered code fences are stepped
over, and the prompt it echoes above a reply joins the alignment as an echo:
it holds its place in the order, which is what keeps the reply from matching
the echo, and it is never something to annotate. A transcript that is missing or
unreadable is never an error: the paragraph fallback keeps annotation working,
which matters for the Codex CLI, whose 0.153.4 keeps a running session's
thread in its own state database and writes the rollout file Diple parses only
later.

## Shims and default-on

`diple on` writes one shim per registered adapter into `$XDG_DATA_HOME/diple/bin`
and adds a marked block to the user's startup files that puts that directory
first on `PATH`; `diple off` removes exactly what it added. A shim uses shell
builtins only, because it may run under a `PATH` that holds nothing but its own
directory and the real agent's, and it exports its own directory as
`DIPLE_SHIM_DIR` so Diple skips it when it looks for the real CLI. Diple also
refuses to execute anything carrying the shim marker, so a mis-set `PATH` can
never become an exec loop. `DIPLE=0` runs the real CLI for one invocation, and
`Alt+H` hides the layer for the rest of a session without ending it.

## The keyboard and the agent's own dialogs

Every mouse gesture has a key, and every key is a table entry a user can
rebind in `$XDG_CONFIG_HOME/diple/bindings.conf`; `diple bindings` prints the
effective table. A bad line there is reported on stderr and skipped — a config
file must never cost the user their terminal. `Alt+K` selects the topmost
block in view, `j`/`k` walk blocks, `L` takes a code line and `V` extends the
range, `v` opens a span on the block's first word and `w`/`b` and `l`/`h` size
it by word and by character; the toolbar letters then make the card, exactly
as they do after a click.

The adapter reports when the agent is showing a dialog of its own — a
permission question, a chooser, a text question. While one is up Diple owns
no keys at all: an open editor is put away with its text, the selection,
search field and chooser are dismissed, focus returns to the native box, and
the next keypress answers the agent. When the dialog clears the editor comes
back exactly as it was.

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
  fetched or spawned at run time except the wrapped agent and a command the
  user explicitly attaches to an instruction card, which runs once, in the
  session's directory, under a timeout and an output cap.
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
