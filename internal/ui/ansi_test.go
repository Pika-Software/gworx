package ui

import (
	"image/color"
	"testing"
)

func TestStripANSI(t *testing.T) {
	in := "\x1b[31mred\x1b[0m plain \x1b[2K"
	got := stripANSI(in)
	if got != "red plain " {
		t.Fatalf("got %q", got)
	}
}

func TestParseANSI(t *testing.T) {
	spans := parseANSI("\x1b[31merr\x1b[0m ok")
	if len(spans) != 2 {
		t.Fatalf("spans=%d %#v", len(spans), spans)
	}
	if spans[0].Text != "err" || spans[0].Color == nil {
		t.Fatalf("first=%#v", spans[0])
	}
	if spans[1].Text != " ok" || spans[1].Color != nil {
		t.Fatalf("second=%#v", spans[1])
	}
	red, ok := spans[0].Color.(color.NRGBA)
	if !ok || red.R < 200 {
		t.Fatalf("expected red, got %#v", spans[0].Color)
	}
}

func TestParseANSIPlain(t *testing.T) {
	spans := parseANSI("hello")
	if len(spans) != 1 || spans[0].Text != "hello" || spans[0].Color != nil {
		t.Fatalf("%#v", spans)
	}
}
