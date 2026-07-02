#!/usr/bin/env bash
# Restore this release's backed-up asset manifest into the SHARED public/assets.
#
# Wired to the `deploy:rollback` after-hook, so it runs automatically once the
# rollback has repointed `current` at the previous release. whoosh runs the
# hook task inside `current`, so "." here IS the rolled-back release, copying its
# saved manifest back over the shared dir makes the live app resolve assets the
# way this release's code expects again.
set -euo pipefail

backup_dir="assets_manifest_backup"
if [ ! -d "$backup_dir" ]; then
	echo "restore-manifest: no $backup_dir in this release - was it deployed with backup-manifest.sh?" >&2
	exit 1
fi

shopt -s nullglob
found=0
for m in "$backup_dir"/*; do
	cp -p "$m" public/assets/
	found=1
done

if [ "$found" -eq 0 ]; then
	echo "restore-manifest: $backup_dir is empty" >&2
	exit 1
fi
echo "restore-manifest: restored $(ls "$backup_dir" | tr '\n' ' ') into public/assets"
