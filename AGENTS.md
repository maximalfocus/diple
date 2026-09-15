# diple — contributor instructions

Diple is a transparent terminal layer under an AI coding-agent CLI. It wraps the
agent in a pseudo-terminal, forwards bytes both ways, and keeps a VT screen
model with scrollback. Everything Diple adds is drawn with the terminal's own
palette; the agent's output is never repainted with different attributes.

## Layout

- `cmd/diple` — the `diple` command. `diple <agent> [args…]` is the wrapped form;
  `diple blocks <transcript>` prints an adapter's turns and blocks.
- `internal/agent` — locating the real agent executable, the non-interactive
  invocation forms that bypass the wrapper, and the catalogue of agent CLIs
  aligned with those `herdr` recognises.
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
  real CLIs on PATH, wrapped or identity-only, the `PATH` block in the user's
  startup files, and the report behind `diple status`.
- `internal/persona` — the copies of Diple's own binary named as the user
  typed an agent, kept per build in the user cache, that Diple re-executes
  itself through so the host names the agent.
- `internal/keys` — the binding table: every gesture Diple owns, its default
  key, and the user's `bindings.conf` read over the defaults.
- `internal/clip` — the clipboard ladder: OSC 52 where the host answers for
  the clipboard, the platform's own command where it does not. It only ever
  writes; Diple never reads the clipboard.
- `internal/attach` — what a free card's attachment refers to: a path
  is only a reference, a command is run once and its output travels with the
  card.
- `internal/hostcheck` — the host check behind `scripts/verify-hosts.sh`, and
  the verification tiers' uses of it: replaying the captures committed under
  `testdata/hosts/<host>/<version>/`, filing and verifying head-bound host
  evidence, and classifying which changes need that evidence.
- `internal/acceptance` — tier-2 checks on the tests themselves: every unit
  case in `docs/acceptance/` is named by a test, and no test under `internal/`
  sleeps on the wall clock.

## Pass-through and composited

The session forwards the agent's bytes untouched while Diple owns nothing on
screen. As soon as a raise, selection, strip, editor, search field, highlight,
or a non-empty tray shows, it composites: the wrapped process is told
`rows − tray height`, the physical screen is built from the screen model with
the tray inserted above the agent's input box, and only rows that changed are
repainted. When the last Diple-owned thing disappears the live screen is
repainted from the model and the agent's cursor, attribute, and modes are
restored. That repaint-from-the-model is what gives every cell the raise and
the strip borrow back attribute for attribute. Diple draws only with the
default colours, the 16 indexed colours, and bold, dim, reverse, and
underline; `--plain` drops colour and keeps the attributes.

## Rendering modes

An agent runs inline (it prints into the terminal, Diple's scrollback is the
history) or fullscreen (alternate screen with its own viewport and mouse
tracking, the agent viewport is the history). Claude Code's `tui: fullscreen`
setting selects the latter. Every adapter reports the mode from the screen
model, and alignment runs against whichever history the mode provides.

## The raise, the strip, and the press

Resting the pointer inside a block raises it: its bounding box — from the
block's smallest indent to one column past its longest row — in reverse video,
and a moment later a strip on the row beneath it, at the block's own left edge,
carrying `note`, `fix`, `ask` and, past a divider, `copy`. The strip is opaque,
writing every cell it covers, and it underlines the tag in force. Only its four
choices hold the pointer: a pointer anywhere else on that row belongs to what
the strip covers, so a strip never stands between the user and the block
beneath it.

The block and its strip are one target, and the raise outlives the pointer by a
moment so a loose path between them does not lose it. A press means what it
means when it ends: a press that moves is a selection wherever it started, and
only a press that comes up where it went down is a press on what lies under it.
The dwell is what makes a raised block Diple's — a press that lands before it,
like every wheel event and every press outside a raised block, reaches the
agent untouched.

**Nothing here is a modifier.** The host's own selection modifier — Shift, or
Option on macOS — never reaches Diple, which is the whole reason the gesture
arrives in Terminal.app and iTerm2, where Option is spent on the host's own
bypass of mouse reporting. The one exception the model makes is a Shift-press
on a second raised line, which extends a code or diff line range.

## Selection and the clipboard

Diple owns the mouse, so the host stops offering its own drag-selection. Diple
therefore makes the selection itself: a drag selects across rows and past any
block's edge and copies when the button comes up, a double-press takes the word
and a triple-press the whole logical line. What is copied is what the selection
shows: its highlighted cells, read by the column model that drew them, so a
wide or combined character comes out once, Markdown the screen hides never
travels, and a visible list marker does. Rows join where a logical line
wrapped — a terminal soft wrap keeps its spaces, and an agent wrap inside an
aligned block drops the renderer's continuation indent — and other rows end
lines. No highlight covers the agent's turn marker, and no copy carries it.
`--copy-on-select=off` leaves the clipboard to the explicit `copy`, and `Alt+H`
returns the mouse to the host entirely.

## Cards and the tray

