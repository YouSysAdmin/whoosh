#!/usr/bin/env bash
# Run `rails db:migrate` only when the database actually changed. We compare the
# new release's db/ directory against the currently-live release's db/ and skip
# the (slow, locking)
# migration when they're identical.
#
# Runs before the release goes live, so $CURRENT_PATH still points at the OLD
# release and $RELEASE_PATH at the new one. The deploy context is exported as
# environment variables, so no templating is needed.
set -euo pipefail

new_db="$RELEASE_PATH/db"
cur_db="$CURRENT_PATH/db"

# No db/ in the release (e.g. not a Rails app, or migrations live elsewhere):
# nothing to do.
if [ ! -d "$new_db" ]; then
	echo "db:migrate - no ${new_db} in the release, skipping"
	exit 0
fi

# First deploy (no current/ yet) or db/ changed since the live release -> migrate.
# `diff -qr` exits 0 only when the trees are identical, missing current/db (first
# deploy) makes the -d test false, so we fall through and migrate.
if [ -d "$cur_db" ] && diff -qr "$new_db" "$cur_db" >/dev/null 2>&1; then
	echo "db:migrate - no changes in ${cur_db} since the current release, skipping"
	exit 0
fi

echo "db:migrate - changes detected in ${cur_db}, running migrations"
bundle exec rails db:migrate
