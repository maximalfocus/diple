#!/usr/bin/env bash
# Records a wrapped session in each host the portability list names, types one
# Diple gesture into it where R-014 calls the host driven, and checks the
# capture. Run it from the repository root:
#
#   scripts/verify-hosts.sh [--plain] [--manual] [--evidence <dir>] [--baseline] [--copy] [host...]
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
#
# --copy is S-017's copy check instead: a canned agent draws a reply holding
# every case R-015 names, and each copy action is made on it in turn, the
# clipboard read back after each and compared with the text the selection
# shows. A host nothing can type into gets each step from the person at the
# keyboard.
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
copy=""
while :; do
	case "${1:-}" in
	--plain) plain="--plain"; shift ;;
	--manual) manual="1"; shift ;;
	--evidence) evidence="${2:?--evidence needs a directory}"; shift 2 ;;
	--baseline) baseline="1"; shift ;;
	--copy) copy="1"; shift ;;
	*) break ;;
	esac
done
# How long the canned agent holds the session: long enough for the gesture, or
# for a person to make every copy the copy check asks for.
seconds=25
[ -z "$copy" ] || seconds=900
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
if [ -n "$copy" ]; then
	cp "$src/scripts/fake-copy-agent.sh" "$agent"
else
	cp "$src/scripts/fake-agent.sh" "$agent"
fi
chmod +x "$agent"

capture_for() { printf '%s/%s%s.capture.jsonl' "$out" "$1" "${plain:+.plain}"; }
pid_for() { printf '%s/%s.pid' "$ctl" "$1"; }
# wrapped runs the session in a directory of its own. The adapter looks for the
# transcript of the session it is wrapping under the working directory, and a
# checkout that has been worked in with the real agent has transcripts there:
# aligning the canned agent's rows against one of those would leave every block
# without rows, and nothing to raise, for a reason that has nothing to do with
# the host. Diple is exec'd, as a shell execs the agent a user types, so the
# pane's process is the agent's persona and not a shell left waiting on it:
# macOS tmux names a pane after its process group's leader, and a bash that
# runs this line without exec'ing is that leader. The shell writes its pid just
# before it execs: exec keeps the pid through Diple and through the persona Diple
# re-executes itself as, so it is the session's pid whatever argv the pane shows.
wrapped() {
	local fmt='cd %s && export PATH=%s:$PATH DIPLE_FAKE_AGENT_SECONDS=%s%s'
	fmt+=' && echo $$ >%s && exec %s --record %s %s claude'
	local extra=""
	if [ -n "$copy" ]; then
		# The copy check's agent writes a transcript for Diple to find, into
		# a home of the run's own, never the user's.
		extra=" HOME=$ctl/home-$1 DIPLE_COPY_DIR=$ctl/copy-$1"
	fi
	printf "$fmt" \
		"$ctl" "$agent_dir" "${DIPLE_FAKE_AGENT_SECONDS:-$seconds}" "$extra" "$(pid_for "$1")" \
		"$diple" "$(capture_for "$1")" "$plain"
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
# write when it closes. The session is known by the pid its shell wrote, never
# by its arguments: Diple re-executes itself through the agent's persona, whose
# argv names only the agent, so a match on `--record` found nothing and let the
# check read a capture still being written. It returns 1 when the session is
# still running as the wait gives up, and 2 when no pid was written.
wait_for_settled() {
	local pid tries=0
	pid="$(cat "$1" 2>/dev/null)"
	[ -n "$pid" ] || return 2
	# ps rather than kill -0: a session that has exited but not yet been
	# reaped by its host is a zombie, and kill -0 still succeeds on it.
	while :; do
		case "$(ps -o stat= -p "$pid" 2>/dev/null)" in
		'' | Z*) return 0 ;;
		esac
		[ "$tries" -lt 120 ] || return 1
		sleep 1
		tries=$((tries + 1))
	done
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
	tmux)
		local name
		name="$(tmux display-message -p -t diple-verify '#{pane_current_command}' 2>/dev/null)"
		printf 'name=%s\n' "$name" >"$ctl/identity"
		;;
	herdr)
		herdr agent list 2>/dev/null |
			"$ctl/hostcheck" herdr-pane --pane "${2:-}" --name diple-verify >"$ctl/identity"
		;;
	esac
}

