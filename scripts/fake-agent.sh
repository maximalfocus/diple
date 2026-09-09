#!/bin/sh
# A canned agent for host verification: it prints one reply in the shape a
# coding-agent CLI draws, holds the session open long enough for a gesture to
# arrive, and exits on its own so the capture is always complete. It makes no
# model calls and needs no credentials, so a capture says something about the
# host and nothing about an agent.
printf '\033[1m⏺ Plan\033[0m\r\n'
printf '\r\n'
printf '  1. Read the config file and note the two ports it listens on\r\n'
printf '  2. Change the handler\r\n'
printf '  3. Verify\r\n'
printf '\r\n'
printf '  That is the whole plan.\r\n'
printf '\r\n'
printf '❯ '
sleep "${DIPLE_FAKE_AGENT_SECONDS:-8}"
printf '\r\n'
