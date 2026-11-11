package ui

import (
	"image/color"
	"regexp"
	"strconv"
	"strings"

	"fyne.io/fyne/v2/theme"
)

// Matches any CSI/OSC/charset ANSI sequence.
var ansiAllRe = regexp.MustCompile(`\x1b\][^\x07]*\x07|\x1b[()][A-Z0-9]|\x1b\[[0-9;?]*[A-Za-z]`)

// ansiSGRRe finds SGR color sequences (\x1b[...m).
var ansiSGRRe = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

type ansiSpan struct {
	Text  string
	Color color.Color // nil means theme foreground
}

func stripANSI(s string) string {
	return ansiAllRe.ReplaceAllString(s, "")
}

// parseANSI splits s into spans using SGR color codes. Other ANSI is stripped.
func parseANSI(s string) []ansiSpan {
	if s == "" {
		return nil
	}
	if !strings.ContainsRune(s, '\x1b') {
		return []ansiSpan{{Text: s}}
	}

	var out []ansiSpan
	var fg color.Color
	rest := s
	for {
		loc := ansiSGRRe.FindStringSubmatchIndex(rest)
		if loc == nil {
			text := stripANSI(rest)
			if text != "" {
				out = append(out, ansiSpan{Text: text, Color: fg})
			}
			break
		}
		before := stripANSI(rest[:loc[0]])
		if before != "" {
			out = append(out, ansiSpan{Text: before, Color: fg})
		}
		fg = applySGR(fg, rest[loc[2]:loc[3]])
		rest = rest[loc[1]:]
	}
	if len(out) == 0 {
		return []ansiSpan{{Text: stripANSI(s)}}
	}
	return out
}

func applySGR(cur color.Color, params string) color.Color {
	if params == "" {
		return nil
	}
	parts := strings.Split(params, ";")
	bold := false
	for i := 0; i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			continue
		}
		switch {
		case n == 0:
			cur = nil
			bold = false
		case n == 1:
			bold = true
		case n == 22:
			bold = false
		case n == 39:
			cur = nil
		case n >= 30 && n <= 37:
			cur = ansiBasic(n-30, bold)
		case n >= 90 && n <= 97:
			cur = ansiBasic(n-90, true)
		case n == 38 && i+1 < len(parts):
			cur, i = parseExtended(parts, i+1)
		}
	}
	return cur
}

func parseExtended(parts []string, i int) (color.Color, int) {
	mode, err := strconv.Atoi(parts[i])
	if err != nil {
		return nil, i
	}
	switch mode {
	case 5: // 256-color
		if i+1 >= len(parts) {
			return nil, i
		}
		n, err := strconv.Atoi(parts[i+1])
		if err != nil {
			return nil, i
		}
		return ansi256(n), i + 1
	case 2: // truecolor
		if i+3 >= len(parts) {
			return nil, i
		}
		r, e1 := strconv.Atoi(parts[i+1])
		g, e2 := strconv.Atoi(parts[i+2])
		b, e3 := strconv.Atoi(parts[i+3])
		if e1 != nil || e2 != nil || e3 != nil {
			return nil, i
		}
		return color.NRGBA{R: uint8(clamp8(r)), G: uint8(clamp8(g)), B: uint8(clamp8(b)), A: 255}, i + 3
	}
	return nil, i
}

func clamp8(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

func ansiBasic(i int, bright bool) color.Color {
	// Readable on the input-background console panel.
	normal := [8]color.NRGBA{
		{80, 80, 80, 255},    // black -> visible grey
		{220, 50, 47, 255},   // red
		{50, 170, 70, 255},   // green
		{200, 160, 30, 255},  // yellow
		{60, 120, 220, 255},  // blue
		{180, 70, 180, 255},  // magenta
		{40, 170, 190, 255},  // cyan
		{200, 200, 200, 255}, // white
	}
	brightC := [8]color.NRGBA{
		{120, 120, 120, 255},
		{255, 90, 90, 255},
		{80, 220, 100, 255},
		{240, 200, 50, 255},
		{100, 160, 255, 255},
		{220, 120, 220, 255},
		{80, 220, 230, 255},
		{255, 255, 255, 255},
	}
	if i < 0 || i > 7 {
		return theme.Color(theme.ColorNameForeground)
	}
	if bright {
		return brightC[i]
	}
	return normal[i]
}

func ansi256(n int) color.Color {
	if n < 0 {
		n = 0
	}
	if n < 8 {
		return ansiBasic(n, false)
	}
	if n < 16 {
		return ansiBasic(n-8, true)
	}
	if n < 232 {
		n -= 16
		b := n % 6
		n /= 6
		g := n % 6
		r := n / 6
		return color.NRGBA{R: uint8(ansiCube(r)), G: uint8(ansiCube(g)), B: uint8(ansiCube(b)), A: 255}
	}
	if n > 255 {
		n = 255
	}
	v := uint8(8 + (n-232)*10)
	return color.NRGBA{R: v, G: v, B: v, A: 255}
}

func ansiCube(v int) int {
	if v == 0 {
		return 0
	}
	return 55 + v*40
}
