# diple

**Diple** (`›`) is a transparent terminal layer that sits under an AI
coding-agent CLI — Claude Code, Codex CLI, pi — in the terminal you already
use. It leaves the agent's rendering, your theme, and your history exactly as
they are, and adds two things:

1. **Point at what you mean.** Rest the pointer on any block the agent printed
   — a paragraph, a list item, a code line, a diff line — and the whole block
   lifts into reverse video, with a strip beneath it offering `note`, `fix`,
   `ask` and `copy`. Press it, in this turn or an earlier one, and write a
   short note there. Diple claims no modifier key: pressing a raised block is
   the whole gesture, so Shift and Option stay your terminal's.
2. **A tray that grows out of the agent's own input box.** Notes and your own
   instructions pile up as cards while the agent keeps working. One gesture
   compiles them into a single well-structured message and submits it through
   the agent's native input. An empty tray has no footprint, and there is only
   ever one input box on screen.

Because Diple owns the mouse, it also gives back the selection your terminal
stops offering: drag to select and copy, double-press for a word, triple-press
for a line. What lands on the clipboard is the transcript's text, so a command
that wrapped over three rows comes back as one line.

It replaces the "screenshot, then retype a wall of text" loop with something
closer to a pull-request review: many pinned comments, sent once. It is
local-only: no network calls, no model calls of its own, no telemetry.

*Point at it. Say it. Send it once.*

## Install

```
go build -o ~/.local/bin/diple ./cmd/diple   # or: brew install <tap>/diple
diple on                                     # shims for every supported agent
```

`diple on` puts a shim for each supported CLI in `$XDG_DATA_HOME/diple/bin`
and adds one marked block to your shell startup files that puts that directory
first on `PATH`. Re-read your profile and `claude`, `codex`, and `pi` run under
Diple from anywhere — a bare shell, a script, a `tmux` pane, a `herdr` pane.

```
diple status     # what is installed, where it stands on PATH
diple off        # remove the shims and the PATH block
DIPLE=0 claude   # run the real CLI once, unwrapped
diple claude     # run one wrapped session without shims
```

## Using it

| Gesture | Does |
|---|---|
| Rest on a block | Raises it, with the strip beneath: `note` `fix` `ask` │ `copy` |
| Press the raised block | Opens a note on it, tagged `note` |
| Press a tag on the strip | Opens the note tagged that way |
| `n` `f` `a` `c` | The strip's four choices, without moving the pointer |
| Drag | Selects across rows and copies; inside one block it is a span too |
| Double-press, triple-press | Copy the word, then the whole line |
| Shift-press a second raised line | Extend a code or diff line range |
| `Alt+F` `Alt+A` `Alt+N` | Move the note's tag chip while writing |
| `Enter` / `Esc` | Save the note; `Esc` keeps what you typed |
| `Alt+K`, then `j`/`k` | Reach and walk blocks by keyboard |
| `v`, then `w`/`b`, `l`/`h` | Open and size a span by word or character |
| `L`, `V` | Take a code line and extend the range |
| `[` `]` | Jump to the previous or next assistant turn |
| `/` | Search the transcript and go to the match |
| `Alt+N` / `Alt+O` | Write a free card, or the tray's one closing remark |
| `Tab` | Move focus between the native box and the tray |
| `j`/`k`, `J`/`K`, `e`, `d`, `×` | In the tray: select, reorder, edit, delete |
| `Alt+Enter` / `Alt+P` | Send the tray as one message, or paste it without sending |
| `Alt+H` | Hide the layer, and give the mouse back to your terminal |
| Wheel, `End` | Scroll Diple's scrollback and return to live |

A press means what it means when it ends: a press that moves is a selection,
and only one that comes up where it went down is a press on what lies under
it. A press that lands before the block has risen reaches the agent untouched,
as every wheel event does.

`[`, `]`, and `/` are Diple's only while it already owns navigation — the view
is scrolled, a block is selected, or the tray has focus — so a slash command
still starts with `/`. Every binding is yours to change in
`$XDG_CONFIG_HOME/diple/bindings.conf`; `diple bindings` prints the table in
effect.

A free card can carry attachments: `@path` references a file, and `!command`
runs the command once, then, and carries what it printed. `diple stash` sets a
tray aside and `diple unstash` gives it back to the next session of that agent.

```
diple --plain claude              # draw without colour
diple --marks=off claude          # no tail marks, and no raise with them
diple --motion=off claude         # draw the raise's final frame only
diple --copy-on-select=off claude # copy only on an explicit copy
```

## Supported agents

| Agent | Verified against | Notes |
|---|---|---|
| Claude Code | 2.1.266 | Inline and fullscreen rendering |
| Codex CLI | 0.153.4 | Writes its transcript after the fact, so a live session anchors notes at paragraph granularity |
| pi | session format 3 | Echoes your prompt above its reply; Diple never mistakes one for the other |

Any agent without an adapter still runs — Diple wraps it and passes it through
untouched.

## What it will not do

No browser or window of its own, no second input box, no rewriting of the
agent's output, no model calls, no memory of whether a note was addressed, and
no Windows support in this release.

`AGENTS.md` has the layout and the rules, `CONTRIBUTING.md` how to work on it,
and `docs/adapters.md` how to teach Diple another CLI.

MIT licensed.
