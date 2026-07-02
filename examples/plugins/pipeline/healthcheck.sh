#!/bin/sh
# Example post-deploy healthcheck contributed by the example-pipeline plugins.
# It runs on each target host (the executor streams it over SSH) after the new
# release goes live. The deploy context is available as environment variables
# ($RELEASE_PATH, $HOST, $STAGE, ...). Replace the body with a real check, e.g.
# curl a health endpoint and exit non-zero to fail (and roll back) the deploy.
set -eu

echo "[example-pipeline] post-publish healthcheck on $(hostname): release=${RELEASE_PATH:-?}"
