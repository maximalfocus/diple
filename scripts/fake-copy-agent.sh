#!/bin/sh
# A canned agent for `scripts/verify-hosts.sh --copy`. It writes the transcript
# Claude Code writes for one reply, so Diple aligns the reply as it would a
# real one; draws that reply the way Claude Code draws it, wrapped at the
# terminal's own width; and files, for every copy the check makes, the row to
# point at and exactly the text the copy must carry. It makes no model calls
# and needs no credentials. DIPLE_COPY_DIR is where it files them.
set -u
dir="${DIPLE_COPY_DIR:?DIPLE_COPY_DIR is not set}"
mkdir -p "$dir"
cols=$(stty size </dev/tty 2>/dev/null | awk '{print $2}')
[ -n "$cols" ] || cols=80

# The characters the check needs byte for byte: é drawn as e and a combining
# acute, and a woman-technologist emoji joined by a zero-width joiner.
acute=$(printf '\314\201')
zwj=$(printf '\342\200\215')
woman=$(printf '\360\237\221\251')
laptop=$(printf '\360\237\222\273')
cjk="漢字 cafe${acute} ${woman}${zwj}${laptop} end"
long3="Read the config file and note the two ports that the service listens on for HTTP"
long3="$long3 and metrics, then change the handler and verify it"
# A command wider than the terminal, which the terminal wraps, with literal
# double spaces that must survive the copy.
cmd="echo"
while [ ${#cmd} -lt $((cols + 20)) ]; do
	cmd="$cmd a  b"
done

# The transcript, where Claude Code keeps it for this directory.
ts=$(date -u +%Y-%m-%dT%H:%M:%S.000Z)
sid="copy-check-$$"
proj="$HOME/.claude/projects/$(pwd -P | sed 's/[^A-Za-z0-9]/-/g')"
mkdir -p "$proj"
md="Copy check\\n\\n1. Use **bold** and \`code\` here"
md="$md\\n2. See [the docs](https://example.com/a) first"
md="$md\\n3. $long3\\n\\n$cjk\\n\\nFirst line  \\nsecond line"
md="$md\\n\\n\`\`\`sh\\n$cmd\\n\`\`\`"
{
	printf '{"type":"user","version":"2.1.268","sessionId":"%s","timestamp":"%s",' "$sid" "$ts"
	printf '"message":{"role":"user","content":"copy check"}}\n'
	printf '{"type":"assistant","version":"2.1.268","sessionId":"%s","timestamp":"%s",' "$sid" "$ts"
	printf '"message":{"id":"msg_copy_check","role":"assistant",'
	printf '"content":[{"type":"text","text":"%s"}]}}\n' "$md"
} >"$proj/$sid.jsonl"

# The reply as Claude Code draws it: the turn marker, list items indented two
# columns with their text wrapped under it, code without its fences.
row=0
line() {
	printf '%s\r\n' "$1"
	row=$((row + 1))
}
printf '\033[H\033[2J'
line "$(printf '\033[1m⏺\033[0m Copy check')"
marker=$row
line ""
line "$(printf '  1. Use \033[1mbold\033[0m and \033[36mcode\033[0m here')"
item1=$row
line "$(printf '  2. See \033[4mthe docs\033[0m first')"
item2=$row
text="  3."
item3=$((row + 1))
for w in $long3; do
	if [ $((${#text} + 1 + ${#w})) -gt "$cols" ]; then
		line "$text"
		text="    "
	fi
	text="$text $w"
done
line "$text"
item3last=$row
line ""
line "  $cjk"
cjkrow=$row
line ""
line "  First line"
hard=$row
line "  second line"
line ""
printf '  %s\r\n' "$cmd"
cmdrow=$((row + 1))
row=$((row + (${#cmd} + 2 + cols - 1) / cols))
line ""
line "  ran a tool"
tool=$row
line "  and printed this"
line ""

# Where to point, and what each copy must carry.
cat >"$dir/layout" <<EOF
cols=$cols
marker=$marker
item1=$item1
item2=$item2
item3=$item3
item3last=$item3last
cjk=$cjkrow
hard=$hard
cmd=$cmdrow
tool=$tool
EOF
put() { printf '%s' "$2" >"$dir/expect-$1"; }
put item1 "1. Use bold and code here"
put item1-tail "Use bold and code here"
put item2 "2. See the docs first"
put word "bold"
put item3 "3. $long3"
put cjk "$cjk"
put marker "Copy check"
put hard "First line
second line"
put cmd "$cmd"
put tool "  ran a tool
  and printed this"
: >"$dir/ready"

# Output that arrives while a selection is being made, once the check asks.
waited=0
while [ ! -e "$dir/go" ] && [ $waited -lt "${DIPLE_FAKE_AGENT_SECONDS:-300}" ]; do
	sleep 1
	waited=$((waited + 1))
done
if [ -e "$dir/go" ]; then
	for n in 1 2 3 4; do
		sleep 1
		printf '  more output %s\r\n' "$n"
	done
fi
sleep "${DIPLE_COPY_LINGER:-5}"
printf '\r\n'
