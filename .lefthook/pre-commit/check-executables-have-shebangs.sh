#!/usr/bin/env bash
#------------------------------------------------------------------------------
# Lefthook checks
# (c) YouSysAdmin
#------------------------------------------------------------------------------

# Fail if any executable (mode 100755) file is missing a `#!` shebang.
set -euo pipefail

[[ $# -eq 0 ]] && exit 0

failed=()
while IFS=$'\t' read -r meta path; do
	mode="${meta%% *}"
	[[ "$mode" == "100755" ]] || continue
	[[ -f $path ]] || continue
	if [[ "$(head -c 2 -- "$path" 2>/dev/null)" != "#!" ]]; then
		failed+=("$path")
	fi
done < <(git ls-files --stage -- "$@")

if ((${#failed[@]} > 0)); then
	echo "Executable files missing a #! shebang:" >&2
	printf '  %s\n' "${failed[@]}" >&2
	exit 1
fi
