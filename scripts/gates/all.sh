#!/usr/bin/env bash
# Runs the full gate set locally, without committing or pushing.
#
# Useful before opening a pull request, and as the single command CI can call so
# the pipeline and the hooks enforce exactly the same rules.
#
# Usage:  scripts/gates/all.sh
#
# Exit codes: 0 all gates passed, 1 at least one gate failed.

set -euo pipefail
. "$(dirname "$0")/../lib/common.sh"

root="$(repo_root)"
failed=0
declare -a summary=()

run_gate() {
	local label="$1"
	shift
	gate_start "$label"
	if "$@"; then
		gate_pass "$label"
		summary+=("PASS  $label")
	else
		gate_fail "$label"
		summary+=("FAIL  $label")
		failed=1
	fi
}

run_gate "go lint (vet, gofmt, staticcheck, line limits)" bash "$root/scripts/gates/go-lint.sh"
run_gate "go headers (AGENTS.md 1.2)" bash "$root/scripts/gates/go-headers.sh" --all
run_gate "antislop comment rules" bash "$root/scripts/gates/antislop.sh" --all
run_gate "go test (race)" bash "$root/scripts/gates/go-test.sh"
run_gate "secrets (gitleaks)" bash "$root/scripts/gates/secrets.sh" --all

printf '\n==> summary\n'
printf '%s\n' "${summary[@]}"

if [ "$failed" -ne 0 ]; then
	printf '\none or more gates failed\n' >&2
	exit 1
fi
printf '\nall gates passed\n'
