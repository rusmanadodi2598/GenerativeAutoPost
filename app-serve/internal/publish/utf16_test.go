// Package publish verifies the UTF-16 length helper against the TikTok unit.
//
// @file      internal/publish/utf16_test.go
// @for       Table test for utf16Len code unit counting.
// @uses      testing, unicode/utf16
// @reason    The 2200 and 90 caps are counted in UTF-16 units, not bytes or runes.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package publish

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestUtf16LenCountsCodeUnits(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  int
	}{
		{name: "empty", input: "", want: 0},
		{name: "ascii", input: "hello", want: 5},
		{name: "bmp multibyte", input: "café 你好", want: 7},
		{name: "one astral char", input: "a😀", want: 3},
		{name: "mixed", input: "hi 😀 é 好", want: 9},
		{name: "lone high surrogate", input: string([]rune{0xd800}), want: 1},
		{name: "lone low surrogate", input: string([]rune{0xdc00}), want: 1},
		{name: "video boundary", input: strings.Repeat("a", 2200), want: 2200},
		{name: "astral halves title", input: strings.Repeat("😀", 1100), want: 2200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := utf16Len(tc.input)
			if got != tc.want {
				t.Fatalf("utf16Len(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

// TestUtf16LenAgreesWithStdlib pins the helper to the standard library on
// well formed text, so a hand rolled surrogate check cannot drift.
func TestUtf16LenAgreesWithStdlib(t *testing.T) {
	input := "plain 😀 text é"
	if got, want := utf16Len(input), len(utf16.Encode([]rune(input))); got != want {
		t.Fatalf("utf16Len = %d, want %d", got, want)
	}
}
