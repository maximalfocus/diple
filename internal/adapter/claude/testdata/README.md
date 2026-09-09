# Claude Code fixtures

One directory per verified Claude Code version. Each rendering mode has a
recorded session (`<mode>.recording.jsonl`, written by `diple --record`) and
the transcript Claude Code wrote for it (`<mode>.transcript.jsonl`, reduced to
its `user` and `assistant` entries up to the last assistant entry). The tests
replay the recording into the screen model and align the transcript's blocks
against the rows the mode provides; the expected rows live in `claude_test.go`
and were read off the replayed fixtures by hand.

## Recording a new version

1. Run the real CLI under a pseudo-terminal of 80×24 from a neutral working
   directory (for example `/tmp/diple-fixture`) with every `CLAUDE*` variable
   removed from the environment, so the CLI saves its transcript. Use
   `diple --record <mode>.recording.jsonl claude`; for the inline mode set
   `CLAUDE_CODE_DISABLE_ALTERNATE_SCREEN=1`, for fullscreen leave the user's
   `tui: fullscreen` setting in effect.
2. Paste, with bracketed paste, a prompt that asks for exactly this Markdown
   and nothing else: a level-two heading, a numbered list whose first item
   wraps at 80 columns and whose second item has two nested bullets, a fenced
   Go code block, a fenced diff block, and a closing paragraph.
3. Wait for the reply, then stop the recording before `/exit` so the
   fullscreen fixture keeps the alternate screen active. Copy the newest
   transcript from `~/.claude/projects/<encoded cwd>/`, keep only its `user`
   and `assistant` lines up to the last assistant entry, and confirm neither
   file carries an account, home directory, or credential.
4. Add the version to `Verified` and the expected rows to the test.
