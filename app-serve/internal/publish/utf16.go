// Package publish measures the TikTok length limits in their own unit.
//
// @file      internal/publish/utf16.go
// @for       UTF-16 code unit counting for title and description caps.
// @uses      unicode/utf16
// @reason    TikTok caps titles and descriptions in UTF-16 units, not bytes or runes.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package publish

import "unicode/utf16"

// utf16Len counts the UTF-16 code units of s, which is the unit TikTok limits
// titles and descriptions in. A surrogate pair counts 2 and a lone surrogate
// counts 1 each, so invalid UTF-16 is measured rather than rejected.
func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}
