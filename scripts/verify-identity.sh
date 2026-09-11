#!/usr/bin/env bash
# Checks R-017 with a real agent CLI: the host names a pane running the agent
# through Diple exactly as it names a pane running the bare CLI, and `herdr`
# reports the same idle, working and blocked states for both. Run it from the
# repository root on a machine where the agent is installed and logged in:
#
#   scripts/verify-identity.sh [--cards] [--env KEY=VALUE]... <herdr|tmux> <agent> [-- <agent args>]
#
# It builds Diple from this checkout and shims the agent in a directory of its
# own, so the user's own shims, startup files and Diple are never touched: the
# bare run gets a PATH with every Diple shim directory removed, and the
# wrapped run gets only this build's shim ahead of it. --cards adds three free
# cards to the wrapped session's tray before the states are checked, so the
# host is shown to name the agent with Diple drawing in its pane.
#
# The prompts cost a few tokens of the agent's model. The blocked state is
# reached by asking for a network command and the question is declined. An
# agent that runs commands without asking by default is asked to ask for this
# run only, through --env: for opencode,
#   --env 'OPENCODE_CONFIG_CONTENT={"permission":{"bash":"ask"}}'
# which leaves the user's own configuration untouched.
set -uo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cards=""
extra_env=()
while :; do
	case "${1:-}" in
	--cards) cards=1; shift ;;
	--env) extra_env+=("${2:?--env needs KEY=VALUE}"); shift 2 ;;
	*) break ;;
	esac
done
host="${1:?usage: verify-identity.sh [--cards] <herdr|tmux> <agent> [-- <agent args>]}"
agent="${2:?usage: verify-identity.sh [--cards] <herdr|tmux> <agent> [-- <agent args>]}"
shift 2
[ "${1:-}" = "--" ] && shift
agent_args=("$@")

ctl="$(mktemp -d "${TMPDIR:-/tmp}/diple-id.XXXXXX")"
panes=()
finish() {
	for p in "${panes[@]}"; do
		case "$host" in
		tmux) tmux kill-session -t "$p" 2>/dev/null ;;
		herdr) herdr pane close "$p" >/dev/null 2>&1 ;;
		esac
	done
	rm -rf "$ctl"
}
trap finish EXIT

(cd "$root" && go build -o "$ctl/diple" ./cmd/diple) || exit 1

# The bare PATH: every directory holding one of Diple's shims is dropped, so
# the agent resolves to the real CLI whatever the user has installed.
bare_path=""
IFS=: read -r -a dirs <<<"$PATH"
for d in "${dirs[@]}"; do
	[ -n "$d" ] || continue
	if [ -f "$d/$agent" ] && grep -q '# diple shim for ' "$d/$agent" 2>/dev/null; then
		continue
	fi
	bare_path="${bare_path:+$bare_path:}$d"
done
PATH="$bare_path" command -v "$agent" >/dev/null || { echo "SKIP $agent: not installed on this machine"; exit 3; }

# This build's shim, in a directory and a HOME of its own.
mkdir -p "$ctl/home"
HOME="$ctl/home" XDG_DATA_HOME="$ctl/data" PATH="$bare_path" "$ctl/diple" on "$agent" >/dev/null || exit 1
wrap_path="$ctl/data/diple/bin:$bare_path"

quoted_args=""
for a in "${agent_args[@]}"; do quoted_args+=" $(printf '%q' "$a")"; done
quoted_env=""
for e in "${extra_env[@]}"; do quoted_env+=" $(printf '%q' "$e")"; done
blocking_prompt='Run this exact shell command and show me its first line of output: curl -sI https://example.com'

# --- host primitives ------------------------------------------------------

# herdr's control interface changed at 0.8; 0.7 starts a command as a named
# agent in a pane of its own.
# `herdr tab` alone prints its help and exits non-zero, so the help is read
# first and matched after.
herdr_v08=""
if [ "$host" = herdr ]; then
	tab_help="$(herdr tab 2>&1 || true)"
	case "$tab_help" in *"tab create"*) herdr_v08=1 ;; esac
fi

# start runs cmd in a new pane and prints the handle later calls take.
start() {
	local label="$1" cmd="$2" pane
	case "$host" in
	tmux)
		tmux new-session -d -s "$label" -x 120 -y 36 -c "$root" "$cmd" || return 1
		echo "$label"
		;;
	herdr)
		if [ -n "$herdr_v08" ]; then
			pane="$(herdr tab create --cwd "$root" --label "$label" 2>/dev/null |
				sed -n 's/.*"root_pane":{[^}]*"pane_id":"\([^"]*\)".*/\1/p')"
			[ -n "$pane" ] || return 1
			herdr pane run "$pane" "$cmd" >/dev/null 2>&1 || return 1
			echo "$pane"
		else
			herdr agent start "$label" --cwd "$root" --no-focus -- sh -c "$cmd" >/dev/null 2>&1 || return 1
			echo "$label"
		fi
		;;
	esac
}