A card is anchored — pointing at a block or span, carrying one tag: `fix`,
`ask`, or `note` — or free: untagged prose, which is what the user would
otherwise have typed into the box. One card per tray may be marked overall; it
holds the last place, so nothing reorders past it and a second one edits the
first. `Alt+N`, or `+` with the tray focused, opens the same one-line editor on
Diple's own overlay row, and `Alt+O` writes the closing remark. While a free
card is being written, a line that reads `@path` or `!command` becomes an
attachment instead of the card's text and the field stays open: a path is
compiled as a reference, and a command is run once, then, and what it printed
travels with the card. A block that carries a card keeps a tail mark — Diple's
own `›` one column past the block's last character, in the card's tag colour,
with the count for more than one. `diple stash` sets an agent's saved tray
aside in one slot and `diple unstash` gives it to that agent's next session;
without an agent named they take the most recent session's, or the one agent
with a stash, and ask when that is ambiguous.

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

`diple on` writes a shim into `$XDG_DATA_HOME/diple/bin` for each catalogued
agent found on `PATH` that has an adapter, and `diple on <agent>` adds any other
agent as identity-only, and it adds a marked block to the user's startup files
that puts that directory first on `PATH`; `diple off` removes every shim it
wrote and the block. `diple status` names the agents found, wrapped, and
identity-only.

The host names the agent, never Diple. A host that inspects the process in its
pane reads `argv[0]`, the kernel's command name, or the executable's path, so
before wrapping Diple re-executes itself, same process, through the agent's
persona — a copy of its own binary named exactly as the user typed the agent,
kept per build in the user cache and never on `PATH` — and all three facts
become the agent's. A symlink would leave the command name `diple`, and a hard
link can report another link's path and outlives an upgrade. An identity-only
agent runs as itself: the process in the pane is the real CLI, with Diple
owning nothing there. Diple's own variables — `DIPLE`, `DIPLE_SHIM_DIR`, and the
persona's hand-over — leave the environment before the agent starts. A shim uses shell
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
it by word and by character; the strip's own four letters — `n`, `f`, `a`, `c`
— then make the card or copy, exactly as they do after a press.

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
  user explicitly attaches to a free card, which runs once, in the
  session's directory, under a timeout and an output cap.
- Fail open: any failure of Diple's own logic degrades to plain pass-through and
  never leaves the host terminal in raw mode.
- Forward the agent's bytes unmodified while live. Diple's only additions to the
  output stream are its own mode envelope and the regions it owns.
- No network calls, no telemetry, no model calls.
- Keep the conceptual model in `AGENTS.md` and the code aligned: a new concept is
  a recorded design decision in the issue or PR, never a quiet special case.

## Verification

Five tiers, each defined by what it needs in order to run; `CONTRIBUTING.md`
has the commands. CI runs the first four — static checks and builds; unit and
session tests under `-race -shuffle=on -count=1`; replay of the adapters'
fixtures and of one committed capture per host and drawing mode under
`testdata/hosts/`; and `tmux` and `herdr` driven live and headless. One check,
`gate`, fails unless all of them succeeded and a change outside the
documentation allowlist carries tier 5: head-bound reports from developers'
live runs in every host, carried through `refs/evidence/` and verified by
`internal/hostcheck`. Tests under `internal/` never sleep on the wall clock,
and `internal/acceptance` enforces that and the case lists under
`docs/acceptance/`.

`scripts/verify-hosts.sh [--plain] [--manual] [--evidence <dir>] [--baseline] [--copy] [host…]`
records a wrapped session in each host the portability list names,
hands the gesture to the hosts R-014 calls driven, and runs
`internal/hostcheck` over the capture — the envelope asked for and given back,
the agent's bytes forwarded unchanged with an empty tray, no 24-bit colour from
Diple, a card made and a copy taken where the host is driven, and the terminal
restored. Diple's gestures are ordinary SGR mouse reports, so a host that can
type into a window can deliver them. `--manual` starts the session and waits
for the person at the keyboard to make the gesture, which is how a
pass-through host is shown to deliver it. It is not part of CI, because it
needs those terminals installed; `RELEASE.md` records the result with the
version each host was verified at.

`--copy` is the copy check instead. A canned agent writes a transcript and
draws a reply holding every case R-015 names — a list item with emphasis,
inline code and a link, wide and combined characters, an agent wrap and a
terminal wrap, a hard break, unaligned output — and each copy action is made on
it in turn: drag, double- and triple-press, the strip's `copy`, and `c`. Before
each, the clipboard is set to a sentinel; after, it is read back and must hold
exactly what the selection shows. A pass-through host gets each step from the
person at the keyboard.

`internal/hostcheck` owns the class table: WezTerm, kitty, `tmux`, and `herdr`
are driven, and every other host is pass-through only. The distinction is the
whole point of the check. A driven host whose capture carries no gesture was
not driven at all — a control interface that moved between host versions, a
window the host's own CLI cannot address, a control socket too long to bind —
and it fails rather than falling back to the pass-through check, because a
silent fallback is how an unverified host came to report a pass. The script
keeps its control sockets on a short temporary path for the same reason, and
reports a host that is absent apart from one that is present and would not be
driven.

## Commits and pull requests

Commit subjects are `type: summary` in the imperative, at most 72 characters.
Types: `feat` `fix` `docs` `test` `ci` `build` `refactor` `perf` `chore`

Every pull request declares exactly one `Delivery-Type: <type>` line in its body
using one of those types; the squash-merge subject is composed from it. Issue
and pull-request titles stay untyped.
