#!/usr/bin/env bash
# Records a wrapped session in each host the portability list names, types one
# Diple gesture into it where R-014 calls the host driven, and checks the
# capture. Run it from the repository root:
#
#   scripts/verify-hosts.sh [--plain] [--manual] [--evidence <dir>] [--baseline] [host...]
#
# With no host arguments it does every host it can start on this machine and
# says which ones it skipped, so the release checklist can record both. A host
# that is absent is reported apart from one that is present and could not be
# driven: they are different facts, and only the first is a reason to skip.
#
# --evidence <dir> is tier 5: the session is built from a clean checkout of
# HEAD, and each passing capture is filed in <dir> with a report binding it to
# that commit, for `scripts/evidence.sh upload`. --baseline files each passing
# capture under testdata/hosts/<host>/<version>/ for tier 3 to replay.
set -uo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
out="${DIPLE_CAPTURES:-$root/.captures}"
mkdir -p "$out"
# Control sockets live on a short path of their own. A unix socket path over
# about 108 bytes cannot be bound, and a checkout deep enough to cross that
# silently cost the host its gesture.
ctl="$(mktemp -d "${TMPDIR:-/tmp}/diple-ctl.XXXXXX")"
finish() {
	if [ -d "$ctl/head" ]; then
		git -C "$root" worktree remove --force "$ctl/head" >/dev/null 2>&1
	fi
	rm -rf "$ctl"
}
trap finish EXIT
diple="$out/diple"
# The canned agent is installed under the name of an adapter Diple knows, so a
# wrapped session has blocks to raise: an agent with no adapter is passed
# through untouched by design, and the annotate gesture has nothing to point
# at. The adapter finds no transcript for this session and falls back to
# paragraph granularity, which is exactly the path R-004 promises.
agent_dir="$ctl/bin"
agent="$agent_dir/claude"
plain=""
manual=""
evidence=""
baseline=""
while :; do
	case "${1:-}" in
	--plain) plain="--plain"; shift ;;
	--manual) manual="1"; shift ;;
	--evidence) evidence="${2:?--evidence needs a directory}"; shift 2 ;;
	--baseline) baseline="1"; shift ;;
	*) break ;;
	esac
done
mode="${plain:+plain}"
mode="${mode:-default}"

# src is the tree the session and the check are built from. Evidence is about
# one commit, so it is built from a clean checkout of HEAD, and nothing
# uncommitted in this tree can reach a report.
src="$root"
if [ -n "$evidence" ]; then
	head="$(git -C "$root" rev-parse HEAD)" || exit 1
	src="$ctl/head"
	git -C "$root" worktree add -q --detach "$src" "$head" || exit 1
	mkdir -p "$evidence"
	evidence="$(cd "$evidence" && pwd)"
	machine="$(uname -s) $(uname -r) $(uname -m)"
fi

(cd "$src" && go build -o "$diple" ./cmd/diple && go build -o "$ctl/hostcheck" ./internal/hostcheck) || exit 1
mkdir -p "$agent_dir"
cp "$src/scripts/fake-agent.sh" "$agent"
chmod +x "$agent"

capture_for() { printf '%s/%s%s.capture.jsonl' "$out" "$1" "${plain:+.plain}"; }
# wrapped runs the session in a directory of its own. The adapter looks for the
# transcript of the session it is wrapping under the working directory, and a
# checkout that has been worked in with the real agent has transcripts there:
# aligning the canned agent's rows against one of those would leave every block
# without rows, and nothing to raise, for a reason that has nothing to do with
# the host. Diple is exec'd, as a shell execs the agent a user types, so the
# pane's process is the agent's persona and not a shell left waiting on it:
# macOS tmux names a pane after its process group's leader, and a bash that
# runs this line without exec'ing is that leader.
wrapped() {
	printf 'cd %s && export PATH=%s:$PATH DIPLE_FAKE_AGENT_SECONDS=%s && exec %s --record %s %s claude' \
		"$ctl" "$agent_dir" "${DIPLE_FAKE_AGENT_SECONDS:-25}" "$diple" "$(capture_for "$1")" "$plain"
}

# The gesture handed to a driven host, in the order R-005 and R-015 ask for.
# It claims no modifier: a drag over the agent's output, which selects and
# copies, then the pointer resting on a block until it rises and a press on it,
# which opens the note the card is written in.
#
# The drag comes first, while the tray is still empty and the agent's rows have
# not moved under it. Diple's own gestures are ordinary SGR mouse reports, so a
# host that can type into a window can deliver them, and the dwell that raises
# a block is real time — which is why each part is handed over on its own.
drag_press=$'\033[<0;3;7M'
drag_move=$'\033[<32;24;7M'
drag_release=$'\033[<0;24;7m'
rest=$'\033[<35;6;3M'
press=$'\033[<0;6;3M'
release=$'\033[<0;6;3m'
note=$'hello from the host check\r'
gesture_parts=("$drag_press" "$drag_move" "$drag_release" "$rest" "$press" "$release" "$note")

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
	local host="$1" pid="${2:-0}"
	if [ -n "${DIPLE_DRIVE_CMD:-}" ]; then
		echo "driving $host (pid $pid) with $DIPLE_DRIVE_CMD" >&2
		$DIPLE_DRIVE_CMD "$host" "$pid" || return 1
		return 0
	fi
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

# host_pid is the process that owns the host's window, which a driving command
# needs so it can refuse to type into anything else.
host_pid() {
	local pat
	case "$1" in
	terminal.app) pat='Utilities/Terminal.app/Contents/MacOS/Terminal' ;;
	iterm2) pat='iTerm.app/Contents/MacOS/iTerm2' ;;
	ghostty) pat='Ghostty.app/Contents/MacOS/ghostty' ;;
	*) echo 0; return ;;
	esac
	# ps rather than pgrep: pgrep's match is restricted in some sessions and
	# silently finds nothing, which would hand a driving command a pid of 0
	# and let it type into whatever happened to be in front.
	ps -eo pid,comm | awk -v pat="$pat" '$2 ~ pat {print $1; exit}'
}

