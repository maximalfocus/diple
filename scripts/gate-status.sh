#!/usr/bin/env bash
# The maintainer's landing check while the repository cannot enforce one: the
# `gate` check on the pull request's current head must have succeeded. A red,
# pending or missing gate refuses the landing.
#
#   scripts/gate-status.sh <pr>
set -euo pipefail

pr="${1:?usage: gate-status.sh <pr>}"
repo="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
head="$(gh pr view "$pr" --repo "$repo" --json headRefOid --jq .headRefOid)"
# The newest gate run on the head decides: the gate re-runs when the pull
# request's body changes, and an older run read older evidence.
latest="$(gh api "repos/$repo/commits/$head/check-runs?check_name=gate&filter=latest" \
	--jq '[.check_runs[] | select(.name == "gate")] | sort_by(.started_at) | last | "\(.status) \(.conclusion)"')"
case "$latest" in
"completed success")
	echo "gate is green on $head"
	exit 0
	;;
"" | "null null")
	echo "REFUSE PR #$pr: no gate check on its head $head" >&2
	;;
*)
	echo "REFUSE PR #$pr: gate on its head $head is $latest" >&2
	;;
esac
exit 1
