#!/usr/bin/env bash
#------------------------------------------------------------------------------
# Lefthook checks
# (c) YouSysAdmin
#------------------------------------------------------------------------------

# Detect committed API tokens / secrets for common providers.
# Patterns favor distinctive prefixes (ghp_, sk_live_, AIza, …) to keep
# false-positives low. Providers without a unique prefix (Cloudflare,
# JumpCloud) are matched in environment-variable assignment context only.
#
# The script prints WHICH file matched WHICH provider, but never the
# matching content - leaking the secret into CI logs would defeat the point.
set -euo pipefail

[[ $# -eq 0 ]] && exit 0

# Parallel arrays: LABELS[i] describes the secret type matched by PATTERNS[i].
LABELS=(
	"AWS access key ID"
	"AWS secret access key (env-var assignment)"
	"GitHub personal access token (classic)"
	"GitHub fine-grained personal access token"
	"Slack token"
	"Slack incoming webhook"
	"Stripe live secret/restricted key"
	"SendGrid API key"
	"Google API key"
	"npm access token"
	"JWT (JSON Web Token)"
	"Cloudflare API token/key (env-var assignment)"
	"JumpCloud API key (env-var assignment)"
)
PATTERNS=(
	'\b(AKIA|ASIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA)[0-9A-Z]{16}\b'
	'aws_secret_access_key[[:space:]]*[:=][[:space:]]*[A-Za-z0-9/+=]{40}'
	'\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36}\b'
	'\bgithub_pat_[A-Za-z0-9_]{82}\b'
	'\bxox[abprs]-[0-9]+-[0-9]+(-[0-9]+)?-[A-Za-z0-9-]{16,}\b'
	'https://hooks\.slack\.com/services/T[A-Z0-9]+/B[A-Z0-9]+/[A-Za-z0-9]{20,}'
	'\b(sk|rk)_live_[A-Za-z0-9]{24,}\b'
	'\bSG\.[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{43}\b'
	'\bAIza[0-9A-Za-z_-]{35}\b'
	'\bnpm_[A-Za-z0-9]{36}\b'
	'\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b'
	'(CLOUDFLARE|CF)_(API_KEY|API_TOKEN|GLOBAL_API_KEY)[[:space:]]*[:=][[:space:]]*["'"'"']?[A-Za-z0-9_-]{32,}'
	'JUMPCLOUD_(API_KEY|TOKEN)[[:space:]]*[:=][[:space:]]*["'"'"']?[A-Za-z0-9]{40}\b'
)
# Per-pattern grep flags. `i` flag is reserved for variable-name patterns whose
# env-var convention varies in case (e.g. `aws_secret_access_key` vs uppercase).
# Prefix-based patterns (ghp_, AIza, sk_live_, …) stay case-sensitive to keep
# false-positives low.
CASEFOLD=(
	""   # AWS access key ID
	"-i" # AWS secret access key
	""   # GitHub PAT classic
	""   # GitHub fine-grained PAT
	""   # Slack token
	""   # Slack webhook
	""   # Stripe key
	""   # SendGrid key
	""   # Google API key
	""   # npm token
	""   # JWT
	"-i" # Cloudflare env-var
	"-i" # JumpCloud env-var
)

failed=0
for i in "${!PATTERNS[@]}"; do
	# shellcheck disable=SC2086
	hits=$(LC_ALL=C grep -lE ${CASEFOLD[$i]} -e "${PATTERNS[$i]}" -- "$@" 2>/dev/null || true)
	if [[ -n $hits ]]; then
		echo "${LABELS[$i]} detected in:" >&2
		while IFS= read -r f; do
			[[ -n $f ]] && echo "  $f" >&2
		done <<<"$hits"
		failed=1
	fi
done

exit "$failed"
