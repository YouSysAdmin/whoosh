#!/usr/bin/env bash
# Runs on each `web` host after the release goes live. The deploy context is
# exported automatically as environment variables ($APP_NAME, $HOST,
# $RELEASE_PATH, $CURRENT_PATH, $STAGE, $RELEASE_TIMESTAMP, ...), so scripts need
# no templating to know where they are.
set -euo pipefail

echo "[$HOST] healthcheck for $APP_NAME ($STAGE), release $RELEASE_TIMESTAMP @ ${COMMIT_HASH:0:8}"

for attempt in $(seq 1 10); do
	if curl -fsS http://127.0.0.1/up >/dev/null 2>&1; then
		echo "[$HOST] healthy after ${attempt} attempt(s)"
		exit 0
	fi
	sleep 2
done

echo "[$HOST] FAILED health check - app not responding on /up" >&2
exit 1
