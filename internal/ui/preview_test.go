package ui

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"testing"
	"time"

	"golang.org/x/image/bmp"
)

func TestThumbnailFlattensGIF(t *testing.T) {
	thumb, err := thumbnail(multiGIF(300, 100, 3))
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(thumb))
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" {
		t.Fatalf("format = %s, want png", format)
	}
	if cfg.Width != previewSize || cfg.Height != 85 {
		t.Fatalf("size = %dx%d, want %dx85", cfg.Width, cfg.Height, previewSize)
	}
	if len(thumb) > 64<<10 {
		t.Fatalf("thumbnail is %d bytes", len(thumb))
	}
}

func TestThumbnailFlattensAnimatedWebP(t *testing.T) {
	for _, name := range []string{"testdata/anim.webp", "testdata/alpha.webp"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := image.Decode(bytes.NewReader(data)); err == nil {
			t.Fatalf("%s decoded without conversion", name)
		}
		thumb, err := thumbnail(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(thumb))
		if err != nil {
			t.Fatal(err)
		}
		if format != "png" || cfg.Width == 0 || cfg.Height == 0 {
			t.Fatalf("%s -> %s %dx%d", name, format, cfg.Width, cfg.Height)
		}
	}
}

func TestThumbnailDecodesBMP(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 4))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := bmp.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	thumb, err := thumbnail(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(thumb))
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" || cfg.Width != 8 || cfg.Height != 4 {
		t.Fatalf("got %s %dx%d", format, cfg.Width, cfg.Height)
	}
}

func TestLookupURLsUsesOwnerForHiddenItems(t *testing.T) {
	c := testCache(t)
	c.publicPreviews = func(context.Context, ...uint64) (map[uint64]string, error) {
		return map[uint64]string{1: "https://cdn.example/1.jpg"}, nil
	}
	c.ownerPreviews = func(_ context.Context, ids []uint64) (map[uint64]string, error) {
		if len(ids) != 1 || ids[0] != 2 {
			t.Errorf("owner lookup ids = %v", ids)
		}
		return map[uint64]string{2: "https://cdn.example/2.jpg"}, nil
	}
	urls, failed := c.lookupURLs(context.Background(), []uint64{1, 2})
	if failed {
		t.Fatal("lookup reported a failure")
	}
	if urls[1] != "https://cdn.example/1.jpg" || urls[2] != "https://cdn.example/2.jpg" {
		t.Fatalf("urls = %v", urls)
	}
}

func TestLookupURLsRetriesWhenOwnerFails(t *testing.T) {
	c := testCache(t)
	c.publicPreviews = func(context.Context, ...uint64) (map[uint64]string, error) {
		return map[uint64]string{}, nil
	}
	c.ownerPreviews = func(context.Context, []uint64) (map[uint64]string, error) {
		return nil, errors.New("steam is not running")
	}
	if _, failed := c.lookupURLs(context.Background(), []uint64{9}); !failed {
		t.Fatal("owner failure was treated as a permanent miss")
	}
	if c.ownerSkip.Before(time.Now()) {
		t.Fatal("owner lookup was not backed off")
	}
}

func TestThumbnailRejectsHugeImage(t *testing.T) {
	// Logical screen is enough for DecodeConfig; no frame data follows.
	hdr := append([]byte("GIF89a"), 0x88, 0x13, 0x88, 0x13, 0x00, 0x00, 0x00) // 5000x5000
	if _, err := thumbnail(hdr); err == nil {
		t.Fatal("expected oversized gif to be rejected before decode")
	}
}

