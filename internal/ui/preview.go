package ui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
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
	"golang.org/x/image/draw"

	"github.com/Pika-Software/gworx/internal/workshop"
)

const (
	previewSize   = 256                // cached previews are scaled to fit this square
	previewMaxAge = 7 * 24 * time.Hour // refresh cached previews after this
	previewRetry  = 10 * time.Minute   // don't ask again for items without a preview
	previewFetch  = 4                  // parallel image downloads
)

// previewCache loads Workshop preview images (memory + disk cache) and local
// image files. Lookups never block on the network: they return what is cached
// and queue a download; subscribers are notified when new previews arrive.
type previewCache struct {
	dir string

	mu     sync.Mutex
	mem    map[uint64]fyne.Resource
	local  map[string]fyne.Resource
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
		mem:    map[uint64]fyne.Resource{},
		local:  map[string]fyne.Resource{},
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

func (c *previewCache) path(id uint64) string {
	return filepath.Join(c.dir, strconv.FormatUint(id, 10)+".png")
}

// workshop returns the preview of a Workshop item, or nil if it isn't loaded yet.
func (c *previewCache) workshop(id uint64) fyne.Resource {
	if id == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if r := c.mem[id]; r != nil {
		return r
	}
	var res fyne.Resource
	stale := true
	if st, err := os.Stat(c.path(id)); err == nil {
		if data, err := os.ReadFile(c.path(id)); err == nil {
			res = fyne.NewStaticResource(fmt.Sprintf("preview-%d-%d.png", id, st.ModTime().UnixNano()), data)
			c.mem[id] = res
			stale = time.Since(st.ModTime()) > previewMaxAge
		}
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
	return res
}

// file returns a thumbnail of a local image, or nil if it can't be read.
func (c *previewCache) file(path string) fyne.Resource {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return nil
	}
	key := fmt.Sprintf("%s|%d|%d", path, st.Size(), st.ModTime().UnixNano())

	c.mu.Lock()
	r, ok := c.local[key]
	c.mu.Unlock()
	if ok {
		return r
	}

	data, err := os.ReadFile(path)
	if err == nil {
		data, err = thumbnail(data)
	}
	if err != nil {
		r = nil
	} else {
		r = fyne.NewStaticResource("local-"+filepath.Base(path)+".png", data)
	}
	c.mu.Lock()
	c.local[key] = r
	c.mu.Unlock()
	return r
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

			var res fyne.Resource
			if u := urls[id]; u != "" {
				if data, err := workshop.FetchBytes(ctx, u, 16<<20); err == nil {
					if thumb, err := thumbnail(data); err == nil {
						os.WriteFile(c.path(id), thumb, 0o644)
						res = fyne.NewStaticResource(fmt.Sprintf("preview-%d-%d.png", id, time.Now().UnixNano()), thumb)
					}
				}
			}

			c.mu.Lock()
			delete(c.queued, id)
			if res != nil {
				c.mem[id] = res
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

// thumbnail decodes an image (PNG, JPEG or the first GIF frame) and scales it
// to fit previewSize, returning PNG data.
func thumbnail(data []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, fmt.Errorf("empty image")
	}
	if w > previewSize || h > previewSize {
		if w >= h {
			w, h = previewSize, max(1, h*previewSize/w)
		} else {
			w, h = max(1, w*previewSize/h), previewSize
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)

	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// newThumb returns an image view showing a placeholder until a preview is set.
func newThumb(size float32) *canvas.Image {
	img := canvas.NewImageFromResource(theme.FileImageIcon())
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSquareSize(size))
	return img
}

// setThumb shows res, or the placeholder when res is nil.
func setThumb(img *canvas.Image, res fyne.Resource) {
	if res == nil {
		res = theme.FileImageIcon()
	}
	if img.Resource != res {
		img.Resource = res
		img.Refresh()
	}
}