# start_host returns 0 when the session was started and, for a driven host,
# the gesture was handed to it; 3 when the host is not installed; 4 when it is
# installed but could not be started or driven.
start_host() {
	local host="$1" cmd launcher pane bundle
	cmd="$(wrapped "$host")"
	rm -f "$(capture_for "$host")" "$(pid_for "$host")"
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
		drive tmux || return 4
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
			drive herdr || return 4
		else
			herdr agent start diple-verify --cwd "$root" --no-focus -- sh -c "$cmd" >/dev/null 2>&1 || return 4
			sleep 4
			host_identity herdr
			drive herdr || return 4
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
		printf '%s\n' "$pane" >"$ctl/wezterm.pane"
		sleep 4
		drive wezterm || return 4
		;;
	kitty)
		command -v kitty >/dev/null || return 3
		kitty -o allow_remote_control=yes --listen-on "unix:$ctl/kitty.sock" --detach sh -c "$cmd" || return 4
		sleep 5
		[ -S "$ctl/kitty.sock" ] || return 4
		drive kitty || return 4
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

# host_send types one piece of input into the wrapped session of a driven
# host, through the host's own control interface.
host_send() {
	case "$1" in
	tmux) tmux send-keys -t diple-verify -l -- "$2" ;;
	herdr)
		if [ -s "$ctl/herdr.pane" ]; then
			herdr pane send-text "$(cat "$ctl/herdr.pane")" "$2" >/dev/null 2>&1
		else
			herdr agent send diple-verify "$2" >/dev/null 2>&1
		fi
		;;
	wezterm)
		printf '%s' "$2" |
			wezterm cli send-text --no-paste --pane-id "$(cat "$ctl/wezterm.pane")" >/dev/null 2>&1
		;;
	kitty) kitty @ --to "unix:$ctl/kitty.sock" send-text -- "$2" >/dev/null 2>&1 ;;
	*) return 1 ;;
	esac
}

# drive hands a driven host the gesture, one part at a time, since the dwell
# that raises a block is real time. The copy check makes its own gestures.
drive() {
	[ -z "$copy" ] || return 0
	local part
	for part in "${gesture_parts[@]}"; do
		host_send "$1" "$part" || return 1
		sleep 1
	done
}

# The clipboard the copy check reads back, and writes a sentinel to before each
# copy so that what it reads is proved to be the copy's.
clip_read() {
	if command -v pbpaste >/dev/null; then
		pbpaste
	elif [ -n "${WAYLAND_DISPLAY:-}" ] && command -v wl-paste >/dev/null; then
		wl-paste -n 2>/dev/null
	elif command -v xclip >/dev/null; then
		xclip -o -selection clipboard 2>/dev/null
	fi
}

clip_write() {
	if command -v pbcopy >/dev/null; then
		printf '%s' "$1" | pbcopy
	elif [ -n "${WAYLAND_DISPLAY:-}" ] && command -v wl-copy >/dev/null; then
		printf '%s' "$1" | wl-copy
	elif command -v xclip >/dev/null; then
		printf '%s' "$1" | xclip -i -selection clipboard
	fi
}

# SGR mouse reports at 1-based columns and rows: a press, its release, a drag
# with the button down, and the pointer resting with none.
m_press() { printf '\033[<0;%s;%sM' "$1" "$2"; }
m_release() { printf '\033[<0;%s;%sm' "$1" "$2"; }
m_drag() { printf '\033[<32;%s;%sM' "$1" "$2"; }
m_rest() { printf '\033[<35;%s;%sM' "$1" "$2"; }
m_click() { printf '%s%s' "$(m_press "$1" "$2")" "$(m_release "$1" "$2")"; }

# copy_step makes one copy and reads it back. A driven host is typed the parts;
# "pause" waits for a raise and "hold" for output to arrive under a drag. By
# hand, the person gets the instruction and says when it is done.
copy_step() {
	local name="$1" want got part
	want="$(cat "$copy_dir/expect-$2")"
	local instruction="$3"
	shift 3
	clip_write "diple copy check sentinel"
	if [ -n "$by_hand" ]; then
		printf '\n  %s, %s: %s\n  Then press Enter here.\n' "$host" "$name" "$instruction" >&2
		read -r _ </dev/tty || true
	else
		for part in "$@"; do
			case "$part" in
			pause) sleep 0.5 ;;
			hold) sleep 5 ;;
			*) host_send "$host" "$part"; sleep 0.05 ;;
			esac
		done
	fi
	sleep 1
	got="$(clip_read)"
	if [ "$got" = "$want" ]; then
		echo "PASS $host copy $name"
	else
		echo "FAIL $host copy $name: got $(printf '%q' "$got"), want $(printf '%q' "$want")"
		copy_fails=$((copy_fails + 1))
	fi
}

