#!/usr/bin/env bash
# Shared helpers for every gate and hook under scripts/.
#
# Sourced, never executed. It exists so the individual gates stay short and so
# they all agree on how the repository root is found, how output is reported,
# and how a missing tool is treated.
#
# Usage:  . "$(dirname "$0")/../lib/common.sh"

set -euo pipefail

# Byte-oriented matching is what every pattern here assumes, so the locale is
# fixed rather than inherited: a developer's LANG and the runner's can differ,
# and character classes would then mean different things on the two machines.
export LC_ALL=C.UTF-8

# repository

# repo_root prints the absolute path of the repository root. A hook can run with
# any working directory, so every gate resolves paths from here rather than
# assuming it starts at the top.
repo_root() {
	git rev-parse --show-toplevel 2>/dev/null || {
		echo "scripts: not inside a git repository" >&2
		exit 2
	}
}

# reporting

readonly C_RESET=$'\033[0m'
readonly C_RED=$'\033[31m'
readonly C_GREEN=$'\033[32m'
readonly C_YELLOW=$'\033[33m'
readonly C_BOLD=$'\033[1m'

# Colour is disabled when the output is not a terminal, so CI logs and files
# stay free of escape sequences.
if [ -t 1 ]; then
	COLOUR=1
else
	COLOUR=0
fi

_colour() {
	if [ "$COLOUR" = "1" ]; then printf '%s' "$1"; fi
}

gate_start() {
	printf '%s==>%s %s\n' "$(_colour "$C_BOLD")" "$(_colour "$C_RESET")" "$1"
}

gate_pass() {
	printf '%sPASS%s %s\n' "$(_colour "$C_GREEN")" "$(_colour "$C_RESET")" "$1"
}

gate_fail() {
	printf '%sFAIL%s %s\n' "$(_colour "$C_RED")" "$(_colour "$C_RESET")" "$1" >&2
}

gate_skip() {
	printf '%sSKIP%s %s\n' "$(_colour "$C_YELLOW")" "$(_colour "$C_RESET")" "$1"
}

# tools

# have reports whether a command exists on PATH.
have() {
	command -v "$1" >/dev/null 2>&1
}

# require_tool exits non-zero when a tool a gate cannot work without is absent.
# A gate that silently passes because its tool is missing is worse than a gate
# that refuses to run: it reports safety it never checked.
require_tool() {
	local tool="$1"
	local hint="${2:-}"
	if have "$tool"; then
		return 0
	fi
	gate_fail "$tool is not installed${hint:+ ($hint)}"
	return 1
}

# tool_path resolves a tool, preferring PATH but accepting a Go-installed
# binary in GOPATH/bin, which is where `go install` puts tools by default.
tool_path() {
	local tool="$1"
	if have "$tool"; then
		command -v "$tool"
		return 0
	fi
	if have go; then
		local gopath_bin
		gopath_bin="$(go env GOPATH 2>/dev/null)/bin/$tool"
		if [ -x "$gopath_bin" ]; then
			printf '%s\n' "$gopath_bin"
			return 0
		fi
	fi
	return 1
}

# files

# go_service_dirs prints the directories holding Go service modules, one per
# line. Only `app-*/` is considered, which is the scope AGENTS.md 1.1 names,
# and which excludes a developer's local root go.mod for tooling, if any.
# Discovering modules rather than hardcoding app-serve keeps the gates working
# when a second Go service arrives.
go_service_dirs() {
	local root
	root="$(repo_root)"
	find "$root" -maxdepth 2 -path "$root/app-*" -type d -not -path '*/node_modules/*' \
		-exec test -f '{}/go.mod' \; -print 2>/dev/null | sort
}

# changed_files prints the files git considers changed, staged or not, plus
# untracked ones. Used by gates that only need to inspect touched files.
#
# A CI checkout has a clean worktree, so an empty answer there would not be a
# pass but a gate that inspected nothing. GATES_BASE_REF names the revision the
# change is against (the pull request base, or the commit the push started from)
# and adds that diff, which is what the hooks get from staged files for free.
changed_files() {
	local root base
	root="$(repo_root)"
	base="${GATES_BASE_REF:-}"
	# The all-zero SHA is what a branch-creation push reports as its "before".
	# It is a well-formed object name without naming an object, and a diff
	# against it lists no files, which the changed-file gates would read as a
	# pass on an empty patch.
	if [ "$base" = 0000000000000000000000000000000000000000 ]; then
		base=""
	fi
	if [ -n "$base" ] && ! git -C "$root" rev-parse --verify --quiet "$base" >/dev/null; then
		echo "gates: GATES_BASE_REF '$base' does not resolve in this checkout" >&2
		return 1
	fi
	{
		git -C "$root" diff --name-only
		git -C "$root" diff --cached --name-only
		git -C "$root" ls-files --others --exclude-standard
		if [ -n "$base" ]; then
			git -C "$root" diff --name-only "${base}...HEAD"
		fi
	} | sort -u
}

# change_set prints the change set and fails when git could not produce one.
#
# A caller reading changed_files through process substitution never sees its
# exit code, and an unreadable GATES_BASE_REF then lists no files at all, which
# a changed-file gate would read as a clean diff. Capture through this instead.
change_set() {
	local files
	if ! files="$(changed_files)"; then
		gate_fail "the change set could not be read; a gate that inspects nothing is not a pass"
		return 1
	fi
	printf '%s\n' "$files"
}
