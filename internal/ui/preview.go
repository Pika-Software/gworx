package ui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"golang.org/x/image/draw"

	"github.com/Pika-Software/gworx/internal/workshop"
)

const (
	previewSize   = 256                // cached previews are scaled to fit this square
	previewMaxAge = 7 * 24 * time.Hour // refresh cached previews after this
	previewRetry  = 10 * time.Minute   // don't ask again for items without a preview
	previewFetch  = 4                  // parallel image downloads
	gifMinDelay   = 20 * time.Millisecond
)

// preview is a Workshop or local image ready for a thumb widget.
type preview struct {
	still  fyne.Resource // first (or only) frame
	frames []image.Image // set when animated; still matches frames[0]
	delays []time.Duration
}

// previewCache loads Workshop preview images (memory + disk cache) and local
// image files. Lookups never block on the network: they return what is cached
// and queue a download; subscribers are notified when new previews arrive.
type previewCache struct {
	dir string

	mu     sync.Mutex
	mem    map[uint64]*preview
	local  map[string]*preview
	failed map[uint64]time.Time
	queued map[uint64]bool
	subs   []func()
	kick   chan struct{}
}

func newPreviewCache() *previewCache {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	c := &previewCache{
		dir:    filepath.Join(base, "gworx", "previews"),
		mem:    map[uint64]*preview{},
		local:  map[string]*preview{},
		failed: map[uint64]time.Time{},
		queued: map[uint64]bool{},
		kick:   make(chan struct{}, 1),
	}
	go c.loop()
	return c
}

// subscribe registers fn to run on the UI thread whenever previews were loaded.
func (c *previewCache) subscribe(fn func()) {
	c.mu.Lock()
	c.subs = append(c.subs, fn)
	c.mu.Unlock()
}

func (c *previewCache) pngPath(id uint64) string {
	return filepath.Join(c.dir, strconv.FormatUint(id, 10)+".png")
}

func (c *previewCache) gifPath(id uint64) string {
	return filepath.Join(c.dir, strconv.FormatUint(id, 10)+".gif")
}

// workshop returns the preview of a Workshop item, or nil if it isn't loaded yet.
func (c *previewCache) workshop(id uint64) *preview {
	if id == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if p := c.mem[id]; p != nil {
		return p
	}
	var p *preview
	stale := true
	if st, err := os.Stat(c.gifPath(id)); err == nil {
		if data, err := os.ReadFile(c.gifPath(id)); err == nil {
			if loaded, err := previewFromGIF(data); err == nil {
				p = loaded
				stale = time.Since(st.ModTime()) > previewMaxAge
			}
		}
	}
	if p == nil {
		if st, err := os.Stat(c.pngPath(id)); err == nil {
			if data, err := os.ReadFile(c.pngPath(id)); err == nil {
				p = &preview{still: fyne.NewStaticResource(fmt.Sprintf("preview-%d-%d.png", id, st.ModTime().UnixNano()), data)}
				stale = time.Since(st.ModTime()) > previewMaxAge
			}
		}
	}
	if p != nil {
		c.mem[id] = p
	}
	if stale {
		if t, ok := c.failed[id]; (!ok || time.Since(t) > previewRetry) && !c.queued[id] {
			c.queued[id] = true
			select {
			case c.kick <- struct{}{}:
			default:
			}
		}
	}
	return p
}

// file returns a thumbnail of a local image, or nil if it can't be read.
func (c *previewCache) file(path string) *preview {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return nil
	}
	key := fmt.Sprintf("%s|%d|%d", path, st.Size(), st.ModTime().UnixNano())

	c.mu.Lock()
	p, ok := c.local[key]
	c.mu.Unlock()
	if ok {
		return p
	}

	data, err := os.ReadFile(path)
	if err != nil {
		p = nil
	} else if isGIF(data) {
		p, err = previewFromGIF(data)
		if err != nil {
			p = nil
		}
	} else {
		thumb, err := thumbnail(data)
		if err != nil {
			p = nil
		} else {
			p = &preview{still: fyne.NewStaticResource("local-"+filepath.Base(path)+".png", thumb)}
		}
	}
	c.mu.Lock()
	c.local[key] = p
	c.mu.Unlock()
	return p
}

