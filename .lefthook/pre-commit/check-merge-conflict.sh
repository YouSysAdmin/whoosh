#!/usr/bin/env bash
#------------------------------------------------------------------------------
# Lefthook checks
# (c) YouSysAdmin
#------------------------------------------------------------------------------

# Fail if any staged file contains a git merge-conflict marker.
set -euo pipefail

[[ $# -eq 0 ]] && exit 0

# Conflict markers: lines beginning with seven of <, =, or >.
PATTERN='^(<{7}|={7}|>{7})( |$)'

hits=$(LC_ALL=C grep -lE "$PATTERN" -- "$@" 2>/dev/null || true)
if [[ -n $hits ]]; then
	echo "Merge conflict markers present in:" >&2
	# shellcheck disable=SC2086
	printf '  %s\n' $hits >&2
	exit 1
fi
