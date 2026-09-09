# pi fixtures

One directory per verified pi session format. The rendering mode has a
recorded session (`<mode>.recording.jsonl`, written by `diple --record`) and
the session file pi wrote for it (`<mode>.transcript.jsonl`), reduced to its
`session` entry and the user and assistant messages up to the last assistant
message. pi records no CLI version in a session file, so the directory is
named for the session format version the entry carries.

## Recording a new version

1. Run the real CLI under a pseudo-terminal of 80×24 from a neutral working
   directory (for example `/tmp/diple-pi-fixture`), with
   `diple --record inline.recording.jsonl pi`.
2. Paste, with bracketed paste, a prompt that asks for exactly this Markdown
   and nothing else: a level-two heading, a numbered list whose first item
   wraps at 80 columns and whose second item has two nested bullets, a fenced
   Go code block, a fenced diff block, and a closing paragraph. pi echoes the
   prompt on screen in the same shape as a reply, which is why the transcript
   keeps the user's message too: it holds its place in the alignment.
3. Wait for the reply, then stop the recording.
4. Copy the newest session file from the CLI's sessions directory for that
   working directory, keep its `session` entry and the user and assistant
   messages up to the last assistant one, and confirm neither file carries an
   account, a home directory, or a credential.
5. Add the version to `Verified` and the expected rows to the test.
