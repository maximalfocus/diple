# Codex CLI fixtures

One directory per verified Codex CLI version. The rendering mode has a
recorded session (`<mode>.recording.jsonl`, written by `diple --record`) and
the rollout file Codex wrote for it (`<mode>.transcript.jsonl`), reduced to
the entries an adapter reads. The tests replay the recording into the screen
model and align the transcript's blocks against the rows; the expected rows
live in `codex_test.go` and were read off the replayed fixture.

## Recording a new version

1. Run the real CLI under a pseudo-terminal of 80×24 from a neutral working
   directory (for example `/tmp/diple-codex-fixture`), with
   `diple --record inline.recording.jsonl codex`. Answer the directory-trust
   question, since it is the CLI's own dialog and not part of a turn.
2. Paste, with bracketed paste, a prompt that asks for exactly this Markdown
   and nothing else: a level-two heading, a numbered list whose first item
   wraps at 80 columns and whose second item has two nested bullets, a fenced
   Go code block, a fenced diff block, and a closing paragraph.
3. Wait for the reply, then stop the recording.
4. Reduce the newest rollout file under the CLI's sessions directory: keep the
   opening `session_meta` entry with only the session id, working directory,
   CLI version, timestamp, originator, and source; keep the `response_item`
   message entries for the user's prompt and the assistant's reply, up to the
   last assistant entry; drop every `developer` entry, the environment block,
   and the CLI's own base instructions, none of which an adapter reads and any
   of which may carry local configuration. Confirm neither file carries an
   account, a home directory, or a credential.
5. Add the version to `Verified` and the expected rows to the test.
