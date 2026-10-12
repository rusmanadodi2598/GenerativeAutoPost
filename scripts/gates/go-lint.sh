#!/usr/bin/env bash
# Gate: Go static analysis for every service module under app-*/.
#
# Runs the checks AGENTS.md 1.4 names as CI requirements: `go vet` clean,
# `staticcheck` clean, `gofmt` formatting, the integration-tagged vet, and the
# AGENTS.md 1.1 line limit on touched hand-written sources.
#
# staticcheck is required: a missing binary fails the gate rather than passing
# silently. golangci-lint is optional and is reported as skipped when absent.
#
# Exit codes: 0 all checks passed (or no Go module exists), 1 a check failed.

set -euo pipefail
. "$(dirname "$0")/../lib/common.sh"

root="$(repo_root)"
dirs="$(go_service_dirs)"

if [ -z "$dirs" ]; then
	gate_skip "no Go module found (looked for app-*/go.mod)"
	exit 0
fi

require_tool go || exit 1

failed=0

# run_in prints the module and executes the given command inside it.
run_in() {
	local dir="$1"
	shift
	(cd "$dir" && "$@")
}

# check_changed_line_limits enforces AGENTS.md 1.1 on touched hand-written
# sources. Generated output is the only exclusion, because 1.1 grants exactly
# that one. A warning at 220 lines gives the change owner a split point before
# the gate rejects a file at 251.
check_changed_line_limits() {
	local rel file lines files failed_limit=0 warned=0
	if ! files="$(change_set)"; then
		gate_fail "line-limit check could not read its change set (AGENTS.md 1.1)"
		return 1
	fi
	while IFS= read -r rel; do
		[ -n "$rel" ] || continue
		case "$rel" in
		app-*/internal/*.go | app-*/internal/*/*.go | app-*/cmd/*.go | app-*/cmd/*/*.go) ;;
		*) continue ;;
		esac
		case "$rel" in
		*_gen.go | *.pb.go | */vendor/*) continue ;;
		esac
		file="$root/$rel"
		[ -f "$file" ] || continue
		lines="$(wc -l <"$file")"
		if [ "$lines" -gt 250 ]; then
			gate_fail "$rel has $lines lines (AGENTS.md 1.1 max 250)"
			failed_limit=1
		elif [ "$lines" -gt 220 ]; then
			gate_start "$rel has $lines lines (warning at 220; split before 250)"
			warned=1
		fi
	done <<<"$files"
	[ "$warned" = "1" ] && gate_skip "line-limit warnings require review"
	return "$failed_limit"
}

# The line limit is a change-set check, so it runs once for the whole repo
# rather than once per module.
if ! check_changed_line_limits; then
	failed=1
fi

if ! sc="$(tool_path staticcheck)"; then
	gate_fail "staticcheck is not installed (AGENTS.md 1.4 requires it)"
	gate_fail "install: go install honnef.co/go/tools/cmd/staticcheck@latest"
	exit 1
fi

gl=""
if gl="$(tool_path golangci-lint)"; then
	:
else
	gl=""
	gate_skip "golangci-lint not installed; the aggregated linter did not run"
	gate_skip "install: https://golangci-lint.run/welcome/install/"
fi

while IFS= read -r dir; do
	[ -n "$dir" ] || continue
	rel="${dir#"$root"/}"

	gate_start "go vet          $rel"
	if run_in "$dir" go vet ./...; then
		gate_pass "go vet $rel"
	else
		gate_fail "go vet $rel"
		failed=1
	fi

	# The integration-tagged files are excluded from ./... by their build tag,
	# so they are vetted separately; a tagged file that does not compile would
	# otherwise go unnoticed until the integration suite runs.
	gate_start "go vet (tagged) $rel"
	if run_in "$dir" go vet -tags=integration ./...; then
		gate_pass "go vet -tags=integration $rel"
	else
		gate_fail "go vet -tags=integration $rel"
		failed=1
	fi

	gate_start "gofmt           $rel"
	unformatted="$(run_in "$dir" gofmt -l . || true)"
	if [ -z "$unformatted" ]; then
		gate_pass "gofmt $rel"
	else
		gate_fail "gofmt $rel: these files need formatting"
		printf '%s\n' "$unformatted" >&2
		failed=1
	fi

	gate_start "staticcheck     $rel"
	if run_in "$dir" "$sc" ./...; then
		gate_pass "staticcheck $rel"
	else
		gate_fail "staticcheck $rel"
		failed=1
	fi

	gate_start "staticcheck (tagged) $rel"
	if run_in "$dir" "$sc" -tags=integration ./...; then
		gate_pass "staticcheck -tags=integration $rel"
	else
		gate_fail "staticcheck -tags=integration $rel"
		failed=1
	fi

	if [ -n "$gl" ]; then
		gate_start "golangci-lint   $rel"
		if run_in "$dir" "$gl" run ./...; then
			gate_pass "golangci-lint $rel"
		else
			gate_fail "golangci-lint $rel"
			failed=1
		fi
	fi
done <<<"$dirs"

exit "$failed"
