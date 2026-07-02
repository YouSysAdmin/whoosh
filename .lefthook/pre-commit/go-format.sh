#!/usr/bin/env bash
#------------------------------------------------------------------------------
# Lefthook checks
# (c) YouSysAdmin
#------------------------------------------------------------------------------

# Format staged Go files in place. Prefers goimports (formats *and* fixes the
# import block); falls back to gofmt if goimports is not installed. gofmt's
# tab indentation matches .editorconfig, so this also covers trailing
# whitespace and the final newline for *.go (excluded from the generic
# hygiene hooks).
set -euo pipefail

[[ $# -eq 0 ]] && exit 0

files=()
for f in "$@"; do
	[[ -f $f ]] && files+=("$f")
done
[[ ${#files[@]} -eq 0 ]] && exit 0

if command -v goimports >/dev/null 2>&1; then
	goimports -w -- "${files[@]}"
else
	gofmt -w -- "${files[@]}"
fi
