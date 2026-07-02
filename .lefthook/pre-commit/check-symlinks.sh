#!/usr/bin/env bash
#------------------------------------------------------------------------------
# Lefthook checks
# (c) YouSysAdmin
#------------------------------------------------------------------------------

# Fail if any staged path is a broken symlink (target does not exist).
set -euo pipefail

[[ $# -eq 0 ]] && exit 0

failed=()
for f in "$@"; do
	if [[ -L $f && ! -e $f ]]; then
		failed+=("$f -> $(readlink -- "$f")")
	fi
done

if ((${#failed[@]} > 0)); then
	echo "Broken symlinks:" >&2
	printf '  %s\n' "${failed[@]}" >&2
	exit 1
fi
