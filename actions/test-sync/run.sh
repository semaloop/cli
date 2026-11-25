#!/usr/bin/env bash
# Runs the test-sync action. On a pull request it checks the test files and
# shows the plan, without applying it. On other events it syncs. Either way,
# the result goes to the job summary.
#
# Reads SEMALOOP_API_KEY, TESTS_PATH, ALLOW_ARCHIVE_ALL and EVENT_NAME.
set -uo pipefail

summary="${GITHUB_STEP_SUMMARY:-/dev/null}"
out="${RUNNER_TEMP:-/tmp}/semaloop-test-sync.out"
err="${RUNNER_TEMP:-/tmp}/semaloop-test-sync.err"

# Pull requests, including ones from forks, only ever see the plan.
case "$EVENT_NAME" in
  pull_request | pull_request_target | merge_group) mode=plan ;;
  *) mode=sync ;;
esac

case "$ALLOW_ARCHIVE_ALL" in
  true | false) ;;
  *)
    echo "::error::allow-archive-all must be true or false, got \"$ALLOW_ARCHIVE_ALL\"." >&2
    exit 1
    ;;
esac

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

# write_summary TITLE NOTE adds the results, and any errors, to the summary.
write_summary() {
  {
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
  } >>"$summary"
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
