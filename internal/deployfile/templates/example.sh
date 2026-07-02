#!/usr/bin/env bash
# Scripts placed here can be referenced by name from a task's `scripts:` list,
# e.g. `- path: example.sh`. Deploy context, config vars, and the task's env are
# all exported as environment variables.
set -euo pipefail

echo "Running ${0##*/} on host: ${HOST:-?}"
echo "  app/stage:    ${APP_NAME:-?} / ${STAGE:-?}"
echo "  release_path: ${RELEASE_PATH:-?}"
echo "  current_path: ${CURRENT_PATH:-?}"
