#!/usr/bin/env bash
# Runs the test-sync action. On a push, a manual run or a schedule it syncs.
# On any other event, such as a pull request, it checks the test files and
# shows the plan, without applying it. Either way, the result goes to the job
# summary. With COMMENT=true, a pull request's plan is also posted as a comment
# on it, which later runs update rather than adding new ones.
#
# Reads SEMALOOP_API_KEY, TESTS_PATH, ALLOW_ARCHIVE_ALL, COMMENT, GITHUB_TOKEN
# and EVENT_NAME.
set -uo pipefail

summary="${GITHUB_STEP_SUMMARY:-/dev/null}"
out="${RUNNER_TEMP:-/tmp}/semaloop-test-sync.out"
err="${RUNNER_TEMP:-/tmp}/semaloop-test-sync.err"
body="${RUNNER_TEMP:-/tmp}/semaloop-test-sync.md"

# Only events that mean "this is what's merged" sync. Everything else, from
# pull requests to review comments, only ever sees the plan.
case "$EVENT_NAME" in
  push | workflow_dispatch | schedule) mode=sync ;;
  *) mode=plan ;;
esac

for input in allow-archive-all:"$ALLOW_ARCHIVE_ALL" comment:"${COMMENT:-false}"; do
  case "${input#*:}" in
    true | false) ;;
    *)
      echo "::error::${input%%:*} must be true or false, got \"${input#*:}\"." >&2
      exit 1
      ;;
  esac
done

# run runs the CLI, keeps its results and logs for the summary, and shows
# both in the job log.
run() {
  semaloop "$@" >"$out" 2>"$err"
  status=$?
  cat "$out"
  cat "$err" >&2
}

# pre writes stdin as preformatted text, escaped so test names and file
# contents can't change the summary's markup.
pre() {
  echo "<pre>"
  sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'
  echo "</pre>"
}

# render TITLE NOTE writes the results, and any errors, as Markdown.
render() {
  echo "### $1"
  echo
  if [ -n "$2" ]; then
    echo "$2"
    echo
  fi
  if [ -s "$out" ]; then
    pre <"$out"
  fi
  if [ "$status" -ne 0 ]; then
    echo "Failed. The job log has the details."
    echo
    if [ -s "$err" ]; then
      pre <"$err"
    fi
  fi
}

# write_summary TITLE NOTE adds the results to the summary and, if asked for,
# to the pull request's comment.
write_summary() {
  render "$1" "$2" >>"$summary"
  if [ "${COMMENT:-false}" = true ] && [ "$mode" = plan ]; then
    comment "$1" "$2"
  fi
}

# unchanged is whether the plan changes nothing. The CLI ends such a plan with
# "No changes, N unchanged." (Plan.totals in internal/cmd/testplan.go).
unchanged() {
  [ "$status" -eq 0 ] && grep -Eq '^No changes, [0-9]+ unchanged\.$' "$out"
}

# github METHOD PATH [FILE] calls the GitHub REST API, sending FILE as the
# request body.
github() {
  local args=(-sS --fail-with-body -X "$1"
    -H "Authorization: Bearer $GITHUB_TOKEN"
    -H "Accept: application/vnd.github+json"
    -H "X-GitHub-Api-Version: 2022-11-28")
  if [ -n "${3:-}" ]; then
    args+=(--data-binary "@$3")
  fi
  curl "${args[@]}" "${GITHUB_API_URL:-https://api.github.com}$2"
}

# comment TITLE NOTE posts the results as a comment on the pull request, or
# updates the comment an earlier run posted. A plan with no changes isn't worth
# a new comment, but it does update an earlier one, which would otherwise show
# changes that are no longer planned. A hidden marker, which includes
# the path so that syncs of different directories keep their own comments,
# finds it again. Failing to comment is a warning rather than an error, so a
# plan isn't marked as failed because, say, a fork's token can't write.
comment() {
  local pr marker run_url page comments id
  if ! command -v jq >/dev/null; then
    echo "::warning::jq isn't installed, so the plan wasn't posted to the pull request." >&2
    return
  fi
  pr=$(jq -r '.pull_request.number // empty' "${GITHUB_EVENT_PATH:-/dev/null}" 2>/dev/null)
  if [ -z "$pr" ]; then
    echo "Not commenting: the $EVENT_NAME event isn't for a pull request."
    return
  fi
  if [ -z "${GITHUB_TOKEN:-}" ]; then
    echo "::warning::github-token is empty, so the plan wasn't posted to the pull request." >&2
    return
  fi

  marker="<!-- semaloop-test-sync: $TESTS_PATH -->"
  run_url="${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID:-}"
  {
    echo "$marker"
    render "$1" "$2"
    echo
    echo "[View the run]($run_url)"
  } >"$body"
  # GitHub rejects comments longer than 65,536 characters. Leave room for the
  # rest of the comment and point to the job summary instead.
  if [ "$(wc -c <"$body")" -gt 60000 ]; then
    {
      echo "$marker"
      echo "### $1"
      echo
      echo "The plan is too long for a comment. [The job summary]($run_url) has it in full."
    } >"$body"
  fi
  jq -Rs '{body: .}' <"$body" >"$body.json"

  local api="/repos/$GITHUB_REPOSITORY/issues"
  id=
  page=1
  while [ -z "$id" ]; do
    if ! comments=$(github GET "$api/$pr/comments?per_page=100&page=$page"); then
      echo "::warning::Couldn't read the pull request's comments, so the plan wasn't posted. Does the workflow have the pull-requests: write permission?" >&2
      return
    fi
    id=$(jq -r --arg marker "$marker" 'map(select(.body | startswith($marker))) | first | .id // empty' <<<"$comments")
    if [ "$(jq length <<<"$comments")" -lt 100 ]; then
      break
    fi
    page=$((page + 1))
  done

  if [ -z "$id" ] && unchanged; then
    echo "Not commenting: the plan has no changes."
    return
  fi
  if [ -n "$id" ]; then
    github PATCH "$api/comments/$id" "$body.json" >/dev/null
  else
    github POST "$api/$pr/comments" "$body.json" >/dev/null
  fi || echo "::warning::Couldn't post the plan to the pull request. Does the workflow have the pull-requests: write permission?" >&2
}

if [ -z "${SEMALOOP_API_KEY:-}" ]; then
  if [ "$mode" = sync ]; then
    echo "::error::api-key is empty, so the tests can't be synced. Store your Semaloop API key as a secret and pass it as api-key." >&2
    printf '### Semaloop test sync\n\nNot synced: api-key is empty.\n' >>"$summary"
    exit 1
  fi
  run test check -- "$TESTS_PATH"
  note="There's no API key, so the files were checked but the plan was skipped. GitHub doesn't give secrets to workflows run from forks."
  if [ "$status" -eq 0 ]; then
    note+=" The files are valid."
  fi
  write_summary "Semaloop test files" "$note"
  exit "$status"
fi

if [ "$mode" = plan ]; then
  run test diff -- "$TESTS_PATH"
  write_summary "Semaloop test plan" "A pull request only shows the plan. Nothing is changed until the tests are synced."
  exit "$status"
fi

args=(test sync --yes)
if [ "$ALLOW_ARCHIVE_ALL" = true ]; then
  args+=(--allow-archive-all)
fi
run "${args[@]}" -- "$TESTS_PATH"
write_summary "Semaloop test sync" ""
exit "$status"
