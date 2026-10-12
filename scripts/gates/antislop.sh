#!/usr/bin/env bash
# Gate: the antislop comment rules, checked by machine instead of by reading.
#
# Three rules are enforced here, all on hand-written Go:
#
#   - no em dash or en dash in a comment
#   - a 1.2 header region whose field value wraps onto a continuation line
#   - scratch-work citations (`draft N`, `audit N`) below the package line,
#     where a citation is comment debt rather than the mandated @reason text
#
# A comment line may opt out with a trailing `antislop:ignore` marker, for text
# quoting a violation rather than committing one. Every use stays visible to a
# reviewer because the marker text is on the line that would have failed.
#
# Generated and vendored output is out of scope. A missing change set fails
# rather than passing: an unreadable GATES_BASE_REF lists no files, which would
# otherwise read as a clean diff.
#
# Usage:  antislop.sh          check changed Go files (dash and structural
#                              checks stay tree-wide)
#         antislop.sh --all    check the whole tree
#
# Exit codes: 0 clean, 1 at least one violation, 2 a required tool is missing.

set -euo pipefail
. "$(dirname "$0")/../lib/common.sh"

root="$(repo_root)"
mode="changed"
if [ "${1:-}" = "--all" ]; then
	mode="all"
fi

readonly SELF="scripts/gates/antislop.sh"

# The characters antislop forbids outright: em dash and en dash.
# $'\u2014' / $'\u2013' keep the literal glyphs out of this file, so the gate
# never trips its own rule.
readonly DASH_RE=$'\u2014|\u2013'
# Scratch-work citations: planning drafts and audit passes, which rot when they
# close. A SPEC or AGENTS.md section reference is NOT in this pattern.
readonly CITATION_RE='[dD]raft [0-9]+ +[FR§]|[dD]raft [0-9]+$|audit anti-slop [0-9]+|audit [0-9]+'
# A field value continued on a tab-indented line: what gofmt rewrites a
# hand-wrapped value into.
readonly FIELD_CONT_RE='^//\t'
# A field value whose text ends here has finished its sentence.
readonly FIELD_TAG_RE='^// (@(file|for|uses|reason|author|layer|stability|since))[[:space:]]+'
readonly FIELD_ENDS_RE='[.!?)"][[:space:]]*$'

# generated globs are the outputs no one hand-wrote.
readonly GO_EXCLUDES=(
	':(exclude)**/node_modules/**'
	':(exclude)**/vendor/**'
	':(exclude)**/*_gen.go'
	':(exclude)*.pb.go'
)

# grep_files runs one pattern over the given path globs, minus lines opted out.
grep_files() {
	local re="$1"
	shift
	git -C "$root" grep -nIE "$re" -- "$@" 2>/dev/null | grep -v 'antislop:ignore' || true
}

# go_paths lists tracked hand-authored Go files.
go_paths() {
	git -C "$root" ls-files -- '*.go' "${GO_EXCLUDES[@]}" | sort -u
}

# changed_go_paths narrows that list to the change set. An unreadable change
# set is a failure, not an empty one, because an empty list is also what a clean
# diff looks like.
changed_go_paths() {
	local files
	if ! files="$(change_set)"; then
		return 1
	fi
	comm -12 <(go_paths) <(printf '%s\n' "$files" | sort -u)
}

# citation_paths lists the Go files the body-citation rule reads.
citation_paths() {
	if [ "$mode" = "all" ]; then
		go_paths
	else
		changed_go_paths
	fi
}

# go_body_comments prints only the comment text below a Go file's `package`
# line, which is where the 1.2 mandated header ends. The string part of a line
# is dropped first, so a citation printed by a test does not read as comment
# debt the author never wrote.
go_body_comments() {
	local file="$1" pkg
	pkg="$(grep -n '^package ' "$root/$file" | head -1 | cut -d: -f1 || true)"
	awk -v start="${pkg:-0}" '
		NR > start {
			pos = index($0, "//")
			if (pos == 0) next
			prefix = substr($0, 1, pos - 1)
			if (gsub(/"/, "\"", prefix) % 2 == 1) next
			print substr($0, pos)
		}' "$root/$file"
}

# header_shape_hits prints every 1.2 header line whose value continues onto a
# tab-indented line. The header region is everything above `package`.
header_shape_hits() {
	# Paths contain no spaces here, and awk needs one argument per file.
	# shellcheck disable=SC2046
	awk -v tagre="$FIELD_TAG_RE" -v contre="$FIELD_CONT_RE" -v endsre="$FIELD_ENDS_RE" '
		FNR == 1              { inhdr = 1; anchor = "" }
		/^package[[:space:]]/ { inhdr = 0 }
		inhdr == 0            { next }
		$0 ~ tagre            { anchor = $0; next }
		/^\/\/[ \t]*$/        { next }
		$0 ~ contre {
			if (anchor != "") {
				frag = $0
				sub(/^\/\/\t+/, "", frag)
				# An indented block after a finished sentence is structure rather than
				# a wrapped value: runnable `go test` lines are written this way.
				if (anchor ~ endsre && frag !~ /^[a-z]/) {
					anchor = ""
					next
				}
				printf "%s:%d: %s\n", FILENAME, FNR, substr($0, 1, 90)
				anchor = anchor " " frag
			}
			next
		}
		{ anchor = "" }
	' $(git -C "$root" ls-files -- '*.go' "${GO_EXCLUDES[@]}")
}

# report prints each hit and returns 1 when there were any.
report() {
	local label="$1" hits="$2"
	if [ -z "$hits" ]; then
		gate_pass "$label"
		return 0
	fi
	local n
	n="$(printf '%s\n' "$hits" | grep -c .)"
	printf '%s\n' "$hits" | head -n 8 | while IFS= read -r line; do
		gate_fail "$label: $(printf '%s' "$line" | cut -c1-150)"
	done
	[ "$n" -gt 8 ] && gate_fail "$label: ...and $((n - 8)) more"
	return 1
}

failed=0

# ---- R-02 no em or en dash (tree-wide, comments and prose alike) ----

gate_start "R-02 no em or en dash (tree-wide)"
dash_hits="$(grep_files "$DASH_RE" '*.go' "${GO_EXCLUDES[@]}" ":(exclude)$SELF")"
report "R-02 dash" "$dash_hits" || failed=1

# ---- citations, on the change set by default ----

gate_start "scratch-work citations"
if ! scope="$(citation_paths)"; then
	gate_fail "antislop gate could not read the change set (GATES_BASE_REF is set but does not resolve)"
	exit 1
fi
if [ -z "$scope" ]; then
	gate_skip "no Go files in scope"
else
	cite_fail=0
	while IFS= read -r f; do
		[ -n "$f" ] || continue
		[ -f "$root/$f" ] || continue
		c="$(go_body_comments "$f" | grep -E "$CITATION_RE" | grep -v 'antislop:ignore' || true)"
		if [ -n "$c" ]; then
			gate_fail "$f cites a closed draft in a comment: $(printf '%s\n' "$c" | head -1 | cut -c1-110)"
			cite_fail=1
		fi
	done <<<"$scope"
	if [ "$cite_fail" = "1" ]; then
		failed=1
	else
		gate_pass "scratch-work citations"
	fi
fi

# ---- 1.2 header field shape (tree-wide) ----

report "1.2 header field shape (tree-wide)" "$(header_shape_hits)" || failed=1

if [ "$failed" -ne 0 ]; then
	gate_fail "antislop gate found violations above"
	exit 1
fi
gate_pass "antislop gate"
exit 0
