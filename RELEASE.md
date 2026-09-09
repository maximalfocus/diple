# Release checklist

The initial release is the release boundary in the product contract: before it
the repository stays private and no domain is registered; at it the name is
re-checked, the domain is registered, and the repository is made public.

## Done in the repository

- [x] Every requirement of the initial release is implemented and landed.
- [x] `gofmt -l .`, `go vet ./...`, and `go test -race ./...` are clean, and CI
      runs them on Linux and macOS and cross-compiles darwin/linux ×
      amd64/arm64.
- [x] Adapters for Claude Code, Codex CLI, and pi, each with fixtures recorded
      from a real session at a pinned version.
- [x] Public README, contributor guide, and adapter-authoring guide.
- [x] Homebrew formula in `packaging/homebrew/diple.rb`, with the release URLs
      and checksums left for the release itself.
- [x] Name re-check, run at the release boundary: `brew search diple` returns
      only `dipc`; the npm registry has no `diple` package (404); a GitHub
      repository search for `diple` returns only unrelated projects whose names
      merely contain the string (diplexers, diplexcoin). `diple.sh` is held by a
      third party, so the domain to register is `getdiple.sh`.

## Host verification

`scripts/verify-hosts.sh` records a wrapped session in a host and checks the
capture: the mouse envelope was asked for and given back, the agent's bytes
were forwarded unchanged while the tray was empty, Diple drew no 24-bit colour,
and the terminal was left as it was found. Where the host can be typed into,
the check also requires that a gesture made a card and drew the tray.
`scripts/verify-hosts.sh --plain` additionally requires that Diple's drawing
used only reverse and underline.

| Host | Capture | Gesture | Notes |
|---|---|---|---|
| Terminal.app | pass | — | opens with `open -a`; nothing can type into it from outside |
| WezTerm | pass | — | `wezterm cli send-text` targets the active pane, not the new window |
| kitty | pass | pass | remote control types the gesture |
| Ghostty | pending | — | needs one interactive first launch before it will run a command |
| iTerm2 | pending | — | needs one interactive first launch before it will run a command |
| tmux | pass | pass | `send-keys` types the gesture |
| herdr | pass | pass | `agent send` types the gesture |
| `--plain` | pass | pass | recorded in tmux, kitty, and herdr |

Ghostty and iTerm2 are installed but have never been opened by a person on the
verification machine, and both sit behind a first-run window until one does.
Once either has been opened once, its row is one command:
`scripts/verify-hosts.sh ghostty` or `scripts/verify-hosts.sh iterm2`.

## Left for the release itself

- [ ] Ghostty and iTerm2 captures, after their first launch.
- [ ] Register `getdiple.sh`.
- [ ] Cut the release, fill the formula's URLs and checksums, publish the tap.
- [ ] Make the repository public. This is a separate, explicitly invoked step —
      publication is never part of an implementation run.
