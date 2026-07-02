#!/usr/bin/env bash
# Back up the freshly-compiled asset manifest INTO this release, so a later
# rollback can restore the manifest that matches this release's code.
#
# Only needed in the SHARED-assets model (public/assets is a linked_dir). The
# manifest in public/assets was just overwritten by assets:precompile, we copy
# it somewhere immutable and per-release. whoosh runs task scripts inside the
# new release dir, so "public/assets" below is this release's view of the shared
# dir, and "assets_manifest_backup" is a real dir living in the release itself.
set -euo pipefail

backup_dir="assets_manifest_backup"
mkdir -p "$backup_dir"

# Match whatever the Rails toolchain produced:
#   Sprockets 4 -> .sprockets-manifest-<md5>.json
#   Sprockets 3 -> manifest-<md5>.json
#   Propshaft   -> .manifest.json
shopt -s nullglob
found=0
for m in public/assets/.sprockets-manifest-*.json \
	public/assets/manifest-*.json \
	public/assets/.manifest.json; do
	cp -p "$m" "$backup_dir/"
	found=1
done

if [ "$found" -eq 0 ]; then
	echo "backup-manifest: no asset manifest found under public/assets" >&2
	exit 1
fi
echo "backup-manifest: saved $(ls "$backup_dir" | tr '\n' ' ')"
