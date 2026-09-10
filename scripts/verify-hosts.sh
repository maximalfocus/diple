#!/usr/bin/env bash
# Records a wrapped session in each host the portability list names, types one
# Diple gesture into it where R-014 calls the host driven, and checks the
# capture. Run it from the repository root:
#
#   scripts/verify-hosts.sh [--plain] [--manual] [host...]
#
# With no host arguments it does every host it can start on this machine and
# says which ones it skipped, so the release checklist can record both. A host
# that is absent is reported apart from one that is present and could not be
# driven: they are different facts, and only the first is a reason to skip.
set -uo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
out="${DIPLE_CAPTURES:-$root/.captures}"
mkdir -p "$out"
# Control sockets live on a short path of their own. A unix socket path over
# about 108 bytes cannot be bound, and a checkout deep enough to cross that
# silently cost the host its gesture.
ctl="$(mktemp -d "${TMPDIR:-/tmp}/diple-ctl.XXXXXX")"
trap 'rm -rf "$ctl"' EXIT
diple="$out/diple"
agent="$root/scripts/fake-agent.sh"
plain=""
manual=""
while :; do
	case "${1:-}" in
	--plain) plain="--plain"; shift ;;
	--manual) manual="1"; shift ;;
	*) break ;;
	esac
done

go build -o "$diple" "$root/cmd/diple" || exit 1

capture_for() { printf '%s/%s%s.capture.jsonl' "$out" "$1" "${plain:+.plain}"; }
wrapped() { printf '%s --record %s %s %s' "$diple" "$(capture_for "$1")" "$plain" "$agent"; }

# The gesture handed to a driven host, in the order R-005 and R-015 ask for.
# It claims no modifier: a drag over the agent's output, which selects and
# copies, then a free card written from the tray, which draws.
#
# The drag comes first, while the tray is still empty and the agent's rows
# have not moved under it. Diple's own gestures are ordinary SGR mouse
# reports, so a host that can type into a window can deliver them.
drag_press=$'\033[<0;3;3M'
drag_move=$'\033[<32;24;3M'
drag_release=$'\033[<0;24;3m'
card=$'\033nhello from the host check\r'

# app_bundle prints the path of a macOS app bundle, looking in /Applications
# and then in ~/Applications, where Homebrew puts a cask when /Applications is
# not writable by the user running it.
app_bundle() {
	local name="$1" dir
	for dir in /Applications "$HOME/Applications"; do
		if [ -d "$dir/$name" ]; then
			printf '%s\n' "$dir/$name"
			return 0
		fi
	done
	return 1
}

# host_version names the version a host was verified at, which R-014 records
# for the same reason R-012 records an adapter's CLI version: a host's control
# interface changes between releases, and a run that does not name the version
# cannot be read later.
host_version() {
	case "$1" in
	tmux) tmux -V 2>/dev/null | awk '{print $2}' ;;
	kitty) kitty --version 2>/dev/null | awk '{print $2}' ;;
	wezterm) wezterm --version 2>/dev/null | awk '{print $2}' ;;
	herdr) herdr --version 2>/dev/null | awk '{print $2}' ;;
	ghostty)
		if bundle="$(app_bundle Ghostty.app)"; then
			"$bundle/Contents/MacOS/ghostty" --version 2>/dev/null | head -1 | awk '{print $2}'
		else
			ghostty --version 2>/dev/null | head -1 | awk '{print $2}'
		fi ;;
	iterm2) defaults read "$(app_bundle iTerm.app)/Contents/Info" CFBundleShortVersionString 2>/dev/null ;;
	terminal.app) defaults read /System/Applications/Utilities/Terminal.app/Contents/Info CFBundleShortVersionString 2>/dev/null ;;
	esac
}

# wait_for_gesture holds the run while the person at the keyboard makes the
# gesture in a host nothing can type into from outside.
wait_for_gesture() {
	local host="$1"
	cat >&2 <<-EOM

	  $host is open with a wrapped session in it. In that window:
	    1. drag across two rows of the agent's output and let go — it selects
	       and copies;
	    2. press Alt+N (Option+N on macOS), type a note, and press Enter — it
	       makes a card;
	    3. quit the agent so the capture is written.
	  Then press Enter here.
	EOM
	read -r _ </dev/tty || true
}

wait_for_capture() {
	local file="$1" tries=0
	while [ "$tries" -lt 45 ]; do
		[ -s "$file" ] && return 0
		sleep 1
		tries=$((tries + 1))
	done
	[ -s "$file" ]
}