# wait_for_settled holds until the session that is writing the capture has
# exited. Waiting for the file to stop growing is not enough: a gesture is over
# well before the session is, and the recorder still has its last bytes to
# write when it closes.
wait_for_settled() {
	local file="$1" tries=0
	while [ "$tries" -lt 120 ]; do
		if ! ps -eo args | grep -q "[-]-record $file"; then
			return 0
		fi
		sleep 1
		tries=$((tries + 1))
	done
	return 0
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

# host_identity asks the host what it calls the wrapped pane, and its state,
# which R-017 says must name the agent the user launched, never Diple. The
# answer is appended to the capture, so the host check and every replay of the
# capture see it.
host_identity() {
	case "$1" in
	tmux) printf 'name=%s\n' "$(tmux display-message -p -t diple-verify '#{pane_current_command}' 2>/dev/null)" >"$ctl/identity" ;;
	herdr) herdr agent list 2>/dev/null | "$ctl/hostcheck" herdr-pane --pane "${2:-}" --name diple-verify >"$ctl/identity" ;;
	esac
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
	# A stable path, not one under the run's temporary directory: a host that
	# asks before running a script asks again for every new path it sees, and
	# a launcher whose name changed each run could never be approved once.
	launcher="$out/launch-$host.sh"
	# A host launched through the window server does not inherit this shell's
	# environment, so everything the session needs is already inside $cmd.
	# No exec: the command is a shell line of its own, not one executable.
	printf '#!/bin/sh\n%s\n' "$cmd" >"$launcher"
	chmod +x "$launcher"
	case "$host" in
	tmux)
		command -v tmux >/dev/null || return 3
		tmux kill-session -t diple-verify 2>/dev/null
		tmux new-session -d -s diple-verify -x 100 -y 30 "$cmd" || return 4
		sleep 3
		host_identity tmux
		for part in "${gesture_parts[@]}"; do
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
			host_identity herdr "$pane"
			for part in "${gesture_parts[@]}"; do
				herdr pane send-text "$pane" "$part" >/dev/null 2>&1 || return 4
				sleep 1
			done
		else
			herdr agent start diple-verify --cwd "$root" --no-focus -- sh -c "$cmd" >/dev/null 2>&1 || return 4
			sleep 4
			host_identity herdr
			for part in "${gesture_parts[@]}"; do
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
		for part in "${gesture_parts[@]}"; do
			printf '%s' "$part" | wezterm cli send-text --no-paste --pane-id "$pane" >/dev/null 2>&1 || return 4
			sleep 1
		done
		;;
	kitty)
		command -v kitty >/dev/null || return 3
		kitty -o allow_remote_control=yes --listen-on "unix:$ctl/kitty.sock" --detach sh -c "$cmd" || return 4
		sleep 5
		[ -S "$ctl/kitty.sock" ] || return 4
		for part in "${gesture_parts[@]}"; do
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
		bundle="$(app_bundle iTerm.app)" || return 3
		# A window of its own, so a run never lands in a window the user is
		# already working in, and the one to drive is unambiguous. `open -a`
		# is kept for a machine that will not answer the automation prompt.
		# `open -a` runs the launcher in a window of the application's own
		# making, which is the path its mouse reporting is reliable through.
		open -a "$bundle" "$launcher" >/dev/null 2>&1 || return 4
		sleep 5
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
	# A window this run opened is closed again, so a later run is never left
	# choosing between windows and never writes a capture twice over.
	if [ -s "$ctl/iterm2.window" ]; then
		osascript -e 'tell application "iTerm2" to close (every window whose id is '"$(cat "$ctl/iterm2.window")"')' >/dev/null 2>&1
	fi
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
		if ! wait_for_gesture "$host" "$(host_pid "$host")"; then
			echo "FAIL $host: the gesture could not be made" >&2
			status=1
			continue
		fi
	fi
	if ! wait_for_capture "$file"; then
		echo "FAIL $host: no capture was written"
		status=1
		continue
	fi
	wait_for_settled "$file"
	case "$host" in
	tmux | herdr)
		# A host that names the pane must have said what it calls it.
		if [ ! -s "$ctl/identity" ]; then
			echo "FAIL $host: the host did not say what it calls the wrapped pane"
			status=1
			continue
		fi
		echo "$host names the pane: $(cat "$ctl/identity")"
		printf '{"at":0,"kind":"host","data":"%s"}\n' "$(base64 <"$ctl/identity" | tr -d '\n')" >>"$file"
		rm -f "$ctl/identity"
		;;
	esac
	version="$(host_version "$host")"
	if ! "$ctl/hostcheck" --host "$host" --host-version "$version" ${plain:+--plain} "$file"; then
		status=1
		continue
	fi
	if [ -n "$evidence" ]; then
		"$ctl/hostcheck" report --sha "$head" --host "$host" --host-version "$version" \
			--mode "$mode" --machine "$machine" --out "$evidence" "$file" || status=1
	fi
	if [ -n "$baseline" ]; then
		# A capture is filed under the version it was recorded at, so a host
		# that cannot name its version cannot be a baseline.
		if [ -z "$version" ]; then
			echo "FAIL $host: no version to file the capture under" >&2
			status=1
			continue
		fi
		dest="$root/testdata/hosts/$host/$version"
		mkdir -p "$dest" && cp "$file" "$dest/$mode.capture.jsonl" && echo "baseline $host $version $mode"
	fi
done
cleanup_hosts
exit "$status"
