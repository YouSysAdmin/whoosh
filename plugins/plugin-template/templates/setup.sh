#!/bin/sh
# Host-side script for the contributed `plugin-template:setup` task.
# Configured entirely through environment variables (set in the task's Envs by setup.go), so it stays static and
# testable. The executor streams it to each target host over stdin - nothing is uploaded.
#
# The task env also carries whoosh's standard values ($RELEASE_PATH, $CURRENT_PATH, $HOST, $STAGE, $ROLES, ...) and,
# when run as a hook, $DEPLOY_PHASE.
#
# TODO: replace the body with your real host-side setup (install packages, create dirs, write configs, ...).
# Keep it idempotent - it runs on every deploy.
set -eu

echo "plugin-template setup: host=$(hostname) endpoint=${PLUGIN_TEMPLATE_ENDPOINT:-unset} phase=${DEPLOY_PHASE:-standalone}"
