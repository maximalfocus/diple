#!/usr/bin/env bash
# The maintainer's landing check while the repository cannot enforce one. It
# refuses unless the `gate` check on the pull request's current head has
# succeeded and, for a change outside the documentation allowlist, the body
# records a passing macOS acceptance run of that same head:
#
#   macOS-Acceptance: <head sha> PASS
#
# A red, pending or missing gate, or a missing, failed or stale macOS run,
# refuses the landing.
#
#   scripts/gate-status.sh <pr>
set -euo pipefail

pr="${1:?usage: gate-status.sh <pr>}"
root="$(cd "$(dirname "$0")/.." && pwd)"
repo="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
head="$(gh pr view "$pr" --repo "$repo" --json headRefOid --jq .headRefOid)"
refuse=0

# The newest gate run on the head decides: the gate re-runs when the pull
# request's body changes, and an older run read older evidence.
latest="$(gh api "repos/$repo/commits/$head/check-runs?check_name=gate&filter=latest" \
	--jq '[.check_runs[] | select(.name == "gate")] | sort_by(.started_at) | last | "\(.status) \(.conclusion)"')"
case "$latest" in
"completed success") echo "gate is green on $head" ;;
"" | "null null")
	echo "REFUSE PR #$pr: no gate check on its head $head" >&2
	refuse=1
	;;
*)
	echo "REFUSE PR #$pr: gate on its head $head is $latest" >&2
	refuse=1
	;;
esac

# CI's macOS runners do not stand in for this run: it is a developer's run on
# a macOS machine, from a clean checkout of the head, recorded in the body.
class="$(gh pr diff "$pr" --repo "$repo" --name-only | (cd "$root" && go run ./internal/hostcheck classify))"
if [ "$class" = docs-only ]; then
	echo "documentation only: no macOS acceptance run is needed"
else
	body="$(gh pr view "$pr" --repo "$repo" --json body --jq '.body // ""' | tr -d '\r')"
	if printf '%s\n' "$body" | grep -Eq "^[[:space:]]*macOS-Acceptance:[[:space:]]*$head[[:space:]]+PASS[[:space:]]*$"; then
		echo "macOS acceptance passed on $head"
	else
		echo "REFUSE PR #$pr: no passing macOS acceptance run recorded for its head $head ($class)" >&2
		refuse=1
	fi
fi
exit "$refuse"
