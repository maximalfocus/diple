#!/usr/bin/env bash
# Checks R-017 with a real agent CLI: the host names a pane running the agent
# through Diple exactly as it names a pane running the bare CLI, and `herdr`
# reports the same idle, working and blocked states for both. Run it from the
# repository root on a machine where the agent is installed and logged in:
#
#   scripts/verify-identity.sh [--cards] [--env KEY=VALUE]... [--block <prompt>] [--cwd <dir>] <herdr|tmux> <agent> [-- <agent args>]
#
# It builds Diple from this checkout and shims the agent in a directory of its
# own, so the user's own shims, startup files and Diple are never touched: the
# bare run gets a PATH with every Diple shim directory removed, and the
# wrapped run gets only this build's shim ahead of it. --cards adds three free
# cards to the wrapped session's tray before the states are checked, so the
# host is shown to name the agent with Diple drawing in its pane.
#
# The prompts cost a few tokens of the agent's model. The blocked state is
# reached by asking for a harmless command with a side effect, which an agent
# asks approval for, and the question is declined; --block replaces the prompt.
# An agent that runs commands without asking is asked to ask for this run only,
# through its own arguments or --env, leaving the user's configuration alone:
#   claude:   -- --permission-mode default   (it may start in auto mode)
#   codex:    -- --sandbox read-only --ask-for-approval on-request
#   opencode: --env 'OPENCODE_CONFIG_CONTENT={"permission":{"bash":"ask"}}'
#
# --cwd runs the agents in a directory they already trust, which the build
# never depends on; it defaults to this checkout.
#
# The check: in every phase the wrapped pane is named as the agent was typed
# (a CLI behind a version-numbered file, or run by a runtime such as node, is
# named for the agent, not the file or the runtime), or, for an identity-only
# agent, exactly as the bare pane; and it reports the same state as the bare
# pane. The bare pane's name is recorded for comparison, not judged: without
# Diple, macOS tmux names claude by its version file and pi as node. A state the host cannot
# report for the bare CLI either (herdr falls back to idle for an agent it has
# no rule for) is listed as not reached, on both sides.
# Empty arrays are expanded as ${a[@]+"${a[@]}"} throughout: macOS still
# ships bash 3.2, where "${a[@]}" of an empty array is unbound under set -u.
set -uo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
work="$root"
cards=""
extra_env=()
blocking_prompt='Use your shell tool to run exactly this command and nothing else: touch /tmp/diple-identity-probe' 
while :; do
	case "${1:-}" in
	--cards) cards=1; shift ;;
	--env) extra_env+=("${2:?--env needs KEY=VALUE}"); shift 2 ;;
	--block) blocking_prompt="${2:?--block needs a prompt}"; shift 2 ;;
	--cwd) work="${2:?--cwd needs a directory}"; shift 2 ;;
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
	for p in ${panes[@]+"${panes[@]}"}; do
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
for a in ${agent_args[@]+"${agent_args[@]}"}; do quoted_args+=" $(printf '%q' "$a")"; done
quoted_env=""
for e in ${extra_env[@]+"${extra_env[@]}"}; do quoted_env+=" $(printf '%q' "$e")"; done

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
		tmux new-session -d -s "$label" -x 120 -y 36 -c "$work" "$cmd" || return 1
		echo "$label"
		;;
	herdr)
		if [ -n "$herdr_v08" ]; then
			pane="$(herdr tab create --cwd "$work" --label "$label" 2>/dev/null |
				sed -n 's/.*"root_pane":{[^}]*"pane_id":"\([^"]*\)".*/\1/p')"
			[ -n "$pane" ] || return 1
			herdr pane run "$pane" "$cmd" >/dev/null 2>&1 || return 1
			echo "$pane"
		else
			# herdr 0.7 names the agent after the label only until the pane's
			# occupant changes, so the pane is followed by its id.
			pane="$(herdr agent start "$label" --cwd "$work" --no-focus -- sh -c "$cmd" 2>/dev/null |
				sed -n 's/.*"pane_id":"\([^"]*\)".*/\1/p')"
			[ -n "$pane" ] || return 1
			echo "$pane"
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
		line="$(herdr agent list 2>/dev/null | "$ctl/hostcheck" herdr-pane --pane "$1")"
		echo "${line:-name= state=none}"
		;;
	esac
}