func TestWorkshopDoesNotDecodeGIF(t *testing.T) {
	c := testCache(t)
	if err := os.WriteFile(c.gifPath(7), multiGIF(400, 400, 12), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if p := c.workshop(7); p != nil {
		t.Fatal("gif decoded on the caller")
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("workshop took %s", elapsed)
	}
	if !c.queued[7] {
		t.Fatal("gif was not queued")
	}
	if c.mem[7] != nil {
		t.Fatal("gif stored inline")
	}
}

func TestWorkshopReturnsCachedPNG(t *testing.T) {
	c := testCache(t)
	var buf bytes.Buffer
	if err := png.Encode(&buf, solidPaletted(4, 4, color.RGBA{R: 255, A: 255})); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.pngPath(9), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	p := c.workshop(9)
	if p == nil || p.still == nil {
		t.Fatal("missing cached preview")
	}
	if c.queued[9] {
		t.Fatal("fresh png should not be refetched")
	}
	if got := c.workshop(9); got != p {
		t.Fatal("second lookup missed memory")
	}
}

func TestDiskPreviewConvertsLegacyGIF(t *testing.T) {
	c := testCache(t)
	if err := os.WriteFile(c.gifPath(4), multiGIF(32, 16, 4), 0o644); err != nil {
		t.Fatal(err)
	}
	p, _, ok := c.diskPreview(4)
	if !ok || p == nil || p.still == nil {
		t.Fatal("legacy gif was not converted")
	}
	if _, err := os.Stat(c.gifPath(4)); !os.IsNotExist(err) {
		t.Fatal("gif cache file still present")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(p.still.Content()))
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" || cfg.Width != 32 || cfg.Height != 16 {
		t.Fatalf("got %s %dx%d", format, cfg.Width, cfg.Height)
	}
}

func TestMemCacheEvictsOldest(t *testing.T) {
	c := testCache(t)
	for id := uint64(0); id < previewMemMax; id++ {
		c.putMemLocked(id, &preview{})
	}
	c.putMemLocked(previewMemMax, &preview{})
	if _, ok := c.mem[0]; ok {
		t.Fatal("oldest preview was kept")
	}
	if _, ok := c.mem[previewMemMax]; !ok {
		t.Fatal("newest preview missing")
	}
	if len(c.mem) > previewMemMax {
		t.Fatalf("mem = %d, cap %d", len(c.mem), previewMemMax)
	}
}

func TestLocalCacheDropsPreviousVersion(t *testing.T) {
	c := testCache(t)
	c.rememberLocalLocked("/tmp/a.png", "/tmp/a.png|1|1", &preview{})
	c.rememberLocalLocked("/tmp/a.png", "/tmp/a.png|2|2", &preview{})
	if _, ok := c.local["/tmp/a.png|1|1"]; ok {
		t.Fatal("old local preview kept")
	}
	if _, ok := c.local["/tmp/a.png|2|2"]; !ok {
		t.Fatal("new local preview missing")
	}
}

func TestQueueSkipsRecentFailure(t *testing.T) {
	c := testCache(t)
	c.failed[3] = time.Now().Add(time.Minute)
	if p := c.workshop(3); p != nil {
		t.Fatal("unexpected preview")
	}
	if c.queued[3] {
		t.Fatal("failure was retried immediately")
	}
}

func testCache(t *testing.T) *previewCache {
	t.Helper()
	return &previewCache{
		dir:      t.TempDir(),
		mem:      map[uint64]*preview{},
		local:    map[string]*preview{},
		inflight: map[string]string{},
		failed:   map[uint64]time.Time{},
		queued:   map[uint64]bool{},
		kick:     make(chan struct{}, 1),
	}
}

func multiGIF(w, h, frames int) []byte {
	g := &gif.GIF{Config: image.Config{Width: w, Height: h}}
	colors := []color.Color{
		color.RGBA{R: 200, A: 255},
		color.RGBA{G: 180, A: 255},
		color.RGBA{B: 220, A: 255},
	}
	for i := 0; i < frames; i++ {
		g.Image = append(g.Image, solidPaletted(w, h, colors[i%len(colors)]))
		g.Delay = append(g.Delay, 2)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func solidPaletted(w, h int, c color.Color) *image.Paletted {
	pal := color.Palette{color.RGBA{}, c}
	img := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	for i := range img.Pix {
		img.Pix[i] = 1
	}
	return img
}
