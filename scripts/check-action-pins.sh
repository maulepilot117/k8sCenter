#!/usr/bin/env bash
# scripts/check-action-pins.sh
#
# Enforce two supply-chain rules on every `uses:` in .github/workflows/:
#
#   1. Third-party actions are pinned to a full 40-character commit SHA, not a
#      tag. A tag is mutable — an attacker who compromises a maintainer account
#      can repoint v4 at their own commit and every workflow picks it up on the
#      next run, with whatever permissions the job holds.
#
#   2. That commit is at least 7 days old. This is the same application-layer
#      cooldown the repo applies to npm, Go modules and container bases: most
#      registry compromises are caught within days, and a week of community
#      catch-time is the cheapest defense available. `bunfig.toml` enforces it
#      for Bun dependencies and frontend/Dockerfile for Debian packages; this
#      script is the GitHub Actions half, which until now was review-enforced
#      only — which is to say, not enforced.
#
# Local actions (`uses: ./...`) and container actions (`uses: docker://...`)
# are out of scope: the first is this repo's own code, the second is pinned by
# digest elsewhere or not a GitHub Action at all.
#
# Usage:
#   scripts/check-action-pins.sh            # check every workflow
#   GITHUB_TOKEN=... scripts/check-action-pins.sh
#
# The commit date lookup needs the GitHub API. Unauthenticated requests are
# rate-limited to 60/hour, which is not enough for a full sweep, so CI passes
# GITHUB_TOKEN. Without a usable token the script reports what it could not
# check and fails, rather than passing while having verified nothing.

set -euo pipefail

WORKFLOW_DIR="${1:-.github/workflows}"
MIN_AGE_DAYS=7

if ! command -v gh >/dev/null 2>&1; then
  echo "ERROR: the gh CLI is required to look up commit dates." >&2
  exit 1
fi

now_ts=$(date +%s)
unpinned=0
too_new=0
unknown=0
checked=0

# Cache lookups: the same action SHA appears in several workflows, and each
# lookup is an API call against a rate limit.
declare -A seen

while IFS= read -r line; do
  ref="${line#*uses:}"
  # Drop the trailing "# v7.0.1" comment BEFORE trimming, or the comment's
  # own text ends up glued to the revision and every pin reads as unpinned.
  ref="${ref%%#*}"
  ref="$(echo "$ref" | tr -d ' \r\"'"'")"

  case "$ref" in
    ./*|docker://*|"") continue ;;
  esac

  repo="${ref%%@*}"
  rev="${ref#*@}"

  # owner/repo or owner/repo/path — the API wants the first two segments.
  owner_repo="$(echo "$repo" | cut -d/ -f1,2)"

  if ! echo "$rev" | grep -qE '^[0-9a-f]{40}$'; then
    echo "NOT SHA-PINNED: $ref"
    echo "    Pin to the full 40-character commit SHA and keep the tag as a"
    echo "    trailing comment, e.g. uses: $repo@<sha> # $rev"
    unpinned=$((unpinned + 1))
    continue
  fi

  key="${owner_repo}@${rev}"
  if [ -n "${seen[$key]:-}" ]; then
    continue
  fi
  seen[$key]=1
  checked=$((checked + 1))

  if ! committed=$(gh api "repos/${owner_repo}/commits/${rev}" \
        --jq '.commit.committer.date' 2>/dev/null); then
    echo "COULD NOT VERIFY: $ref"
    echo "    The GitHub API did not return a commit date. A pin this script"
    echo "    cannot check is not a pin this script may pass."
    unknown=$((unknown + 1))
    continue
  fi

  committed_ts=$(date -d "$committed" +%s)
  age_days=$(( (now_ts - committed_ts) / 86400 ))

  if [ "$age_days" -lt "$MIN_AGE_DAYS" ]; then
    echo "INSIDE COOLDOWN: $ref"
    echo "    Commit is ${age_days} day(s) old; the supply-chain cooldown is"
    echo "    ${MIN_AGE_DAYS} days. Wait, or pin the previous release."
    too_new=$((too_new + 1))
  fi
done < <(grep -rhE '^\s*(-\s+)?uses:' "$WORKFLOW_DIR")

failures=$((unpinned + too_new + unknown))

# A supply-chain gate that verified nothing must not report success. A renamed
# workflow directory, or a grep that matched no `uses:` line, would otherwise
# print "passed: 0 distinct action pins" and exit 0 -- forever, silently.
if [ "$failures" -eq 0 ] && [ "$checked" -eq 0 ] && [ "$unpinned" -eq 0 ]; then
  echo "ERROR: no action references found under $WORKFLOW_DIR." >&2
  echo "  This check verified nothing. Either the path is wrong or the" >&2
  echo "  workflows moved; a gate that scans an empty set is not a pass." >&2
  exit 1
fi

if [ "$failures" -gt 0 ]; then
  echo
  echo "Action pin check FAILED: ${unpinned} unpinned, ${too_new} inside the" \
       "${MIN_AGE_DAYS}-day cooldown, ${unknown} unverifiable."
  exit 1
fi

echo "Action pin check passed: ${checked} distinct action pins, all SHA-pinned" \
     "and at least ${MIN_AGE_DAYS} days old."
