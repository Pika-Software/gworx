package ui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	previewSize       = 256                // cached previews are scaled to fit this square
	previewMaxAge     = 7 * 24 * time.Hour // refresh cached previews after this
	previewRetry      = 10 * time.Minute   // confirmed "no preview" backoff
	previewErrorRetry = time.Minute        // network or decode failure backoff
	previewFetch      = 4                  // parallel image downloads
	previewMemMax     = 400                // scaled stills kept in memory
	previewLocalMax   = 64
	previewDownload   = 20 * time.Second
	previewBatch      = 2 * time.Minute
	previewOwnerWait  = 8 * time.Second // Steam client wait when the public API hid the preview URL
	maxPreviewEdge    = 4096
	maxCachedPNG      = 1 << 20 // larger files are recompressed off the UI thread
)

// preview is a scaled still image. GIFs are flattened to their first frame:
// keeping every frame retained a full bitmap per frame for the life of the
// process and decoded it on the UI thread.
type preview struct {
	still fyne.Resource
}

type localJob struct {
	path string
	key  string
}

// previewCache loads Workshop preview images (memory + disk cache) and local
// image files. Lookups never block on decode or the network: a small cached
// PNG is returned immediately, everything else is queued, and subscribers are
// notified when it is ready.
type previewCache struct {
	dir string

	mu           sync.Mutex
	mem          map[uint64]*preview
	order        []uint64 // oldest workshop id first
	local        map[string]*preview
	localOrder   []string
	inflight     map[string]string // path -> newest local key being loaded
	localPending []localJob
	failed       map[uint64]time.Time // earliest time a missing id may be tried again
	queued       map[uint64]bool
	subs         []func()
	kick         chan struct{}

	// publicPreviews and ownerPreviews resolve Workshop preview URLs.
	// publicPreviews nil means the anonymous Steam Web API. ownerPreviews
	// is asked for items that API hides (private and unlisted); nil skips it.
	publicPreviews func(ctx context.Context, ids ...uint64) (map[uint64]string, error)
	ownerPreviews  func(ctx context.Context, ids []uint64) (map[uint64]string, error)
	ownerSkip      time.Time
}

func newPreviewCache() *previewCache {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	c := &previewCache{
		dir:      filepath.Join(base, "gworx", "previews"),
		mem:      map[uint64]*preview{},
		local:    map[string]*preview{},
		inflight: map[string]string{},
		failed:   map[uint64]time.Time{},
		queued:   map[uint64]bool{},
		kick:     make(chan struct{}, 1),
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
	if p := c.mem[id]; p != nil {
		c.touchLocked(id)
		c.mu.Unlock()
		return p
	}
	c.mu.Unlock()

	// Already-scaled PNG only. Decoding (and legacy GIFs) happens in the background.
	if p, stale := c.cachedPNG(id); p != nil {
		c.mu.Lock()
		if cur := c.mem[id]; cur != nil {
			c.touchLocked(id)
			c.mu.Unlock()
			return cur
		}
		c.putMemLocked(id, p)
		c.mu.Unlock()
		if stale {
			c.queue(id)
		}
		return p
	}
	c.queue(id)
	return nil
}

// file returns a thumbnail of a local image.
// pending is true when path exists and its thumbnail is not ready yet;
// callers should not fall through to another image in that case.
func (c *previewCache) file(path string) (p *preview, pending bool) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return nil, false
	}
	key := fmt.Sprintf("%s|%d|%d", path, st.Size(), st.ModTime().UnixNano())

	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.local[key]; ok {
		return p, false
	}
	if c.inflight[path] == key {
		return nil, true
	}
	c.inflight[path] = key
	c.localPending = append(c.localPending, localJob{path: path, key: key})
	c.kickLocked()
	return nil, true
}

func (c *previewCache) queue(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.queued[id] {
		return
	}
	if until, ok := c.failed[id]; ok && time.Now().Before(until) {
		return
	}
	c.queued[id] = true
	c.kickLocked()
}

func (c *previewCache) kickLocked() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

func (c *previewCache) loop() {
	for range c.kick {
		time.Sleep(150 * time.Millisecond) // gather requests from a whole list
		c.mu.Lock()
		ids := make([]uint64, 0, len(c.queued))
		for id := range c.queued {
			ids = append(ids, id)
		}
		jobs := c.localPending
		c.localPending = nil
		c.mu.Unlock()
		if len(jobs) > 0 {
			c.loadLocal(jobs)
		}
		if len(ids) > 0 {
			c.resolve(ids)
		}
	}
}

func (c *previewCache) loadLocal(jobs []localJob) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, previewFetch)
	for _, job := range jobs {
		wg.Add(1)
		go func(job localJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var p *preview
			if data, err := os.ReadFile(job.path); err == nil {
				if thumb, err := thumbnail(data); err == nil {
					p = &preview{still: fyne.NewStaticResource("local-"+filepath.Base(job.path)+".png", thumb)}
				}
			}
			c.mu.Lock()
			c.rememberLocalLocked(job.path, job.key, p)
			c.mu.Unlock()
		}(job)
	}
	wg.Wait()
	c.notify()
}

