#!/usr/bin/env bash
# Installs the scripts/ git hooks into this repository.
#
# It sets core.hooksPath rather than copying files into .git/hooks, so the hooks
# stay versioned and reviewed like any other source file. A copy in .git/hooks
# is invisible to git, unrunnable in CI, and silently stale after an update.
#
# Idempotent: running it again just re-points the same path.
#
# Usage:  scripts/hooks/install.sh

set -euo pipefail
. "$(dirname "$0")/../lib/common.sh"

root="$(repo_root)"
hooks_rel="scripts/hooks"

gate_start "installing git hooks"

# Verify every hook is present and executable before pointing git at the
# directory: a missing hook would leave that stage silently unprotected.
for hook in pre-commit pre-push; do
	if [ ! -f "$root/$hooks_rel/$hook" ]; then
		gate_fail "missing $hooks_rel/$hook"
		exit 1
	fi
	if [ ! -x "$root/$hooks_rel/$hook" ]; then
		chmod +x "$root/$hooks_rel/$hook"
		printf 'made %s executable\n' "$hooks_rel/$hook"
	fi
done

previous="$(git -C "$root" config --get core.hooksPath || true)"
git -C "$root" config core.hooksPath "$hooks_rel"

if [ -n "$previous" ] && [ "$previous" != "$hooks_rel" ]; then
	printf 'replaced core.hooksPath (was: %s)\n' "$previous"
fi

gate_pass "core.hooksPath = $hooks_rel"
printf '\nbypass a hook with: git commit --no-verify / git push --no-verify\n'
