#!/usr/bin/env bash
# Gate: secret scanning with gitleaks.
#
# Three modes matter, and the subcommand choice is load-bearing:
#
#   --staged   `gitleaks git --staged` scans the staged patch. This is the
#              pre-commit mode: a secret must not enter history, because
#              deleting it later does not remove the commit.
#   --all      `gitleaks git` scans commits plus `gitleaks dir` scans the files
#              a push would carry: tracked files, and untracked files that are
#              not ignored. Both are needed: a secret can exist in an
#              uncommitted file, which the commit walk never sees.
#
# Both modes read .gitleaks.toml, so the hook and CI runs cannot drift into
# enforcing different rules.
#
# Ignored files are out of scope for the working-tree scan on purpose. They
# cannot enter a commit without `git add -f`, and that path is already covered
# by the --staged scan at commit time. Scanning them anyway makes this gate
# permanently red on a working copy that holds its local dev secrets in
# ignored files, and a gate that can never go green is a gate people bypass.
#
# Usage:  scripts/gates/secrets.sh [--staged|--all]
#
# Exit codes: 0 no leaks found, 1 leaks found, 2 gitleaks unavailable.

set -euo pipefail
. "$(dirname "$0")/../lib/common.sh"

root="$(repo_root)"
mode="${1:---all}"
config="$root/.gitleaks.toml"

if ! gl="$(tool_path gitleaks)"; then
	gate_fail "gitleaks is not installed"
	gate_fail "install: https://github.com/gitleaks/gitleaks#installing"
	exit 2
fi

# Only pass --config when the file exists; gitleaks errors on a missing path
# rather than falling back to its defaults.
gl_args=(--redact --no-banner)
if [ -f "$config" ]; then
	gl_args+=(--config "$config")
fi

case "$mode" in
--staged)
	gate_start "gitleaks (staged changes)"
	if (cd "$root" && "$gl" git --staged "${gl_args[@]}"); then
		gate_pass "no secrets in staged changes"
	else
		gate_fail "secrets detected in staged changes; the commit was not made"
		exit 1
	fi
	;;
--all)
	failed=0

	gate_start "gitleaks (commits)"
	if (cd "$root" && "$gl" git "${gl_args[@]}"); then
		gate_pass "no secrets in commits"
	else
		gate_fail "secrets detected in commits"
		failed=1
	fi

	# gitleaks `dir` accepts one path and has no exclusion flag, so the
	# pushable file list is materialised into a temporary tree and that tree
	# is scanned. The list is NUL-separated, so a path with a space survives.
	# A list that cannot be materialised fails the gate: blocking is the safe
	# direction.
	gate_start "gitleaks (working tree)"
	scratch="$(mktemp -d)"
	trap 'rm -rf "$scratch"' EXIT
	# `--cached` still lists a path that was moved or deleted in the worktree
	# but not yet staged, and tar then fails the whole scan on a file that has
	# no content left to read. A gone path carries nothing to a push, so the
	# list is filtered to what is on disk.
	if ! (cd "$root" && git ls-files -z --cached --others --exclude-standard |
		while IFS= read -r -d '' entry; do
			# `if` rather than `[ -f ] && printf`, which would leave a gone path as
			# the loop's last status and, under pipefail, fail the scan this filter
			# exists to let finish.
			if [ -f "$entry" ]; then printf '%s\0' "$entry"; fi
		done |
		tar --null --no-recursion -T - -cf - | tar -xf - -C "$scratch"); then
		gate_fail "could not materialise the pushable file list into $scratch"
		failed=1
	elif (cd "$scratch" && "$gl" dir . "${gl_args[@]}"); then
		gate_pass "no secrets in the files a push would carry"
	else
		gate_fail "secrets detected in the files a push would carry"
		failed=1
	fi

	exit "$failed"
	;;
*)
	gate_fail "unknown mode '$mode' (expected --staged or --all)"
	exit 2
	;;
esac

exit 0