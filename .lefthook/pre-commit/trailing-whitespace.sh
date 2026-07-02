#!/usr/bin/env bash
#------------------------------------------------------------------------------
# Lefthook checks
# (c) YouSysAdmin
#------------------------------------------------------------------------------

# Strip trailing whitespace from staged files.
# Markdown files are left alone (two-space hard line breaks are intentional).
set -euo pipefail

[[ $# -eq 0 ]] && exit 0

for f in "$@"; do
	[[ -f $f && ! -L $f ]] || continue
	case "$f" in
	*.md | *.markdown) continue ;;
	esac
	if LC_ALL=C grep -Eq '[[:blank:]]+$' "$f"; then
		tmp=$(mktemp)
		LC_ALL=C sed -E 's/[[:blank:]]+$//' "$f" >"$tmp" && mv "$tmp" "$f"
	fi
done
