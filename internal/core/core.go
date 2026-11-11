// Package core implements GWorX workflows shared by the GUI and the CLI.
package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Pika-Software/gworx/internal/config"
	"github.com/Pika-Software/gworx/internal/gmaops"
	"github.com/Pika-Software/gworx/internal/server"
	"github.com/Pika-Software/gworx/internal/store"
	"github.com/Pika-Software/gworx/internal/workshop"
)

// Reporter receives progress of long running operations. Nil fields are ignored.
type Reporter struct {
	Log      workshop.Logger
	Progress workshop.Progress
}

func (r Reporter) log(s string) {
	if r.Log != nil {
		r.Log(s)
	}
}

func (r Reporter) progress(f float64) {
	if r.Progress != nil {
		r.Progress(f)
	}
}

func (r Reporter) logger() workshop.Logger       { return r.log }
func (r Reporter) progresser() workshop.Progress { return r.progress }

type Service struct {
	Cfg     *config.Config
	Store   *store.Store
	SW      *workshop.Steamworks
	Prompt  workshop.Prompter
	Servers *server.Manager
	// ServersErr is set when servers.json could not be read.
	ServersErr error
}

func New(cfg *config.Config, st *store.Store, prompt workshop.Prompter) *Service {
	s := &Service{
		Cfg:    cfg,
		Store:  st,
		SW:     &workshop.Steamworks{LibPath: cfg.SteamAPIPath, StagingDir: filepath.Join(cfg.DataDir, "staging")},
		Prompt: prompt,
	}
	s.Servers, s.ServersErr = server.Open(config.Dir(), func() server.Settings {
		return server.Settings{SteamCMD: s.SteamCMD(), Token: s.Cfg.SteamAccountToken, Wrapper: s.Cfg.ServerWrapper}
	})
	return s
}

// Close releases the Steam API if it was used.
func (s *Service) Close() { s.SW.Close() }

// Backend returns the Workshop backend selected in the config.
func (s *Service) Backend() workshop.Backend {
	if s.Cfg.Backend == config.BackendSteamworks {
		if !s.SW.Initialized() {
			s.SW.LibPath = s.Cfg.SteamAPIPath
		}
		return s.SW
	}
	return s.SteamCMD()
}

func (s *Service) SteamCMD() *workshop.SteamCMD {
	return &workshop.SteamCMD{
		Path:       s.Cfg.SteamCMDPath,
		User:       s.Cfg.SteamUsername,
		InstallDir: s.Cfg.SteamCMDDir(),
		Wrapper:    s.Cfg.ServerWrapper,
		Anonymous:  s.Cfg.AnonymousDownloads,
		Prompt:     s.Prompt,
	}
}

func (s *Service) DefaultAuthor() string {
	if s.Cfg.Backend == config.BackendSteamworks {
		if p := s.SW.CurrentUser(); p != "" {
			return p
		}
	}
	if s.Cfg.DefaultAuthor != "" {
		return s.Cfg.DefaultAuthor
	}
	if s.Cfg.SteamUsername != "" {
		return s.Cfg.SteamUsername
	}
	return "unknown"
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slug turns a title into a file name friendly string.
func Slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "_"), "_")
	if len(s) > 48 {
		s = strings.TrimRight(s[:48], "_")
	}
	if s == "" {
		s = "addon"
	}
	return s
}

// BuildOutput is the default output path for a project.
func (s *Service) BuildOutput(p *gmaops.Project) string {
	if p.Output != "" {
		return p.Output
	}
	return filepath.Join(s.Cfg.Builds(), Slug(p.Title)+".gma")
}

// ExtractDest is the default extraction folder for a downloaded item.
func (s *Service) ExtractDest(id uint64, title string) string {
	return filepath.Join(s.Cfg.Uploads(), fmt.Sprintf("%d_%s", id, Slug(title)))
}

// Build packs the project into out.
func (s *Service) Build(p *gmaops.Project, out string, r Reporter) (*gmaops.ScanResult, error) {
	r.log(fmt.Sprintf("Building %q from %s", p.Title, p.SourceDir))
	res, err := gmaops.Build(p, out, func(done, total int) {
		r.progress(float64(done) / float64(total))
	})
	if res != nil {
		for _, d := range res.Disallowed {
			r.log("skipped (not allowed in GMA): " + d)
		}
		r.log(fmt.Sprintf("%d files packed, %d ignored, %d not allowed", len(res.Included), len(res.Ignored), len(res.Disallowed)))
	}
	if err != nil {
		return res, err
	}
	if st, err := os.Stat(out); err == nil {
		r.log(fmt.Sprintf("Wrote %s (%s)", out, HumanSize(st.Size())))
	}
	return res, nil
}

// Extract unpacks a GMA, logging warnings.
func (s *Service) Extract(gmaPath, dest string, verify bool, r Reporter) (int, error) {
	r.log(fmt.Sprintf("Extracting %s to %s", gmaPath, dest))
	return gmaops.Extract(gmaPath, dest, gmaops.ExtractOptions{
		VerifyCRC: verify,
		Progress:  func(done, total int) { r.progress(float64(done) / float64(total)) },
		Warn:      r.log,
	})
}

// DownloadOptions control Download.
type DownloadOptions struct {
	Extract bool
	// ExtractDir overrides the destination folder (default: uploads/<id>_<title>).
	ExtractDir string
}