# start_host returns 0 when the session was started and, for a driven host,
# the gesture was handed to it; 3 when the host is not installed; 4 when it is
# installed but could not be started or driven.
start_host() {
	local host="$1" cmd launcher pane bundle
	cmd="$(wrapped "$host")"
	rm -f "$(capture_for "$host")"
	# A launcher script, so a host that only knows how to open a file can
	# still start a wrapped session.
	launcher="$ctl/launch-$host.sh"
	printf '#!/bin/sh\nexec %s\n' "$cmd" >"$launcher"
	chmod +x "$launcher"
	case "$host" in
	tmux)
		command -v tmux >/dev/null || return 3
		tmux kill-session -t diple-verify 2>/dev/null
		tmux new-session -d -s diple-verify -x 100 -y 30 "$cmd" || return 4
		sleep 3
		for part in "$drag_press" "$drag_move" "$drag_release" "$card"; do
			tmux send-keys -t diple-verify -l -- "$part" || return 4
			sleep 1
		done
		;;
	herdr)
		command -v herdr >/dev/null || return 3
		# herdr 0.8 replaced `agent start <argv>` with a tab and pane API,
		# and `agent send` with `pane send-text`; 0.7 is kept behind it so
		# the check reads either. Conflating the two with "not available"
		# is what turned a real verification into a silent skip.
		pane="$(herdr tab create --cwd "$root" --label diple-verify 2>/dev/null |
			sed -n 's/.*"root_pane":{[^}]*"pane_id":"\([^"]*\)".*/\1/p')"
		if [ -n "$pane" ]; then
			printf '%s\n' "$pane" >"$ctl/herdr.pane"
			herdr pane run "$pane" "$cmd" >/dev/null 2>&1 || return 4
			sleep 4
			for part in "$drag_press" "$drag_move" "$drag_release" "$card"; do
				herdr pane send-text "$pane" "$part" >/dev/null 2>&1 || return 4
				sleep 1
			done
		else
			herdr agent start diple-verify --cwd "$root" --no-focus -- sh -c "$cmd" >/dev/null 2>&1 || return 4
			sleep 4
			for part in "$drag_press" "$drag_move" "$drag_release" "$card"; do
				herdr agent send diple-verify "$part" >/dev/null 2>&1 || return 4
				sleep 1
			done
		fi
		;;
	wezterm)
		command -v wezterm >/dev/null || return 3
		# A window from `wezterm start --always-new-process` is not in the
		# mux `wezterm cli` talks to, and `send-text` needs an explicit
		# --pane-id: without one it writes to whichever pane is focused, so
		# the gesture went anywhere but the wrapped session.
		wezterm cli list >/dev/null 2>&1 || {
			wezterm start -- sh -c 'sleep 120' >/dev/null 2>&1 &
			sleep 5
		}
		pane="$(wezterm cli spawn --new-window --pane-id 0 -- sh -c "$cmd" 2>/dev/null)" || return 4
		[ -n "$pane" ] || return 4
		sleep 4
		for part in "$drag_press" "$drag_move" "$drag_release" "$card"; do
			printf '%s' "$part" | wezterm cli send-text --no-paste --pane-id "$pane" >/dev/null 2>&1 || return 4
			sleep 1
		done
		;;
	kitty)
		command -v kitty >/dev/null || return 3
		kitty -o allow_remote_control=yes --listen-on "unix:$ctl/kitty.sock" --detach sh -c "$cmd" || return 4
		sleep 5
		[ -S "$ctl/kitty.sock" ] || return 4
		for part in "$drag_press" "$drag_move" "$drag_release" "$card"; do
			kitty @ --to "unix:$ctl/kitty.sock" send-text -- "$part" >/dev/null 2>&1 || return 4
			sleep 1
		done
		;;
	ghostty)
		# Ghostty offers no way to type into a running window from outside,
		# so R-014 classes it pass-through only and its capture covers
		# forwarding, the envelope, and restore. It ships its command inside
		# the bundle on macOS and on PATH on Linux.
		if bundle="$(app_bundle Ghostty.app)"; then
			"$bundle/Contents/MacOS/ghostty" -e "$launcher" >/dev/null 2>&1 &
		elif command -v ghostty >/dev/null; then
			ghostty -e "$launcher" >/dev/null 2>&1 &
		else
			return 3
		fi
		sleep 5
		;;
	iterm2)
		# `open -a` runs a script in a terminal without asking for the
		# automation permission an AppleScript would need.
		bundle="$(app_bundle iTerm.app)" || return 3
		open -a "$bundle" "$launcher" >/dev/null 2>&1 || return 4
		sleep 4
		;;
	terminal.app)
		[ -d /System/Applications/Utilities/Terminal.app ] || return 3
		open -a /System/Applications/Utilities/Terminal.app "$launcher" >/dev/null 2>&1 || return 4
		sleep 4
		;;
	*)
		echo "unknown host: $host" >&2
		return 2
		;;
	esac
}

cleanup_hosts() {
	tmux kill-session -t diple-verify 2>/dev/null
	if [ -s "$ctl/herdr.pane" ]; then
		herdr pane close "$(cat "$ctl/herdr.pane")" >/dev/null 2>&1
	fi
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
		echo "SKIP $host: not installed on this machine"
		status=1
		continue
		;;
	4)
		echo "FAIL $host: installed, but the session could not be started or driven"
		status=1
		continue
		;;
	2) status=1; continue ;;
	esac
	if [ -n "$manual" ]; then
		wait_for_gesture "$host"
	fi
	if ! wait_for_capture "$file"; then
		echo "FAIL $host: no capture was written"
		status=1
		continue
	fi
	go run "$root/internal/hostcheck" --host "$host" --host-version "$(host_version "$host")" ${plain:+--plain} "$file" || status=1
done
cleanup_hosts
exit "$status"