func (c *previewCache) loop() {
	for range c.kick {
		time.Sleep(150 * time.Millisecond) // gather requests from a whole list
		c.mu.Lock()
		ids := make([]uint64, 0, len(c.queued))
		for id := range c.queued {
			ids = append(ids, id)
		}
		c.mu.Unlock()
		if len(ids) > 0 {
			c.fetch(ids)
		}
	}
}

func (c *previewCache) fetch(ids []uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	urls, err := workshop.GetPreviewURLs(ctx, ids...)
	if err != nil {
		urls = map[uint64]string{}
	}
	os.MkdirAll(c.dir, 0o755)

	var wg sync.WaitGroup
	sem := make(chan struct{}, previewFetch)
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var p *preview
			if u := urls[id]; u != "" {
				if data, err := workshop.FetchBytes(ctx, u, 16<<20); err == nil {
					p = c.storeFetched(id, data)
				}
			}

			c.mu.Lock()
			delete(c.queued, id)
			if p != nil {
				c.mem[id] = p
				delete(c.failed, id)
			} else if c.mem[id] == nil {
				c.failed[id] = time.Now()
			}
			c.mu.Unlock()
		}()
	}
	wg.Wait()

	c.mu.Lock()
	subs := append([]func(){}, c.subs...)
	c.mu.Unlock()
	fyne.Do(func() {
		for _, fn := range subs {
			fn()
		}
	})
}

// storeFetched writes a downloaded preview to disk and returns the in-memory form.
func (c *previewCache) storeFetched(id uint64, data []byte) *preview {
	if isGIF(data) {
		if p, err := previewFromGIF(data); err == nil && len(p.frames) > 1 {
			if err := os.WriteFile(c.gifPath(id), data, 0o644); err == nil {
				os.Remove(c.pngPath(id))
				return p
			}
		}
	}
	thumb, err := thumbnail(data)
	if err != nil {
		return nil
	}
	if err := os.WriteFile(c.pngPath(id), thumb, 0o644); err != nil {
		return nil
	}
	os.Remove(c.gifPath(id))
	return &preview{still: fyne.NewStaticResource(fmt.Sprintf("preview-%d-%d.png", id, time.Now().UnixNano()), thumb)}
}

func isGIF(data []byte) bool {
	return len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a")
}

// previewFromGIF decodes a GIF, compositing frames and scaling them to previewSize.
// Single-frame GIFs are returned as a static preview.
func previewFromGIF(data []byte) (*preview, error) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if len(g.Image) == 0 {
		return nil, fmt.Errorf("empty gif")
	}

	w, h := g.Config.Width, g.Config.Height
	if w == 0 || h == 0 {
		b := g.Image[0].Bounds()
		w, h = b.Dx(), b.Dy()
	}
	accum := image.NewNRGBA(image.Rect(0, 0, w, h))
	frames := make([]image.Image, 0, len(g.Image))
	delays := make([]time.Duration, 0, len(g.Image))

	for i, src := range g.Image {
		disposal := byte(0)
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}

		var backup *image.NRGBA
		if disposal == gif.DisposalPrevious {
			backup = cloneNRGBA(accum)
		}

		draw.Draw(accum, src.Bounds(), src, src.Bounds().Min, draw.Over)
		frames = append(frames, scaleToFit(accum, previewSize))

		d := 10 // GIF delay units are 10ms; 0 is commonly treated as 100ms
		if i < len(g.Delay) && g.Delay[i] > 0 {
			d = g.Delay[i]
		}
		delays = append(delays, time.Duration(d)*10*time.Millisecond)

		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(accum, src.Bounds(), image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			if backup != nil {
				draw.Draw(accum, accum.Bounds(), backup, image.Point{}, draw.Src)
			}
		}
	}

	still, err := pngResource("gif-frame0.png", frames[0])
	if err != nil {
		return nil, err
	}
	p := &preview{still: still}
	if len(frames) > 1 {
		p.frames = frames
		p.delays = delays
	}
	return p, nil
}

