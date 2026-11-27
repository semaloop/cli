#!/usr/bin/env bash
# Runs the test-sync action. On a push, a manual run or a schedule it syncs.
# On any other event, such as a pull request, it checks the test files and
# shows the plan, without applying it. Either way, the result goes to the job
# summary. With COMMENT=true, the plan is also written as a comment, for the
# next step to post on the pull request.
#
# Reads SEMALOOP_API_KEY, TESTS_PATH, ALLOW_ARCHIVE_ALL, COMMENT and EVENT_NAME.
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
# writes the pull request's comment.
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

# comment writes the pull request's comment for the next step to post. With no
# changes, it only updates an earlier comment.
comment() {
  local run_url only_update=false
  run_url="${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY:-}/actions/runs/${GITHUB_RUN_ID:-}"
  {
    render "$1" "$2"
    echo
    echo "[View the run]($run_url)"
  } >"$body"
  # GitHub rejects comments longer than 65,536 characters. Leave room for the
  # rest of the comment and point to the job summary instead.
  if [ "$(wc -c <"$body")" -gt 60000 ]; then
    {
      echo "### $1"
      echo
      echo "The plan is too long for a comment. [The job summary]($run_url) has it in full."
    } >"$body"
  fi
  if unchanged; then
    only_update=true
  fi
  {
    echo "comment-path=$body"
    echo "comment-only-update=$only_update"
  } >>"${GITHUB_OUTPUT:-/dev/null}"
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