func (c *previewCache) resolve(ids []uint64) {
	var need []uint64
	showed := false
	for _, id := range ids {
		c.mu.Lock()
		have := c.mem[id] != nil
		c.mu.Unlock()

		p, stale, ok := c.diskPreview(id)
		if ok && !have {
			c.mu.Lock()
			if c.mem[id] == nil {
				c.putMemLocked(id, p)
			}
			c.mu.Unlock()
			showed = true
		}
		if ok && !stale {
			c.mu.Lock()
			delete(c.queued, id)
			delete(c.failed, id)
			c.mu.Unlock()
			continue
		}
		need = append(need, id)
	}
	if showed {
		c.notify()
	}
	if len(need) > 0 {
		c.fetch(need)
	}
}

// lookupURLs returns a preview URL for each id the caller can see.
// The bool is true when a lookup failed and a miss should be retried soon,
// rather than treated as an item that has no preview.
func (c *previewCache) lookupURLs(ctx context.Context, ids []uint64) (map[uint64]string, bool) {
	lookup := c.publicPreviews
	if lookup == nil {
		lookup = workshop.GetPreviewURLs
	}
	urls, err := lookup(ctx, ids...)
	if urls == nil {
		urls = map[uint64]string{}
	}
	failed := err != nil

	var hidden []uint64
	for _, id := range ids {
		if urls[id] == "" {
			hidden = append(hidden, id)
		}
	}
	if len(hidden) == 0 || c.ownerPreviews == nil {
		return urls, failed
	}

	c.mu.Lock()
	skip := time.Now().Before(c.ownerSkip)
	c.mu.Unlock()
	if skip {
		return urls, true
	}

	ownerCtx, cancel := context.WithTimeout(ctx, previewOwnerWait)
	more, err := c.ownerPreviews(ownerCtx, hidden)
	cancel()
	if err != nil {
		c.mu.Lock()
		c.ownerSkip = time.Now().Add(previewErrorRetry)
		c.mu.Unlock()
		return urls, true
	}
	for id, u := range more {
		if u != "" {
			urls[id] = u
		}
	}
	return urls, failed
}

func (c *previewCache) fetch(ids []uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), previewBatch)
	defer cancel()

	urls, lookupFailed := c.lookupURLs(ctx, ids)
	_ = os.MkdirAll(c.dir, 0o755)

	var wg sync.WaitGroup
	sem := make(chan struct{}, previewFetch)
	var changedMu sync.Mutex
	changed := false
	note := func(ok bool) {
		if !ok {
			return
		}
		changedMu.Lock()
		changed = true
		changedMu.Unlock()
	}
	for _, id := range ids {
		u := urls[id]
		if u == "" {
			wait := previewRetry
			if lookupFailed {
				wait = previewErrorRetry
			}
			note(c.finish(id, nil, wait))
			continue
		}
		wg.Add(1)
		go func(id uint64, u string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			dlCtx, dlCancel := context.WithTimeout(ctx, previewDownload)
			data, derr := workshop.FetchBytes(dlCtx, u, 16<<20)
			dlCancel()

			var p *preview
			wait := previewErrorRetry
			if derr == nil {
				p = c.storeFetched(id, data)
				if p == nil {
					wait = previewRetry
				}
			}
			note(c.finish(id, p, wait))
		}(id, u)
	}
	wg.Wait()
	if changed {
		c.notify()
	}
}

// finish stores a decoded preview, or records when a miss may be tried again.
// It reports whether a new image was stored.
func (c *previewCache) finish(id uint64, p *preview, retry time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.queued, id)
	if p != nil {
		c.putMemLocked(id, p)
		delete(c.failed, id)
		return true
	}
	if retry > 0 && c.mem[id] == nil {
		c.failed[id] = time.Now().Add(retry)
	}
	return false
}

func (c *previewCache) notify() {
	c.mu.Lock()
	subs := append([]func(){}, c.subs...)
	c.mu.Unlock()
	if len(subs) == 0 {
		return
	}
	fyne.Do(func() {
		for _, fn := range subs {
			fn()
		}
	})
}

// cachedPNG reads a preview that is already a small scaled PNG.
func (c *previewCache) cachedPNG(id uint64) (*preview, bool) {
	st, err := os.Stat(c.pngPath(id))
	if err != nil || st.Size() == 0 || st.Size() > maxCachedPNG {
		return nil, false
	}
	data, err := os.ReadFile(c.pngPath(id))
	if err != nil || len(data) == 0 {
		return nil, false
	}
	os.Remove(c.gifPath(id))
	return newStill(id, data, st.ModTime()), time.Since(st.ModTime()) > previewMaxAge
}

