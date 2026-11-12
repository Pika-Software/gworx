package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"testing"
	"time"
)

func TestPreviewFromGIFAnimated(t *testing.T) {
	var buf bytes.Buffer
	err := gif.EncodeAll(&buf, &gif.GIF{
		Image: []*image.Paletted{
			solidPaletted(10, 10, color.RGBA{R: 255, A: 255}),
			solidPaletted(10, 10, color.RGBA{G: 255, A: 255}),
			solidPaletted(10, 10, color.RGBA{B: 255, A: 255}),
		},
		Delay:    []int{5, 10, 15},
		Disposal: []byte{gif.DisposalNone, gif.DisposalNone, gif.DisposalNone},
		Config:   image.Config{Width: 10, Height: 10},
	})
	if err != nil {
		t.Fatal(err)
	}

	p, err := previewFromGIF(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if p.still == nil {
		t.Fatal("missing still frame")
	}
	if len(p.frames) != 3 {
		t.Fatalf("frames = %d, want 3", len(p.frames))
	}
	want := []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 150 * time.Millisecond}
	for i, d := range p.delays {
		if d != want[i] {
			t.Fatalf("delay[%d] = %v, want %v", i, d, want[i])
		}
	}
}

func TestPreviewFromGIFSingleFrame(t *testing.T) {
	var buf bytes.Buffer
	err := gif.EncodeAll(&buf, &gif.GIF{
		Image:  []*image.Paletted{solidPaletted(8, 8, color.RGBA{R: 1, A: 255})},
		Delay:  []int{0},
		Config: image.Config{Width: 8, Height: 8},
	})
	if err != nil {
		t.Fatal(err)
	}

	p, err := previewFromGIF(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if p.still == nil {
		t.Fatal("missing still frame")
	}
	if len(p.frames) != 0 {
		t.Fatalf("single-frame GIF should be static, got %d frames", len(p.frames))
	}
}

func solidPaletted(w, h int, c color.RGBA) *image.Paletted {
	pal := color.Palette{color.RGBA{}, c}
	img := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetColorIndex(x, y, 1)
		}
	}
	return img
}
