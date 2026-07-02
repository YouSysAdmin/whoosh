#!/bin/sh
# whoosh rbenv plugin - host setup script.
#
# Idempotently installs rbenv + ruby-build under $RBENV_ROOT, registers rbenv in
# the operator's shell rc files, ensures the desired Ruby versions are built, and (optionally) prunes versions
# not in the desired set.
# Configuration is passed in through the environment (RBENV_* vars set by the plugin), every read has a safe
# default so the script also works when run by hand.
set -eu

: "${RBENV_ROOT:=$HOME/.rbenv}"
RBENV_REPO="${RBENV_REPO:-https://github.com/rbenv/rbenv.git}"
RUBY_BUILD_REPO="${RUBY_BUILD_REPO:-https://github.com/rbenv/ruby-build.git}"
RBENV_DEFAULT_GEMS_REPO="${RBENV_DEFAULT_GEMS_REPO:-https://github.com/rbenv/rbenv-default-gems.git}"
RBENV_DEFAULT_GEMS_LIST="${RBENV_DEFAULT_GEMS_LIST:-}"
RBENV_PLUGINS="${RBENV_PLUGINS:-}"
RBENV_VERSIONS="${RBENV_VERSIONS:-}"
RBENV_GLOBAL="${RBENV_GLOBAL:-}"
RBENV_SHELLS="${RBENV_SHELLS:-bash zsh}"
RBENV_PRUNE="${RBENV_PRUNE:-0}"
RBENV_UPDATE="${RBENV_UPDATE:-0}"
RBENV_INSTALL_RUBY="${RBENV_INSTALL_RUBY:-1}"
RBENV_READ_RUBY_VERSION="${RBENV_READ_RUBY_VERSION:-1}"

log() { printf 'rbenv: %s\n' "$*"; }

# ---------------------------------------------------------------------------
# Install/Update rbenv itself.
# ---------------------------------------------------------------------------
rbenv_bin="$RBENV_ROOT/bin/rbenv"
if [ -x "$rbenv_bin" ]; then
	log "rbenv already installed at $RBENV_ROOT"
	if [ "$RBENV_UPDATE" = "1" ] && [ -d "$RBENV_ROOT/.git" ]; then
		log "updating rbenv"
		git -C "$RBENV_ROOT" pull --ff-only || log "rbenv update skipped"
	fi
elif command -v rbenv >/dev/null 2>&1; then
	# rbenv is on PATH but not under RBENV_ROOT (e.g. a system package).
	rbenv_bin="$(command -v rbenv)"
	RBENV_ROOT="$(rbenv root 2>/dev/null || echo "$RBENV_ROOT")"
	log "using existing rbenv from PATH ($rbenv_bin, root $RBENV_ROOT)"
else
	log "installing rbenv into $RBENV_ROOT"
	git clone --depth 1 "$RBENV_REPO" "$RBENV_ROOT"
	rbenv_bin="$RBENV_ROOT/bin/rbenv"
fi

[ -x "$rbenv_bin" ] || {
	log "rbenv binary not found after install"
	exit 1
}

export RBENV_ROOT
export PATH="$RBENV_ROOT/bin:$RBENV_ROOT/shims:$PATH"

# ---------------------------------------------------------------------------
# install_plugin <name> <repo> [version]
# Clones an rbenv plugin into $RBENV_ROOT/plugins/<name>.
# With a version (tag, branch, or commit SHA) the checkout is pinned to it -
# a full clone, so any SHA is reachable,
# without one it is a shallow clone of the default branch.
# On an existing checkout a version is fetched and checked out,
# otherwise RBENV_UPDATE=1 fast-forwards the default branch.
# ---------------------------------------------------------------------------
install_plugin() {
	name="$1"
	repo="$2"
	version="${3:-}"
	dir="$RBENV_ROOT/plugins/$name"
	if [ -d "$dir" ]; then
		if [ -n "$version" ] && [ -d "$dir/.git" ]; then
			log "setting $name to $version"
			git -C "$dir" fetch --tags --force origin >/dev/null 2>&1 || log "$name: fetch failed"
			if ! git -C "$dir" checkout --quiet "$version" 2>/dev/null; then
				# The commit may be missing from a prior shallow clone, deepen and retry.
				git -C "$dir" fetch --unshallow --tags origin >/dev/null 2>&1 || true
				git -C "$dir" checkout --quiet "$version" || log "$name: cannot checkout $version"
			fi
		elif [ "$RBENV_UPDATE" = "1" ] && [ -d "$dir/.git" ]; then
			log "updating $name"
			git -C "$dir" pull --ff-only || log "$name update skipped"
		fi
		return 0
	fi
	mkdir -p "$RBENV_ROOT/plugins"
	if [ -n "$version" ]; then
		log "installing $name @ $version"
		git clone "$repo" "$dir"
		git -C "$dir" checkout --quiet "$version"
	else
		log "installing $name"
		git clone --depth 1 "$repo" "$dir"
	fi
}

# ---------------------------------------------------------------------------
# Install/Update ruby-build - the builder that backs `rbenv install`.
# ---------------------------------------------------------------------------
if [ "$RBENV_INSTALL_RUBY" = "1" ]; then
	if [ -d "$RBENV_ROOT/plugins/ruby-build" ]; then
		install_plugin ruby-build "$RUBY_BUILD_REPO" # present: updates when RBENV_UPDATE=1
	elif "$rbenv_bin" install --version >/dev/null 2>&1; then
		log "ruby-build already available (rbenv install works)"
	else
		install_plugin ruby-build "$RUBY_BUILD_REPO" # absent: clone it
	fi
fi