// diskPreview returns a cached still, converting a legacy GIF or an oversized
// PNG to a scaled PNG first. ok is false when nothing usable is on disk.
func (c *previewCache) diskPreview(id uint64) (p *preview, stale bool, ok bool) {
	if p, stale = c.cachedPNG(id); p != nil {
		return p, stale, true
	}

	path := ""
	var mod time.Time
	if st, err := os.Stat(c.gifPath(id)); err == nil {
		path = c.gifPath(id)
		mod = st.ModTime()
	} else if st, err := os.Stat(c.pngPath(id)); err == nil {
		path = c.pngPath(id)
		mod = st.ModTime()
	} else {
		return nil, true, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, true, false
	}
	thumb, err := thumbnail(data)
	if err != nil {
		os.Remove(path)
		return nil, true, false
	}
	_ = os.MkdirAll(c.dir, 0o755)
	if err := os.WriteFile(c.pngPath(id), thumb, 0o644); err != nil {
		return nil, true, false
	}
	if path != c.pngPath(id) {
		os.Remove(path)
	}
	if !mod.IsZero() {
		_ = os.Chtimes(c.pngPath(id), mod, mod)
	}
	return newStill(id, thumb, mod), time.Since(mod) > previewMaxAge, true
}

// storeFetched writes a downloaded preview as a scaled PNG and returns it.
func (c *previewCache) storeFetched(id uint64, data []byte) *preview {
	thumb, err := thumbnail(data)
	if err != nil {
		return nil
	}
	if err := os.WriteFile(c.pngPath(id), thumb, 0o644); err != nil {
		return nil
	}
	os.Remove(c.gifPath(id))
	return newStill(id, thumb, time.Now())
}

func newStill(id uint64, data []byte, mod time.Time) *preview {
	stamp := mod.UnixNano()
	if stamp == 0 {
		stamp = time.Now().UnixNano()
	}
	return &preview{still: fyne.NewStaticResource(fmt.Sprintf("preview-%d-%d.png", id, stamp), data)}
}

// thumbnail decodes the first frame of an image and scales it to previewSize.
func thumbnail(data []byte) ([]byte, error) {
	src, err := decodePreview(data)
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
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

func (c *previewCache) putMemLocked(id uint64, p *preview) {
	_, exists := c.mem[id]
	c.mem[id] = p
	c.touchLocked(id)
	if exists {
		return
	}
	guard := len(c.order) + 1
	for len(c.mem) > previewMemMax && len(c.order) > 0 && guard > 0 {
		guard--
		old := c.order[0]
		c.order = c.order[1:]
		if old == id {
			c.order = append(c.order, id)
			continue
		}
		delete(c.mem, old)
	}
}

func (c *previewCache) touchLocked(id uint64) {
	for i, v := range c.order {
		if v == id {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	c.order = append(c.order, id)
}

func (c *previewCache) rememberLocalLocked(path, key string, p *preview) {
	if newest, ok := c.inflight[path]; ok && newest != key && localNewer(newest, key) {
		return
	}
	if c.inflight[path] == key {
		delete(c.inflight, path)
	}
	prefix := path + "|"
	for k := range c.local {
		if strings.HasPrefix(k, prefix) {
			delete(c.local, k)
			c.dropLocalOrder(k)
		}
	}
	c.local[key] = p
	c.localOrder = append(c.localOrder, key)
	guard := len(c.localOrder) + 1
	for len(c.local) > previewLocalMax && len(c.localOrder) > 0 && guard > 0 {
		guard--
		old := c.localOrder[0]
		c.localOrder = c.localOrder[1:]
		if old == key {
			c.localOrder = append(c.localOrder, key)
			continue
		}
		delete(c.local, old)
	}
}

func (c *previewCache) dropLocalOrder(key string) {
	for i, k := range c.localOrder {
		if k == key {
			c.localOrder = append(c.localOrder[:i], c.localOrder[i+1:]...)
			return
		}
	}
}

func localNewer(a, b string) bool {
	return localStamp(a) > localStamp(b)
}

func localStamp(key string) int64 {
	i := strings.LastIndexByte(key, '|')
	if i < 0 {
		return 0
	}
	n, _ := strconv.ParseInt(key[i+1:], 10, 64)
	return n
}

// thumb is a square still preview.
type thumb struct {
	widget.BaseWidget

	size float32
	img  *canvas.Image
}

// newThumb returns an image view showing a placeholder until a preview is set.
func newThumb(size float32) *thumb {
	t := &thumb{
		size: size,
		img:  canvas.NewImageFromResource(theme.FileImageIcon()),
	}
	t.img.FillMode = canvas.ImageFillContain
	t.img.ScaleMode = canvas.ImageScaleFastest
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

// setThumb shows p, or the placeholder when p is nil.
func setThumb(t *thumb, p *preview) {
	res := fyne.Resource(theme.FileImageIcon())
	if p != nil && p.still != nil {
		res = p.still
	}
	if t.img.Resource == res && t.img.Image == nil {
		return
	}
	t.img.Resource = res
	t.img.Image = nil
	t.img.Refresh()
}
