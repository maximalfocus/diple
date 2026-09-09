# diple

Diple (›) is a transparent terminal layer that sits under an AI coding-agent
CLI in the terminal you already use. It leaves the agent's rendering, your
theme, and the conversation history untouched.

This repository is private while the product is built. `diple <agent> [args…]`
runs the agent inside a pseudo-terminal, forwards bytes both ways, and keeps a
full screen model with scrollback that the mouse wheel scrolls.

```
go build ./cmd/diple
./diple claude
```

See `AGENTS.md` for layout, rules, and verification.