# copy_check runs every copy action over every text case on the canned reply.
copy_check() {
	local host="$1" copy_dir="$ctl/copy-$1" by_hand="$manual" tries=0 copy_fails=0
	case "$host" in
	tmux | herdr | wezterm | kitty) ;;
	*) by_hand=1 ;;
	esac
	while [ ! -e "$copy_dir/ready" ] && [ "$tries" -lt 30 ]; do
		sleep 1
		tries=$((tries + 1))
	done
	if [ ! -e "$copy_dir/ready" ]; then
		echo "FAIL $host: the copy agent never drew its reply"
		return 1
	fi
	sleep 3 # Diple finds the transcript and aligns the reply to it
	local cols marker item1 item2 item3 item3last cjk hard cmd tool
	# shellcheck disable=SC1090,SC1091
	. "$copy_dir/layout"
	local w="$cols" n cmdlast strip=$((item3last + 1))
	n=$(wc -m <"$copy_dir/expect-cmd")
	cmdlast=$((cmd + (n + 2 + cols - 1) / cols - 1))
	[ -z "$by_hand" ] || printf '\n  The copy check in %s: make each copy in its window.\n' "$host" >&2
	copy_step drag item1 "drag across the row '1. Use bold' from the left edge to the right" \
		"$(m_press 1 "$item1")" "$(m_drag "$w" "$item1")" "$(m_release "$w" "$item1")"
	copy_step after-list-marker item1-tail "drag from the U of 'Use' on that row to the right edge" \
		"$(m_press 6 "$item1")" "$(m_drag "$w" "$item1")" "$(m_release "$w" "$item1")"
	copy_step turn-marker marker "drag across 'Copy check' from the left edge, over the marker" \
		"$(m_press 1 "$marker")" "$(m_drag "$w" "$marker")" "$(m_release "$w" "$marker")"
	copy_step double-press word "double-click the word 'bold'" \
		"$(m_click 11 "$item1")" "$(m_click 11 "$item1")"
	copy_step triple-press item2 "triple-click the row '2. See the docs first'" \
		"$(m_click 8 "$item2")" "$(m_click 8 "$item2")" "$(m_click 8 "$item2")"
	copy_step strip-copy item3 "rest on item 3 until it lifts, then click copy on its strip" \
		"$(m_rest 8 "$item3")" pause "$(m_click 18 "$strip")"
	copy_step c-key cjk "rest the pointer on the row '漢字 café' until it lifts, then press c" \
		"$(m_rest 4 "$cjk")" pause c
	copy_step hard-break hard "drag from the start of 'First line' to the end of 'second line'" \
		"$(m_press 1 "$hard")" "$(m_drag "$w" $((hard + 1)))" "$(m_release "$w" $((hard + 1)))"
	copy_step soft-wrap cmd "drag from the start of the long echo command to the end of its last row" \
		"$(m_press 1 "$cmd")" "$(m_drag "$w" "$cmdlast")" "$(m_release "$w" "$cmdlast")"
	copy_step unaligned tool "drag from the start of 'ran a tool' to the end of 'and printed this'" \
		"$(m_press 1 "$tool")" "$(m_drag "$w" $((tool + 1)))" "$(m_release "$w" $((tool + 1)))"
	: >"$copy_dir/go"
	copy_step during-output item1 \
		"press at the start of '1. Use bold', hold while output appears, drag to the right edge" \
		"$(m_press 1 "$item1")" "$(m_drag "$w" "$item1")" hold "$(m_release "$w" "$item1")"
	[ "$copy_fails" -eq 0 ]
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
	if [ -n "$copy" ]; then
		copy_check "$host" || status=1
		wait_for_settled "$(pid_for "$host")" || true
		continue
	fi
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
	wait_for_settled "$(pid_for "$host")"
	case "$?" in
	1)
		echo "FAIL $host: the session was still writing its capture after 120 seconds"
		status=1
		continue
		;;
	2)
		echo "FAIL $host: the session wrote no pid, so its end could not be awaited"
		status=1
		continue
		;;
	esac
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
