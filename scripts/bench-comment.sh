#!/usr/bin/env bash
# Post or update the one sticky benchmark comment on a PR.
# Usage: bench-comment.sh <report.md>   (needs GH_TOKEN, GITHUB_REPOSITORY, PR_NUMBER)
# Never fails the job: without write access (fork PRs) it just says so and exits 0.
set -uo pipefail

marker='<!-- made-bench -->'
body=$(printf '%s\n%s\n' "$marker" "$(cat "$1")")
api="repos/$GITHUB_REPOSITORY/issues"

id=$(gh api --paginate "$api/$PR_NUMBER/comments" \
  --jq "[.[] | select(.user.login == \"github-actions[bot]\" and (.body | startswith(\"$marker\")))][0].id // empty" 2>/dev/null | head -n1)

if [ -n "$id" ]; then
  gh api -X PATCH "$api/comments/$id" -f body="$body" >/dev/null
else
  gh api -X POST "$api/$PR_NUMBER/comments" -f body="$body" >/dev/null
fi || echo "bench: could not post the PR comment (no write access?); see the step summary"
exit 0
