#!/usr/bin/env bash
# Self-test for the gate set: proves each gate fails on the violation it claims
# to catch, and passes on a clean tree.
#
# This is a POSITIVE behavior test of the finished gates, not a RED-first unit
# test. The gates are implemented first (steps 1-8 of the task brief); this
# script then builds a throwaway fixture repository containing a small, valid Go
# module, checks that the gates accept it, injects four violations, and asserts
# that the matching gate rejects each one:
#
#   (a) a 251-line Go file under app-serve/internal/  -> go-lint (line limit)
#   (b) a header missing @reason                      -> go-headers
#   (c) a Go comment containing an em dash            -> antislop
#   (d) a fake secret in a staged file                -> secrets --staged
#
# One benign control runs the real gate set against the current tree, which has
# no Go module yet, so the Go gates must SKIP cleanly rather than fail.
#
# The fixture is a copied tree plus `git init`, which is enough for repo_root()
# and for the staged-file and whole-tree gate modes. No network is needed.
#
# Usage:  scripts/gates/self-test.sh
#
# Exit codes: 0 every check behaved as asserted, 1 at least one did not.

set -euo pipefail
. "$(dirname "$0")/../lib/common.sh"

real_root="$(repo_root)"
unset GATES_BASE_REF || true

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fixture="$work/fixture"

checks_pass=0
checks_fail=0

report_pass() { printf '  ok      %s\n' "$1"; checks_pass=$((checks_pass + 1)); }
report_fail() { printf '  NOT OK  %s\n' "$1" >&2; checks_fail=$((checks_fail + 1)); }

indent() { sed 's/^/          /'; }

# run <label> <cmd...> asserts the command exits 0.
run() {
	local label="$1"
	shift
	local out
	if out="$("$@" 2>&1)"; then
		report_pass "$label"
	else
		report_fail "$label (expected exit 0, got non-zero)"
		printf '%s\n' "$out" | indent
	fi
}

# run_fail <label> <needle> <cmd...> asserts the command exits non-zero AND its
# output names the reason. A gate that fails for the wrong reason must not be
# mistaken for a gate that catches the injected violation.
run_fail() {
	local label="$1" needle="$2"
	shift 2
	local out
	if out="$("$@" 2>&1)"; then
		report_fail "$label (expected non-zero, got 0)"
		printf '%s\n' "$out" | indent
	elif printf '%s' "$out" | grep -qF -- "$needle"; then
		report_pass "$label"
	else
		report_fail "$label (failed, but output lacked: $needle)"
		printf '%s\n' "$out" | indent
	fi
}

# gate runs a gate script from inside the fixture, where repo_root resolves.
gate() {
	local name="$1"
	shift
	(cd "$fixture" && bash "scripts/gates/$name" "$@")
}

printf '==> building fixture in %s\n' "$fixture"
mkdir -p "$fixture"
cp -R "$real_root/scripts" "$fixture/scripts"
cp "$real_root/.gitleaks.toml" "$fixture/.gitleaks.toml"
git -C "$fixture" init -q
git -C "$fixture" config user.email gates@self-test.invalid
git -C "$fixture" config user.name "gate self-test"

mkdir -p "$fixture/app-serve/internal/control"
cat >"$fixture/app-serve/go.mod" <<'EOF'
module genauto.test/app-serve

go 1.26
EOF

# The control package is the one file every gate must accept; a failure after
# injection is then attributable to the violation, not to a malformed fixture.
cat >"$fixture/app-serve/internal/control/control.go" <<'EOF'
// Package control is the benign control package for the gate self-test.
//
// @file      internal/control/control.go
// @for       Provides a clean, header-complete Go file for the gate self-test.
// @uses      The Go standard library only.
// @reason    Every gate needs one file it accepts, so an injected violation is the only thing a later failure can be blamed on.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     util
// @stability stable
// @since     2026-10-12
package control

// Answer returns the answer the control test expects.
func Answer() int { return 42 }
EOF

cat >"$fixture/app-serve/internal/control/control_test.go" <<'EOF'
// Package control tests the gate self-test control package.
//
// @file      internal/control/control_test.go
// @for       Proves the fixture module has a test the go-test gate can run.
// @uses      testing
// @reason    A module with no tests would let go-test report a pass it did not earn.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     util
// @stability stable
// @since     2026-10-12
package control

import "testing"

