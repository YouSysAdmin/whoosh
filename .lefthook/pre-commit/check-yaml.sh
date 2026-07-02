#!/usr/bin/env bash
#------------------------------------------------------------------------------
# Lefthook checks
# (c) YouSysAdmin
#------------------------------------------------------------------------------

# Validate that staged YAML files parse. Parse-only, matching the behavior of
# pre-commit-hooks `check-yaml` (which uses PyYAML's safe_load).
set -euo pipefail

[[ $# -eq 0 ]] && exit 0

python3 - "$@" <<'PY'
import sys
import yaml

failed = []
for path in sys.argv[1:]:
    try:
        with open(path, "rb") as fh:
            yaml.safe_load(fh)
    except yaml.YAMLError as exc:
        failed.append((path, str(exc)))
    except OSError:
        # File staged for delete or otherwise unreadable - leave it alone.
        pass

if failed:
    sys.stderr.write("YAML parse errors:\n")
    for path, msg in failed:
        sys.stderr.write(f"  {path}: {msg}\n")
    sys.exit(1)
PY