# identity prints `name=<n> state=<s>` for a pane.
identity() {
	case "$host" in
	tmux) printf 'name=%s state=-\n' "$(tmux display-message -p -t "$1" '#{pane_current_command}' 2>/dev/null)" ;;
	herdr)
		local line
		if [ -n "$herdr_v08" ]; then
			line="$(herdr agent list 2>/dev/null | "$ctl/hostcheck" herdr-pane --pane "$1")"
		else
			line="$(herdr agent list 2>/dev/null | "$ctl/hostcheck" herdr-pane --name "$1")"
		fi
		echo "${line:-name= state=none}"
		;;
	esac
}

# type sends text to the pane; key sends one control sequence.
type_text() {
	case "$host" in
	tmux) tmux send-keys -t "$1" -l -- "$2" ;;
	herdr) if [ -n "$herdr_v08" ]; then herdr pane send-text "$1" "$2" >/dev/null 2>&1; else herdr agent send "$1" "$2" >/dev/null 2>&1; fi ;;
	esac
}
enter() { type_text "$1" $'\r'; }
escape() { type_text "$1" $'\033'; }

# await polls until the pane reaches state, up to seconds. herdr reports an
# agent that finished while its tab was not in view as `done`, the same idle
# state not yet seen, so `done` counts as idle.
await() {
	local pane="$1" want="$2" secs="$3" i=0 now
	while [ "$i" -lt $((secs * 2)) ]; do
		now="$(identity "$pane")"
		case "$now" in *"state=$want"*) return 0 ;; esac
		[ "$want" = idle ] && case "$now" in *"state=done"*) return 0 ;; esac
		sleep 0.5
		i=$((i + 1))
	done
	return 1
}

go_build_hostcheck() { (cd "$root" && go build -o "$ctl/hostcheck" ./internal/hostcheck) || exit 1; }
go_build_hostcheck

# --- one run --------------------------------------------------------------

# run drives one pane through the states and prints one line per state seen.
run() {
	local mode="$1" path="$2" pane label="diple-id-$1-$$"
	pane="$(start "$label" "env PATH=$path$quoted_env $agent$quoted_args")" || { echo "$mode start FAIL"; return; }
	panes+=("$pane")
	sleep 6
	if [ "$host" = tmux ]; then
		echo "$mode ready $(identity "$pane")"
		return
	fi
	# A first-run question (trusting the directory, say) is answered with its
	# default before the states are read.
	if ! await "$pane" idle 45; then
		enter "$pane"
		await "$pane" idle 30 || { echo "$mode ready $(identity "$pane") (never idle)"; return; }
	fi
	echo "$mode idle $(identity "$pane")"
	if [ "$mode" = wrapped ] && [ -n "$cards" ]; then
		for n in one two three; do
			type_text "$pane" $'\033n'
			sleep 0.5
			type_text "$pane" "card $n"
			enter "$pane"
			sleep 0.5
		done
		sleep 1
		echo "$mode cards $(identity "$pane")"
	fi
	type_text "$pane" "Count from 1 to 40, one number per line."
	enter "$pane"
	if await "$pane" working 30; then echo "$mode working $(identity "$pane")"; else echo "$mode working $(identity "$pane") (never working)"; fi
	await "$pane" idle 120 >/dev/null
	type_text "$pane" "$blocking_prompt"
	enter "$pane"
	if await "$pane" blocked 120; then echo "$mode blocked $(identity "$pane")"; else echo "$mode blocked $(identity "$pane") (never blocked)"; fi
	escape "$pane"
	await "$pane" idle 60 >/dev/null
}

echo "host $host $( [ "$host" = herdr ] && herdr --version | awk '{print $2}' || tmux -V | awk '{print $2}') agent $agent${cards:+ with three cards}"
report="$ctl/report"
# Not in a pipeline: run records the panes it opens, so finish can close them.
run bare "$bare_path" >"$report.bare"
cat "$report.bare"
run wrapped "$wrap_path" >"$report.wrapped"
cat "$report.wrapped"

# The wrapped pane must be named as the agent in every state the bare pane
# reached, and reach the same states.
status=0
while read -r mode phase rest; do
	name="$(printf '%s\n' "$rest" | sed -n 's/.*name=\([^ ]*\).*/\1/p')"
	[ "$name" = "$agent" ] || { echo "FAIL $mode $phase: the host named the pane '${name}', not '$agent'"; status=1; }
	case "$rest" in *"(never"*) echo "FAIL $mode $phase: $rest"; status=1 ;; esac
done < <(cat "$report.bare" "$report.wrapped")
[ "$status" = 0 ] && echo "PASS $host $agent: the host names the wrapped pane as the bare one${cards:+, three cards in its tray}"
exit "$status"
