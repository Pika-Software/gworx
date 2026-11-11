// Package ansi strips terminal escape sequences from process output.
package ansi

import "regexp"

var (
	// OSC strings, charset selection and CSI sequences other than SGR (colours).
	nonColorRe = regexp.MustCompile(`\x1b\][^\x07]*\x07|\x1b[()][A-Z0-9]|\x1b\[[0-9;?]*[A-ln-z]`)
	// Every OSC, charset and CSI sequence.
	allRe = regexp.MustCompile(`\x1b\][^\x07]*\x07|\x1b[()][A-Z0-9]|\x1b\[[0-9;?]*[A-Za-z]`)
)

// Strip removes every escape sequence.
func Strip(s string) string { return allRe.ReplaceAllString(s, "") }

// StripKeepColor removes cursor, erase and OSC sequences but keeps SGR colour
// codes (\x1b[...m) so the UI can render coloured lines.
func StripKeepColor(s string) string { return nonColorRe.ReplaceAllString(s, "") }