# ---------------------------------------------------------------------------
# Install the rbenv-default-gems plugin and render the default-gems list, so
# every `rbenv install` below also installs these gems. Only when a list is set.
# The list is newline-separated (one gem per line, e.g. "bundler", "bcat ~>0.6",
# "rails --pre") and written verbatim to $RBENV_ROOT/default-gems.
# ---------------------------------------------------------------------------
if [ -n "$RBENV_DEFAULT_GEMS_LIST" ]; then
	install_plugin rbenv-default-gems "$RBENV_DEFAULT_GEMS_REPO"
	log "writing default-gems to $RBENV_ROOT/default-gems"
	printf '%s\n' "$RBENV_DEFAULT_GEMS_LIST" >"$RBENV_ROOT/default-gems"
fi

# ---------------------------------------------------------------------------
# Install any additional rbenv plugins from the Deployfile (params plugins:),
# one "<name> <repo> [version]" per line -> cloned into $RBENV_ROOT/plugins/<name>,
# checked out at <version> when given.
# ruby-build and rbenv-default-gems are pinned and need not be listed here
# but we can add hes if need to setup other version.
# ---------------------------------------------------------------------------
if [ -n "$RBENV_PLUGINS" ]; then
	printf '%s\n' "$RBENV_PLUGINS" | while read -r name repo version; do
		[ -n "$name" ] || continue
		install_plugin "$name" "$repo" "$version"
	done
fi

# ---------------------------------------------------------------------------
# Register rbenv in the operator's shell rc files (idempotent, marker-guarded).
# ---------------------------------------------------------------------------
marker="# >>> whoosh rbenv >>>"
end_marker="# <<< whoosh rbenv <<<"

rc_file_for() {
	case "$1" in
	bash) echo ".bashrc" ;;
	zsh) echo ".zshrc" ;;
	bash_profile) echo ".bash_profile" ;;
	profile | sh) echo ".profile" ;;
	fish) echo ".config/fish/config.fish" ;;
	*) echo ".${1}rc" ;;
	esac
}

for shell in $RBENV_SHELLS; do
	rc="$HOME/$(rc_file_for "$shell")"
	if [ -f "$rc" ] && grep -qF "$marker" "$rc" 2>/dev/null; then
		continue
	fi
	log "registering rbenv init in $rc"
	mkdir -p "$(dirname "$rc")"
	{
		echo "$marker"
		printf 'export RBENV_ROOT="%s"\n' "$RBENV_ROOT"
		# The single quotes are deliberate: these lines are written verbatim to the rc file and must expand when the
		# operator's shell sources them, not here.
		# shellcheck disable=SC2016
		echo 'export PATH="$RBENV_ROOT/bin:$PATH"'
		# shellcheck disable=SC2016
		printf 'eval "$(rbenv init - %s)"\n' "$shell"
		echo "$end_marker"
	} >>"$rc"
done

# ---------------------------------------------------------------------------
# Resolve the desired Ruby versions: plugin params + .ruby-version files.
# ---------------------------------------------------------------------------
desired="$RBENV_VERSIONS"

add_ruby_version_file() {
	file="$1"
	[ -f "$file" ] || return 0
	found="$(sed -n '1p' "$file" | tr -d ' \t\r\n')"
	found="${found#ruby-}"
	if [ -n "$found" ]; then
		log "found Ruby $found in $file"
		desired="$desired $found"
	fi
}

if [ "$RBENV_READ_RUBY_VERSION" = "1" ] && [ -n "${CURRENT_PATH:-}" ]; then
	# The app being deployed isn't on the host yet at before:starting, but the
	# previously deployed release (current) is - read its pinned version too.
	add_ruby_version_file "$CURRENT_PATH/.ruby-version"
fi

# Never prune the global version out from under the host.
[ -n "$RBENV_GLOBAL" ] && desired="$desired $RBENV_GLOBAL"

# Deduplicate, drop blanks. The versions are space-separated inside $desired, so split them onto one line each first -
# quoted, printf would emit the whole string as a single line and sort -u would dedupe nothing.
desired="$(printf '%s\n' "$desired" | tr ' ' '\n' | awk 'NF' | sort -u | tr '\n' ' ')"
log "desired Ruby versions: ${desired:-<none>}"

installed="$("$rbenv_bin" versions --bare 2>/dev/null || true)"

# ---------------------------------------------------------------------------
# Build any missing Ruby versions.
# ---------------------------------------------------------------------------
if [ "$RBENV_INSTALL_RUBY" = "1" ]; then
	for v in $desired; do
		if printf '%s\n' "$installed" | grep -qxF "$v"; then
			log "Ruby $v already installed"
		else
			log "installing Ruby $v (this can take a while)"
			"$rbenv_bin" install --skip-existing "$v"
		fi
	done
fi

# ---------------------------------------------------------------------------
# Prune installed versions that are not desired.
# ---------------------------------------------------------------------------
if [ "$RBENV_PRUNE" = "1" ] && [ -n "$desired" ]; then
	for v in $installed; do
		keep=0
		for d in $desired; do
			[ "$v" = "$d" ] && keep=1 && break
		done
		if [ "$keep" = "0" ]; then
			log "removing Ruby $v (not in desired set)"
			"$rbenv_bin" uninstall -f "$v" || log "failed to remove $v"
		fi
	done
fi

# ---------------------------------------------------------------------------
# Set the global version and refresh shims.
# ---------------------------------------------------------------------------
if [ -n "$RBENV_GLOBAL" ]; then
	log "setting rbenv global $RBENV_GLOBAL"
	"$rbenv_bin" global "$RBENV_GLOBAL"
fi

"$rbenv_bin" rehash || true
log "setup complete"
