#!/usr/bin/env bash
# Records a wrapped session in each host the portability list names, types one
# Diple gesture into it where the host can be driven, and checks the capture.
# Run it from the repository root:
#
#   scripts/verify-hosts.sh [--plain] [host...]
#
# With no host arguments it does every host it can start on this machine and
# says which ones it skipped, so the release checklist can record both.
set -uo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
out="${DIPLE_CAPTURES:-$root/.captures}"
mkdir -p "$out"
diple="$out/diple"
agent="$root/scripts/fake-agent.sh"
plain=""
if [ "${1:-}" = "--plain" ]; then
	plain="--plain"
	shift
fi

go build -o "$diple" "$root/cmd/diple" || exit 1

capture_for() { printf '%s/%s%s.capture.jsonl' "$out" "$1" "${plain:+.plain}"; }
wrapped() { printf '%s --record %s %s %s' "$diple" "$(capture_for "$1")" "$plain" "$agent"; }

# The gesture typed into a host that can be driven: open the free-card
# chooser, write a question card, and let the tray draw.
gesture=$'\033nqhello from the host check\r'

wait_for_capture() {
	local file="$1" tries=0
	while [ "$tries" -lt 40 ]; do
		[ -s "$file" ] && return 0
		sleep 1
		tries=$((tries + 1))
	done
	[ -s "$file" ]
}

start_host() {
	local host="$1" cmd launcher
	cmd="$(wrapped "$host")"
	rm -f "$(capture_for "$host")"
	# A launcher script, so a host that only knows how to open a file can
	# still start a wrapped session.
	launcher="$out/launch-$host.sh"
	printf '#!/bin/sh\nexec %s\n' "$cmd" >"$launcher"
	chmod +x "$launcher"
	case "$host" in
	tmux)
		command -v tmux >/dev/null || return 3
		tmux kill-session -t diple-verify 2>/dev/null
		tmux new-session -d -s diple-verify -x 100 -y 30 "$cmd" || return 3
		sleep 3
		tmux send-keys -t diple-verify Escape n q "hello from the host check" Enter 2>/dev/null
		;;
	herdr)
		command -v herdr >/dev/null || return 3
		herdr agent start diple-verify --cwd "$root" --no-focus -- sh -c "$cmd" >/dev/null 2>&1 || return 3
		sleep 4
		herdr agent send diple-verify "$gesture" >/dev/null 2>&1
		;;
	wezterm)
		command -v wezterm >/dev/null || return 3
		wezterm start --always-new-process -- sh -c "$cmd" >/dev/null 2>&1 &
		sleep 4
		wezterm cli send-text --no-paste "$gesture" >/dev/null 2>&1
		;;
	kitty)
		command -v kitty >/dev/null || return 3
		kitty -o allow_remote_control=yes --listen-on "unix:$out/kitty.sock" --detach sh -c "$cmd" || return 3
		sleep 4
		kitty @ --to "unix:$out/kitty.sock" send-text "$gesture" >/dev/null 2>&1
		;;
	ghostty)
		# Ghostty ships its command inside the bundle and offers no way to
		# type into a running window from outside, so its capture covers
		# pass-through, the envelope, and restore, and the checker says so.
		[ -x /Applications/Ghostty.app/Contents/MacOS/ghostty ] || return 3
		/Applications/Ghostty.app/Contents/MacOS/ghostty -e "$launcher" >/dev/null 2>&1 &
		sleep 4
		;;
	iterm2)
		# `open -a` runs a script in a terminal without asking for the
		# automation permission an AppleScript would need.
		[ -d /Applications/iTerm.app ] || return 3
		open -a /Applications/iTerm.app "$launcher" >/dev/null 2>&1 || return 3
		sleep 4
		;;
	terminal.app)
		[ -d /System/Applications/Utilities/Terminal.app ] || return 3
		open -a /System/Applications/Utilities/Terminal.app "$launcher" >/dev/null 2>&1 || return 3
		sleep 4
		;;
	*)
		echo "unknown host: $host" >&2
		return 2
		;;
	esac
}

hosts=("$@")
if [ "${#hosts[@]}" -eq 0 ]; then
	hosts=(terminal.app iterm2 wezterm kitty ghostty tmux herdr)
fi

status=0
for host in "${hosts[@]}"; do
	file="$(capture_for "$host")"
	start_host "$host"
	case "$?" in
	3)
		echo "SKIP $host: not available on this machine"
		status=1
		continue
		;;
	2) status=1; continue ;;
	esac
	if ! wait_for_capture "$file"; then
		echo "FAIL $host: no capture was written"
		status=1
		continue
	fi
	go run "$root/internal/hostcheck" --host "$host" ${plain:+--plain} "$file" || status=1
done
tmux kill-session -t diple-verify 2>/dev/null
exit "$status"
