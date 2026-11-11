package ui

import (
	"image/color"
	"testing"
)

func TestStripANSI(t *testing.T) {
	in := "\x1b]0;title\x07\x1b[31mred\x1b[0m plain \x1b[2K\x1b(B"
	if got := stripANSI(in); got != "red plain " {
		t.Fatalf("got %q", got)
	}
}

func TestParseANSI(t *testing.T) {
	type span struct {
		text  string
		color color.Color
	}
	red := ansiBasic(1, false)
	for _, tc := range []struct {
		in   string
		want []span
	}{
		{"", nil},
		{"hello", []span{{"hello", nil}}},
		{"\x1b[31merr\x1b[0m ok", []span{{"err", red}, {" ok", nil}}},
		{"\x1b[1;31mbold\x1b[39mreset", []span{{"bold", ansiBasic(1, true)}, {"reset", nil}}},
		{"\x1b[92mbright", []span{{"bright", ansiBasic(2, true)}}},
		{"\x1b[38;5;196mcube", []span{{"cube", color.NRGBA{255, 0, 0, 255}}}},
		{"\x1b[38;5;232mgrey", []span{{"grey", color.NRGBA{8, 8, 8, 255}}}},
		{"\x1b[38;2;1;2;300mrgb", []span{{"rgb", color.NRGBA{1, 2, 255, 255}}}},
		{"\x1b[31ma\x1b[2Kb", []span{{"ab", red}}}, // non-SGR is stripped, colour kept
	} {
		got := parseANSI(tc.in)
		ok := len(got) == len(tc.want)
		for i := 0; ok && i < len(got); i++ {
			ok = got[i].Text == tc.want[i].text && got[i].Color == tc.want[i].color
		}
		if !ok {
			t.Errorf("parseANSI(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}