// Download fetches a Workshop item, stores a plain GMA in the downloads folder,
// optionally extracts it and records it in the history.
func (s *Service) Download(ctx context.Context, id uint64, opt DownloadOptions, r Reporter) (store.Download, error) {
	be := s.Backend()

	title := ""
	if items, err := workshop.GetDetails(ctx, id); err == nil {
		title = items[0].Title
		r.log("Item: " + title)
	} else {
		r.log("Web API: " + err.Error())
	}

	r.log("Downloading with " + be.Name() + "...")
	src, err := be.Download(ctx, id, r.logger(), r.progresser())
	if err != nil {
		return store.Download{}, err
	}
	r.log("Got " + src)

	r.progress(-1)
	gmaPath := filepath.Join(s.Cfg.Downloads(), strconv.FormatUint(id, 10)+".gma")
	inf, err := gmaops.Normalize(src, gmaPath)
	if err != nil {
		return store.Download{}, fmt.Errorf("reading downloaded addon: %w", err)
	}
	if inf.Legacy {
		r.log("Decompressed legacy (LZMA) addon")
	}
	if title == "" {
		title = inf.Title
	}
	r.log("Saved " + gmaPath)

	rec := store.Download{
		ID:           id,
		Title:        title,
		Author:       inf.Author,
		GMAPath:      gmaPath,
		DownloadedAt: time.Now(),
	}
	if st, err := os.Stat(gmaPath); err == nil {
		rec.Size = st.Size()
	}

	if opt.Extract {
		dest := opt.ExtractDir
		if dest == "" {
			dest = s.ExtractDest(id, title)
		}
		n, err := s.Extract(gmaPath, dest, true, r)
		if err != nil {
			return rec, err
		}
		r.log(fmt.Sprintf("%d files extracted", n))
		rec.ExtractedPath = dest
	}

	return rec, s.Store.AddDownload(rec)
}

// UploadOptions describe an upload. When Build is set, Project is built into
// Request.GMAPath first.
type UploadOptions struct {
	Request workshop.UploadRequest
	Project *gmaops.Project
	Build   bool
}

// Upload (optionally builds and) publishes an addon and records it.
func (s *Service) Upload(ctx context.Context, opt UploadOptions, r Reporter) (uint64, error) {
	req := opt.Request
	p := opt.Project

	if req.ID == 0 && req.PreviewPath == "" {
		return 0, errors.New("new Workshop items need a preview image")
	}

	if opt.Build {
		if p == nil {
			return 0, errors.New("nothing to build")
		}
		if req.GMAPath == "" {
			req.GMAPath = s.BuildOutput(p)
		}
		if _, err := s.Build(p, req.GMAPath, r); err != nil {
			return 0, err
		}
	}
	if req.GMAPath == "" {
		return 0, errors.New("no GMA file to upload")
	}

	if p != nil {
		if req.Title == "" {
			req.Title = p.Title
		}
		if req.Description == "" {
			req.Description = p.Description
		}
		if len(req.Tags) == 0 {
			req.Tags = workshop.GModTags(p.Category, p.Tags)
		}
	}
	if req.Title == "" && req.ID == 0 {
		if inf, err := gmaops.Inspect(req.GMAPath); err == nil {
			req.Title = inf.Title
		}
	}
	be := s.Backend()
	r.log("Uploading " + req.GMAPath + " with " + be.Name() + "...")
	id, err := be.Upload(ctx, req, r.logger(), r.progresser())
	if err != nil {
		return 0, err
	}
	r.log(fmt.Sprintf("Published item %d", id))

	return id, s.Store.UpsertPublished(store.Published{
		ID:          id,
		Title:       req.Title,
		Description: req.Description,
		GMAPath:     req.GMAPath,
		PreviewPath: req.PreviewPath,
		Visibility:  req.Visibility,
		Project:     p,
		UpdatedAt:   time.Now(),
	})
}

// Delete removes a Workshop item and forgets it.
func (s *Service) Delete(ctx context.Context, id uint64, r Reporter) error {
	if err := s.Backend().Delete(ctx, id, r.logger()); err != nil {
		return err
	}
	return s.Store.RemovePublished(id)
}

// RefreshPublished imports the user's published items into the store.
func (s *Service) RefreshPublished(ctx context.Context, r Reporter) ([]workshop.Item, error) {
	r.progress(-1)
	items, err := s.Backend().ListPublished(ctx, r.logger())
	if errors.Is(err, workshop.ErrNotSupported) {
		if s.Cfg.WebAPIKey == "" || s.Cfg.SteamID64 == "" {
			return nil, errors.New("steamcmd can't list your items: switch to Steamworks, set a Steam Web API key and SteamID64, or add items by ID")
		}
		r.log("Listing items through the Steam Web API...")
		items, err = workshop.GetUserFiles(ctx, s.Cfg.WebAPIKey, s.Cfg.SteamID64)
	}
	if err != nil {
		return nil, err
	}

	for _, it := range items {
		rec := store.Published{ID: it.ID, Title: it.Title, Description: it.Description, Visibility: it.Visibility}
		if old, ok := s.Store.GetPublished(it.ID); ok {
			rec.Visibility = old.Visibility
		}
		if it.Updated > 0 {
			rec.UpdatedAt = time.Unix(it.Updated, 0)
		}
		if err := s.Store.UpsertPublished(rec); err != nil {
			return items, err
		}
	}
	r.log(fmt.Sprintf("%d items", len(items)))
	return items, nil
}

// AddPublished records an existing item by ID, filling details from the Web API when possible.
func (s *Service) AddPublished(ctx context.Context, id uint64) (store.Published, error) {
	rec := store.Published{ID: id, Title: fmt.Sprintf("Item %d", id), Visibility: workshop.VisibilityPublic}
	if items, err := workshop.GetDetails(ctx, id); err == nil {
		it := items[0]
		rec.Title, rec.Description, rec.Visibility = it.Title, it.Description, it.Visibility
		if it.Updated > 0 {
			rec.UpdatedAt = time.Unix(it.Updated, 0)
		}
	}
	return rec, s.Store.UpsertPublished(rec)
}

func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
