#!/usr/bin/env bash
# Record a deployment marker in New Relic via Change Tracking
# (NerdGraph changeTrackingCreateEvent), the analogue of Capistrano's
# newrelic:notice_deployment. Like the capistrano recipe, it never fails
# the deploy: any error is reported as a warning and the script exits 0
# (set STRICT=true to make errors fatal). Requires curl and jq.
#
# The target entity is found by APM application name (the UI type
# "Service - APM"), or exactly via $NEW_RELIC_ENTITY_GUID.
#
# Configuration (environment variables):
#   NEW_RELIC_API_KEY      user API key NRAK-... (required, a license key
#                          does not work: it is an agent ingest credential
#                          and New Relic rejects it on the marker APIs)
#   NEW_RELIC_APP_NAME     application name exactly as registered in New Relic
#                          (default: $APP_NAME from the deploy context)
#   NEW_RELIC_ENTITY_GUID  entity GUID, skips the name search
#   NEW_RELIC_API_HOST     default api.newrelic.com, set api.eu.newrelic.com
#                          for EU region accounts
#   VERSION                deployment version (default: short revision)
#   DESCRIPTION            deployment description (default: app/revision/user line)
#   DEPLOY_USER            who deployed (default: $USER)
#   CHANGELOG              changelog text (optional); when unset it is derived
#                          from the deploy context's $DEPLOY_CHANGELOG
#                          (sha|author|email|subject lines), else - when
#                          $PREVIOUS_REVISION is set and we are inside a git
#                          repo - computed like the capistrano helper:
#                          git log PREVIOUS_REVISION..REVISION
#   PREVIOUS_REVISION      previous deployed revision (default: the deploy
#                          context's $PREVIOUS_COMMIT_HASH)
#   STRICT                 "true" to exit non-zero on errors (default: false)
#
# The revision comes from the deploy context ($COMMIT_HASH) or CI ($GITHUB_SHA).
#
#  newrelic-deploy-notify:
#    desc: Record the deployment in New Relic
#    hidden: true
#    local: true
#    envs:
#      NEW_RELIC_API_KEY: "{{ sensitive .new_relic_api_key }}"
#      NEW_RELIC_APP_NAME: "{{ .new_relic_app_name }}"
#    scripts:
#      - path: newrelic_deploy_notify.sh
#        interpreter: /usr/bin/env bash

set -euo pipefail

STRICT="${STRICT:-false}"

warn_or_die() {
	echo "newrelic-deploy-notify: $1" >&2
	[ "$STRICT" = "true" ] && exit 1
	exit 0
}

APP="${NEW_RELIC_APP_NAME:-${APP_NAME:-}}"
API_KEY="${NEW_RELIC_API_KEY:-}"
API_HOST="${NEW_RELIC_API_HOST:-api.newrelic.com}"
REVISION="${COMMIT_HASH:-${GITHUB_SHA:-}}"
PREVIOUS_REVISION="${PREVIOUS_REVISION:-${PREVIOUS_COMMIT_HASH:-}}"
VERSION="${VERSION:-}"
DEPLOY_USER="${DEPLOY_USER:-${USER:-}}"
DESCRIPTION="${DESCRIPTION:-"${APP} has been deployed ${REVISION} by ${DEPLOY_USER}"}"
CHANGELOG="${CHANGELOG:-}"

[ -n "$APP" ] || warn_or_die "no application name: set NEW_RELIC_APP_NAME"
[ -n "$REVISION" ] || warn_or_die "no revision: set GITHUB_SHA (or run within a deploy)"
[ -n "$API_KEY" ] || warn_or_die "no credentials: set NEW_RELIC_API_KEY (user key NRAK-...)"
command -v jq >/dev/null || warn_or_die "jq is required but not installed"
VERSION="${VERSION:-${REVISION:0:8}}"

# Prefer the deploy context's changelog (captured by whoosh at deploy:updating).
if [ -z "$CHANGELOG" ] && [ -n "${DEPLOY_CHANGELOG:-}" ]; then
	CHANGELOG="$(printf '%s\n' "$DEPLOY_CHANGELOG" | awk -F'|' '{print "  * " $2 ": " $4}')"
fi

# Same changelog lookup as the capistrano helper (helpers/send_deployment.rb).
if [ -z "$CHANGELOG" ] && [ -n "${PREVIOUS_REVISION:-}" ] &&
	git rev-parse --git-dir >/dev/null 2>&1; then
	CHANGELOG="$(git --no-pager log --no-color --pretty=format:'  * %an: %s' \
		--abbrev-commit --no-merges "${PREVIOUS_REVISION}..${REVISION}" 2>/dev/null || true)"
fi

echo "recording deployment of $APP revision ${REVISION:0:8} in New Relic"

if [ -n "${NEW_RELIC_ENTITY_GUID:-}" ]; then
	entity_query="id = '${NEW_RELIC_ENTITY_GUID}'"
else
	# "Service - APM" in the UI is entity domain APM, type APPLICATION. The
	# domain filter matters: browser apps are also type APPLICATION.
	entity_query="name = '${APP}' AND domain = 'APM' AND type = 'APPLICATION'"
fi

# The mutation is assembled with jq so every user-supplied value is
# JSON-escaped. Optional fields are dropped when empty.
payload="$(jq -n \
	--arg entity_query "$entity_query" \
	--arg version "$VERSION" \
	--arg commit "$REVISION" \
	--arg changelog "$CHANGELOG" \
	--arg description "$DESCRIPTION" \
	--arg user "$DEPLOY_USER" \
	'{query: ("mutation { changeTrackingCreateEvent(changeTrackingEvent: {"
    + "categoryAndTypeData: { kind: { category: \"deployment\", type: \"basic\" }, "
    + "categoryFields: { deployment: { version: " + ($version | @json)
    + ", commit: " + ($commit | @json)
    + (if $changelog != "" then ", changelog: " + ($changelog | @json) else "" end)
    + " } } }, entitySearch: { query: " + ($entity_query | @json) + " }"
    + (if $user != "" then ", user: " + ($user | @json) else "" end)
    + (if $description != "" then ", description: " + ($description | @json) else "" end)
    + " }) { changeTrackingEvent { changeTrackingId entity { name guid } } messages } }")}')"

if ! response="$(curl -fsS -X POST "https://${API_HOST}/graphql" \
	-H "API-Key: ${API_KEY}" \
	-H "Content-Type: application/json" \
	--data "$payload" 2>&1)"; then
	warn_or_die "deployment not recorded: ${response}"
fi

# NerdGraph returns HTTP 200 even on failure - check the body for errors.
errors="$(jq -r '[(.errors // [])[].message,
  (.data.changeTrackingCreateEvent.messages // [])[]] | join("; ")' \
	<<<"$response" 2>/dev/null || true)"
event_id="$(jq -r '.data.changeTrackingCreateEvent.changeTrackingEvent.changeTrackingId // empty' \
	<<<"$response" 2>/dev/null || true)"
if [ -z "$event_id" ]; then
	warn_or_die "deployment not recorded: ${errors:-$response}"
fi
[ -n "$errors" ] && echo "newrelic-deploy-notify: $errors" >&2

echo "recorded deployment of $APP (${DESCRIPTION})"