func cloneNRGBA(src *image.NRGBA) *image.NRGBA {
	dst := image.NewNRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)
	return dst
}

func pngResource(name string, img image.Image) (fyne.Resource, error) {
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return fyne.NewStaticResource(name, out.Bytes()), nil
}

// thumbnail decodes an image (PNG, JPEG or the first GIF frame) and scales it
// to fit previewSize, returning PNG data.
func thumbnail(data []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	scaled := scaleToFit(src, previewSize)
	var out bytes.Buffer
	if err := png.Encode(&out, scaled); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func scaleToFit(src image.Image, maxSide int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return image.NewNRGBA(image.Rect(0, 0, 1, 1))
	}
	if w > maxSide || h > maxSide {
		if w >= h {
			w, h = maxSide, max(1, h*maxSide/w)
		} else {
			w, h = max(1, w*maxSide/h), maxSide
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

// thumb is a square preview image that plays animated GIFs when available.
type thumb struct {
	widget.BaseWidget

	size float32
	img  *canvas.Image

	mu  sync.Mutex
	cur *preview
	gen uint64
}

// newThumb returns an image view showing a placeholder until a preview is set.
func newThumb(size float32) *thumb {
	t := &thumb{
		size: size,
		img:  canvas.NewImageFromResource(theme.FileImageIcon()),
	}
	t.img.FillMode = canvas.ImageFillContain
	t.img.SetMinSize(fyne.NewSquareSize(size))
	t.ExtendBaseWidget(t)
	return t
}

func (t *thumb) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.img)
}

func (t *thumb) MinSize() fyne.Size {
	return fyne.NewSquareSize(t.size)
}

func (t *thumb) Hide() {
	t.mu.Lock()
	t.gen++
	t.mu.Unlock()
	t.BaseWidget.Hide()
}

func (t *thumb) Show() {
	t.BaseWidget.Show()
	t.mu.Lock()
	p := t.cur
	t.gen++
	gen := t.gen
	t.mu.Unlock()
	if p != nil && len(p.frames) >= 2 {
		go t.animate(gen, p)
	}
}

// setThumb shows p, or the placeholder when p is nil.
func setThumb(t *thumb, p *preview) {
	t.setPreview(p)
}

func (t *thumb) setPreview(p *preview) {
	t.mu.Lock()
	if t.cur == p {
		t.mu.Unlock()
		return
	}
	t.cur = p
	t.gen++
	gen := t.gen
	visible := t.Visible()
	t.mu.Unlock()

	res := theme.FileImageIcon()
	if p != nil && p.still != nil {
		res = p.still
	}
	t.img.Resource = res
	t.img.Image = nil
	t.img.Refresh()

	if visible && p != nil && len(p.frames) >= 2 {
		go t.animate(gen, p)
	}
}

func (t *thumb) animate(gen uint64, p *preview) {
	i := 0
	for {
		d := p.delays[i]
		if d < gifMinDelay {
			d = gifMinDelay
		}
		time.Sleep(d)

		t.mu.Lock()
		alive := t.gen == gen
		t.mu.Unlock()
		if !alive {
			return
		}

		i = (i + 1) % len(p.frames)
		frame := p.frames[i]
		fyne.Do(func() {
			t.mu.Lock()
			ok := t.gen == gen
			t.mu.Unlock()
			if !ok {
				return
			}
			t.img.Resource = nil
			t.img.Image = frame
			t.img.Refresh()
		})
	}
}
