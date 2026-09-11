#!/usr/bin/env bash
# Host evidence: the tier-5 reports `scripts/verify-hosts.sh --evidence` writes,
# carried from a developer's machine to CI through this private repository.
#
#   scripts/evidence.sh upload <dir>   push a bundle and print its Host-Evidence line
#   scripts/evidence.sh check          in CI: verify a pull request's evidence
#   scripts/evidence.sh prune <sha>    delete the bundles uploaded for a head
#
# A bundle is a commit with no parent under refs/evidence/<sha>/<stamp>, outside
# every branch, so no commit ever has to contain its own SHA. Its identifier is
# the ref and the commit's object id, so what CI reads is exactly what was
# uploaded. A developer uploads with their own push access; CI reads with the
# workflow's GITHUB_TOKEN, which holds contents: read on this repository and
# nothing else. Neither credential is written into a report.
#
# Retention: a bundle stays until its pull request has landed, and a report is
# evidence for fourteen days after it was recorded; after either it is pruned
# or it has expired, and the head needs a fresh run and upload.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

upload() {
	local dir="${1:?usage: evidence.sh upload <dir>}" sha gitdir idx tree commit ref
	dir="$(cd "$dir" && pwd)"
	ls "$dir"/*.report.json >/dev/null 2>&1 || { echo "$dir holds no reports" >&2; exit 2; }
	sha="$(sed -n 's/.*"sha": *"\([0-9a-f]\{40\}\)".*/\1/p' "$dir"/*.report.json | sort -u)"
	[ "$(printf '%s\n' "$sha" | grep -c .)" = 1 ] || {
		echo "a bundle is for exactly one head; its reports name: $sha" >&2
		exit 2
	}
	gitdir="$(git rev-parse --absolute-git-dir)"
	idx="$(mktemp)"
	rm -f "$idx"
	GIT_INDEX_FILE="$idx" git --git-dir="$gitdir" --work-tree="$dir" -C "$dir" add -f -A .
	tree="$(GIT_INDEX_FILE="$idx" git --git-dir="$gitdir" write-tree)"
	rm -f "$idx"
	commit="$(git commit-tree -m "host evidence for $sha" "$tree")"
	ref="refs/evidence/$sha/$(date -u +%Y%m%dT%H%M%SZ)-$$"
	git push -q origin "$commit:$ref"
	printf 'Host-Evidence: %s %s\n' "$ref" "$commit"
}

# check reads the pull request from the environment the workflow sets: EVENT,
# BASE and HEAD, and BODY, the pull request's description.
check() {
	local hc class n=0 status=0 ref oid got d
	if [ "${EVENT:-}" != pull_request ]; then
		echo "host evidence: not a pull request, so there is no head to bind it to"
		return 0
	fi
	: "${BASE:?BASE is the pull request base SHA}" "${HEAD:?HEAD is the pull request head SHA}"
	hc="$(mktemp -d)"
	go build -o "$hc/hostcheck" ./internal/hostcheck
	class="$(git diff --name-only --no-renames "$BASE...$HEAD" | "$hc/hostcheck" classify)"
	echo "change: $class"
	[ "$class" != docs-only ] || return 0

	local dirs=()
	while read -r ref oid; do
		n=$((n + 1))
		if ! git fetch -q origin "+$ref:refs/fetched-evidence/$n" 2>/dev/null; then
			echo "FAIL evidence $ref: missing or unreadable; upload it again" >&2
			status=1
			continue
		fi
		got="$(git rev-parse "refs/fetched-evidence/$n")"
		if [ "$got" != "$oid" ]; then
			echo "FAIL evidence $ref: it is $got, not $oid; upload it again" >&2
			status=1
			continue
		fi
		d="$hc/bundle-$n"
		mkdir -p "$d"
		git archive "$oid" | tar -x -C "$d"
		dirs+=("$d")
	done < <(printf '%s\n' "${BODY:-}" | tr -d '\r' |
		sed -n 's/^[[:space:]]*Host-Evidence:[[:space:]]*\(refs\/evidence\/[^[:space:]]*\)[[:space:]][[:space:]]*\([0-9a-f]\{40\}\)[[:space:]]*$/\1 \2/p')

	"$hc/hostcheck" evidence --head "$HEAD" ${dirs[@]+"${dirs[@]}"} || status=1
	return "$status"
}

prune() {
	local sha="${1:?usage: evidence.sh prune <sha>}" ref
	git ls-remote origin "refs/evidence/$sha/*" | awk '{print $2}' | while read -r ref; do
		git push -q origin --delete "$ref" && echo "pruned $ref"
	done
}

case "${1:-}" in
upload) shift; upload "$@" ;;
check) shift; check "$@" ;;
prune) shift; prune "$@" ;;
*)
	echo "usage: evidence.sh upload <dir> | check | prune <sha>" >&2
	exit 64
	;;
esac
