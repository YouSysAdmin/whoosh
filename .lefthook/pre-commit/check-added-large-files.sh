#!/usr/bin/env bash
#------------------------------------------------------------------------------
# Lefthook checks
# (c) YouSysAdmin
#------------------------------------------------------------------------------

# Reject newly-added files larger than 500 KB.
# Bundled web fonts under frontend/static/fonts/ are intentionally exempt.
set -euo pipefail

MAX_BYTES=512000
EXCLUDE_RE='^frontend/static/fonts/|^vendor/'

failed=()
while IFS= read -r path; do
	[[ -z $path ]] && continue
	[[ $path =~ $EXCLUDE_RE ]] && continue
	[[ -f $path ]] || continue
	size=$(stat -c %s -- "$path" 2>/dev/null || stat -f %z -- "$path" 2>/dev/null || echo 0)
	if ((size > MAX_BYTES)); then
		failed+=("$path ($size bytes)")
	fi
done < <(git diff --cached --name-only --diff-filter=A)

if ((${#failed[@]} > 0)); then
	echo "Files larger than ${MAX_BYTES} bytes are not allowed:" >&2
	printf '  %s\n' "${failed[@]}" >&2
	exit 1
fi
