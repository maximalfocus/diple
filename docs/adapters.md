# Writing an adapter

An adapter is the only place one agent CLI's own facts live. Everything else —
the screen model, the tray, the cards, the fold, the keyboard — is the same for
every agent. A new adapter is a small package under `internal/adapter/<name>`
that implements the interface in `internal/adapter/adapter.go`, registers
itself, and ships fixtures recorded from a real session.

The three shipped adapters are the worked examples: `claude` marks its turns
with a glyph and runs in two rendering modes, `codex` marks its turns but
writes its transcript after the fact, and `pi` marks nothing and echoes your
prompt above its reply. Between them they cover the shapes you are likely to
meet.

## The interface, method by method

**`Name() string`** — the CLI's executable name. It is also the shim's name and
the key in the registry.

**`VerifiedVersions() []string`** — the versions your fixtures pin. A transcript
from another version is still parsed and aligned, because a CLI updates itself
past its pinned versions: `Parse` marks it `Unverified`, `diple blocks` labels
it, and the block-by-block fallback keeps whatever no longer matches from
taking its neighbours down. Only a fixture refuses one:
`adapter.RequireVerified` fails with `adapter.VersionError`, naming the
version. If the CLI records no version, pin what it does record — `pi` pins its
session format version — and say so in the package comment.

**`Bypass(args []string) bool`** — the invocation forms that must run the real
CLI with no wrapper: `--version`, `--help`, print modes, and the CLI's own
subcommands. `agent.Bypass` covers the shared ones; add what is yours
(`codex exec`, `pi install`).

**`Discover(cwd string, since time.Time) (string, error)`** — the transcript of
the session started in `cwd` at or after `since`. Return
`adapter.ErrNoTranscript` while there is none; the tracker keeps asking. Bind a
candidate by what the file itself records — the working directory and the start
— never by modification time alone, because a CLI may keep writing to an older
session's file.

**`Parse(r io.Reader) (*adapter.Transcript, error)`** — turns and blocks. Take
the assistant's text through `blocks.Parse`, which gives paragraphs, headings,
list items with their ordinals, code blocks and their lines, diff lines, table
rows. Leave out anything the user should never quote, such as a model's
thinking. If the CLI draws the user's prompt in the same shape as a reply, add
those messages as turns with `Echo: true`: they hold their place in the
alignment and are never annotatable.

**`Mode(s *screen.Screen) adapter.Mode`** — inline or fullscreen, read from the
screen model. Fullscreen means the agent took the alternate screen and asked
for mouse tracking, so the agent's viewport is the history and Diple forwards
wheel and navigation to it.

**`Align(t *adapter.Transcript, rows []string) []adapter.TurnAlignment`** — call
`adapter.Assign(t, rows, rules)` with your decoration facts. It matches turns
to candidate rows in order and steps past the rows each match occupies. Your
`align.Rules` carry:

- `TurnMarker` — what a turn's first row starts with; leave empty when the CLI
  marks nothing and every paragraph start becomes a candidate.
- `PromptMarker` — what the user's echoed prompt or the input box starts with;
  it ends the region a turn may occupy.
- `Fence` — the fence a renderer prints around a code block, when it prints one
  (pi does); those rows belong to no block and are stepped over.
- `MaxResync` — how many rows a tool call's results may take before the next
  block must appear.

**`Fallback(rows []string) []adapter.TurnAlignment`** — what to offer when there
is no transcript at all: `adapter.Paragraphed(rows, rules)`. Annotation must
keep working at paragraph granularity; this is the promise that makes a missing
or unreadable transcript a degradation rather than a failure. A paragraph ends
at a row without letters or digits, and before a row that begins with a list
marker, a fence or a turn marker, or that is indented less than the
paragraph's text, so each item of a tight list is a block of its own. The same
paragraphs stand in for any block of an aligned turn that fails to match, and
the blocks around it keep their rows.

**`InputRow(screenRows []string) int`** — where the native input box begins, or
`-1` when it is not on screen. Diple inserts the tray directly above it.

**`Busy(s *screen.Screen) bool`** — whether a turn is in flight, read from the
screen. Diple holds a fold for an agent that is busy and does not queue.

**`Prompt(s *screen.Screen) bool`** — whether the agent is showing a question of
its own. While one is up Diple owns no keys at all, so the next keypress
answers the agent. Recognise it by something the CLI only draws then — the
footer its dialogs share is usually enough — and never by something its working
or idle state also draws.

**`QueuesWhenBusy() bool`** — whether typed input is accepted during a turn and
run afterwards.

## Fixtures

A fixture is a real session, not a mock:

1. Record one under a pseudo-terminal of 80×24, from a neutral working
   directory, with `diple --record <mode>.recording.jsonl <cli>`.
2. Ask for a reply with a heading, a numbered list whose first item wraps and
   whose second has nested bullets, a fenced code block, a fenced diff, and a
   closing paragraph. That one reply exercises every block kind.
3. Keep the transcript the CLI wrote, reduced to the entries an adapter reads,
   and confirm neither file carries an account, a home directory, or a
   credential.
4. Put both under `internal/adapter/<name>/testdata/<version>/` with a README
   that says how to record the next version.

## Tests an adapter owes

- Every block of the recorded reply aligns to its exact first and last row.
- A transcript from another version parses and aligns, marked unverified, and a
  fixture from it is refused, naming the version.
- A corrupt transcript still yields paragraph blocks.
- The input row, the busy state, and the prompt state are read correctly from
  the recording, and a busy screen is not mistaken for a prompt.
- The annotate and fold journeys replay against the fixture in
  `internal/wrap`, so the adapter is proved through the product, not only in
  isolation.

Register the adapter with `adapter.Register` in an `init`, import it for its
side effect in `cmd/diple`, and add the CLI to the README's table with the
version you verified.