# type sends text to the pane; key sends one control sequence. herdr's pane
# commands are used on 0.7 too: `agent send` there did not reliably reach the
# agent as typed input.
type_text() {
	case "$host" in
	tmux) tmux send-keys -t "$1" -l -- "$2" ;;
	herdr) herdr pane send-text "$1" "$2" >/dev/null 2>&1 ;;
	esac
}
enter() { type_text "$1" $'\r'; }
escape() { type_text "$1" $'\033'; }

# prompt submits a prompt to the agent. herdr 0.8 pastes it the way the agent
# expects and sends Enter; typed text followed by a carriage return can land
# in an agent's composer as a newline instead of submitting it.
prompt() {
	if [ "$host" = herdr ] && [ -n "$herdr_v08" ]; then
		herdr agent prompt "$1" "$2" >/dev/null 2>&1
	elif [ "$host" = herdr ]; then
		# On herdr 0.7 the text is typed and a typed carriage return submits
		# it; its `pane send-keys enter` does not.
		type_text "$1" "$2"
		sleep 0.3
		enter "$1"
	else
		type_text "$1" "$2"
		enter "$1"
	fi
}

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
		# Saving a card leaves focus on the tray, where Enter would send it;
		# Tab gives focus back to the agent's own box before the prompts.
		type_text "$pane" $'\t'
		sleep 1
		echo "$mode cards $(identity "$pane")"
	fi
	prompt "$pane" "Count from 1 to 40, one number per line."
	if await "$pane" working 30; then echo "$mode working $(identity "$pane")"; else echo "$mode working $(identity "$pane") (never working)"; fi
	await "$pane" idle 120 >/dev/null
	prompt "$pane" "$blocking_prompt"
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

# Parity: in every phase the wrapped pane is named as the agent and reports
# the state the bare pane reported. The states either side reached are listed.
status=0
state_of() { sed -n "s/^$1 $2 .*state=\([^ ]*\).*/\1/p" "$3" | head -1; }
name_of() { sed -n "s/^$1 $2 .*name=\([^ ]*\).*/\1/p" "$3" | head -1; }
for phase in $(awk '{print $2}' "$report.bare" "$report.wrapped" | sort -u); do
	b="$(name_of bare "$phase" "$report.bare")"
	w="$(name_of wrapped "$phase" "$report.wrapped")"
	# A pane the host never listed says nothing about its name.
	for side in "bare:$b" "wrapped:$w"; do
		if [ -z "${side#*:}" ] && [ -n "$(sed -n "/^${side%%:*} $phase /p" "$report.${side%%:*}")" ]; then
			echo "FAIL ${side%%:*} $phase: the host did not list the pane at all"
			status=1
		fi
	done
	if [ -n "$(sed -n "/^wrapped $phase /p" "$report.wrapped")" ] && [ "$w" != "$agent" ] && [ "$w" != "$b" ]; then
		echo "FAIL wrapped $phase: the host named the pane '$w', neither '$agent' nor the bare CLI's '$b'"
		status=1
	fi
	[ "$phase" = cards ] && continue
	b="$(state_of bare "$phase" "$report.bare")"
	w="$(state_of wrapped "$phase" "$report.wrapped")"
	if [ "$b" != "$w" ]; then
		echo "FAIL $phase: the bare pane reported '$b', the wrapped one '$w'"
		status=1
	fi
done
reached="$(grep -v '(never' "$report.wrapped" | awk '{print $2}' | grep -v -e ready -e cards | tr '\n' ' ')"
missed="$(grep '(never' "$report.bare" | awk '{print $2}' | tr '\n' ' ')"
echo "states reached through Diple: ${reached:-none}${missed:+; not reached by the bare CLI either: $missed}"
[ "$status" = 0 ] && echo "PASS $host $agent: the host names and reports the wrapped pane as the bare one${cards:+, three cards in its tray}"
exit "$status"