func TestAnswer(t *testing.T) {
	if got := Answer(); got != 42 {
		t.Fatalf("Answer() = %d, want 42", got)
	}
}
EOF

git -C "$fixture" add -A
git -C "$fixture" commit -q -m "fixture base"

printf '\n==> control: the clean current tree passes\n'
run "clean tree: all gates pass (Go gates skip, no module)" bash "$real_root/scripts/gates/all.sh"

printf '\n==> clean fixture: gates accept a valid module\n'
run "clean fixture: go-test passes" gate go-test.sh
run "clean fixture: go-headers --all passes" gate go-headers.sh --all
run "clean fixture: antislop --all passes" gate antislop.sh --all
# go-lint resolves staticcheck through tool_path, which also looks in
# GOPATH/bin. Branch on the same resolution the gate uses, not on PATH alone,
# so this check agrees with the gate on a machine that installed the tool with
# `go install`. The gate must run the tool when it is present, and fail loudly
# when it is absent; both are asserted here.
if tool_path staticcheck >/dev/null; then
	run "clean fixture: go-lint passes" gate go-lint.sh
else
	run_fail "clean fixture: go-lint reports the missing staticcheck" \
		"staticcheck is not installed" gate go-lint.sh
fi

printf '\n==> injecting four violations\n'

# (a) a Go file over the 250-line AGENTS.md 1.1 limit
mkdir -p "$fixture/app-serve/internal/big"
python3 - "$fixture/app-serve/internal/big/big.go" <<'PY'
import sys

header = [
    "// Package big is a fixture that violates the line limit.",
    "//",
    "// @file      internal/big/big.go",
    "// @for       Exercises the AGENTS.md 1.1 line limit in go-lint.",
    "// @uses      Nothing.",
    "// @reason    The self-test needs a file over 250 lines to prove the limit fires.",
    "// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>",
    "// @layer     util",
    "// @stability stable",
    "// @since     2026-10-12",
    "package big",
]
body = [f"// filler line {i}" for i in range(251 - len(header))]
with open(sys.argv[1], "w", encoding="utf-8") as fh:
    fh.write("\n".join(header + body) + "\n")
PY

# (b) a header missing @reason
mkdir -p "$fixture/app-serve/internal/badhdr"
cat >"$fixture/app-serve/internal/badhdr/badhdr.go" <<'EOF'
// Package badhdr is a fixture with an incomplete header.
//
// @file      internal/badhdr/badhdr.go
// @for       Exercises the missing-tag path in go-headers.
// @uses      Nothing.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     util
// @stability stable
// @since     2026-10-12
package badhdr
EOF

# (c) a Go comment containing an em dash, built at run time so this script
# never carries the character it forbids.
mkdir -p "$fixture/app-serve/internal/badcomment"
dash="$(printf '\xe2\x80\x94')"
cat >"$fixture/app-serve/internal/badcomment/badcomment.go" <<EOF
// Package badcomment is a fixture with a banned dash in a body comment.
//
// @file      internal/badcomment/badcomment.go
// @for       Exercises the R-02 dash rule in antislop.
// @uses      Nothing.
// @reason    The self-test needs a comment holding an em dash to prove the rule fires.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     util
// @stability stable
// @since     2026-10-12
package badcomment

// Bad carries an em dash ${dash} which the antislop gate must reject.
func Bad() {}
EOF

# (d) a fake secret in a staged file
mkdir -p "$fixture/app-serve/config"
secret="$(head -c 48 /dev/urandom | od -An -tx1 | tr -d ' \n')"
printf 'TIKTOK_CLIENT_SECRET=%s\n' "$secret" >"$fixture/app-serve/config/leak.txt"

git -C "$fixture" add -A

printf '\n==> asserting each injected violation is caught\n'
run_fail "250-line limit: go-lint rejects a 251-line file" \
	"251 lines" gate go-lint.sh
run_fail "header contract: go-headers rejects a missing @reason" \
	"missing @reason" gate go-headers.sh --all
run_fail "antislop: the em dash comment is rejected" \
	"R-02 dash" gate antislop.sh --all
run_fail "secrets: a staged fake secret is rejected" \
	"secrets detected in staged changes" gate secrets.sh --staged

printf '\n==> %d passed, %d failed\n' "$checks_pass" "$checks_fail"
if [ "$checks_fail" -ne 0 ]; then
	printf 'gate self-test FAILED\n' >&2
	exit 1
fi
printf 'gate self-test passed\n'
exit 0
