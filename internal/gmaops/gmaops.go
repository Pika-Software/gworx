// Package gmaops implements addon building and extraction on top of go-gma.
package gmaops

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/IGLOU-EU/go-wildcard/v2"
	"github.com/unknown-gd/go-gma"
)

// Project describes how to build a GMA file.
type Project struct {
	Title           string   `json:"title"`
	Description     string   `json:"description,omitempty"`
	Author          string   `json:"author,omitempty"`
	Version         int32    `json:"version,omitempty"`
	Category        string   `json:"type"`
	Tags            []string `json:"tags"`
	RequiredContent []string `json:"required_content,omitempty"`
	SourceDir       string   `json:"source_dir,omitempty"`
	Ignore          []string `json:"ignore"`
	Output          string   `json:"output,omitempty"`
}

// Validate checks the metadata the way gmad/go-gma would.
func (p *Project) Validate() error {
	if strings.TrimSpace(p.Title) == "" {
		return errors.New("title is required")
	}
	if p.Version < 1 {
		return errors.New("addon version must be 1 or higher")
	}
	if p.SourceDir == "" {
		return errors.New("addon folder is required")
	}
	if st, err := os.Stat(p.SourceDir); err != nil {
		return err
	} else if !st.IsDir() {
		return fmt.Errorf("%s is not a folder", p.SourceDir)
	}

	desc := gma.Description{}
	desc.Reset()
	desc.Title = p.Title
	desc.Category = p.Category
	desc.Tags = p.Tags
	_, err := desc.ToJSON()
	return err
}

// ScanResult lists what a build would include.
type ScanResult struct {
	Included   []Entry
	Ignored    []string // matched an ignore wildcard
	Disallowed []string // rejected by the Garry's Mod whitelist
	TotalSize  int64
}

type Entry struct {
	Disk     string // absolute path on disk
	Internal string // path inside the addon (lowercase, forward slashes)
	Size     int64
}

func matchIgnore(patterns []string, rel string, name string) bool {
	for _, p := range patterns {
		p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
		if p == "" {
			continue
		}

		p = strings.TrimSuffix(p, "/")
		if wildcard.Match(p, rel) || wildcard.Match(p, name) {
			return true
		}

		lp := strings.ToLower(p)
		if wildcard.Match(lp, strings.ToLower(rel)) || wildcard.Match(lp, strings.ToLower(name)) {
			return true
		}
	}
	return false
}

// Scan walks the source folder and classifies all files.
func Scan(p *Project) (*ScanResult, error) {
	res := &ScanResult{}
	root := filepath.Clean(p.SourceDir)

	err := filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if full == root {
			return nil
		}

		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if matchIgnore(p.Ignore, rel, d.Name()) {
			if d.IsDir() {
				res.Ignored = append(res.Ignored, rel+"/")
				return filepath.SkipDir
			}
			res.Ignored = append(res.Ignored, rel)
			return nil
		}

		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			res.Ignored = append(res.Ignored, rel)
			return nil
		}

		internal := strings.ToLower(rel)
		if !gma.IsAllowedPath(internal) {
			res.Disallowed = append(res.Disallowed, rel)
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		res.Included = append(res.Included, Entry{Disk: full, Internal: internal, Size: info.Size()})
		res.TotalSize += info.Size()
		return nil
	})

	return res, err
}

// Build creates the GMA file described by p and returns its scan result.
// Files rejected by the whitelist are skipped (and reported), like gmad's -warninvalid.
func Build(p *Project, output string, progress func(done, total int)) (*ScanResult, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	scan, err := Scan(p)
	if err != nil {
		return nil, err
	}
	if len(scan.Included) == 0 {
		return scan, errors.New("no allowed files found in the addon folder")
	}

	addon := gma.Addon{}
	addon.Reset()

	meta := &addon.Metadata
	meta.Title = p.Title
	meta.Author = p.Author
	if meta.Author == "" {
		meta.Author = "unknown"
	}
	meta.Version = p.Version
	meta.RequiredContent = append([]string{}, p.RequiredContent...)

	meta.Description.Title = p.Title
	meta.Description.Category = p.Category
	meta.Description.Tags = append([]string{}, p.Tags...)
	meta.Description.Ignore = append([]string{}, p.Ignore...)

	total := len(scan.Included)
	for i, e := range scan.Included {
		if err := addon.AddFile(e.Disk, e.Internal); err != nil {
			return scan, fmt.Errorf("%s: %w", e.Internal, err)
		}
		if progress != nil {
			progress(i+1, total)
		}
	}

	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return scan, err
	}

	tmp := output + ".part"
	if err := addon.Write(tmp); err != nil {
		os.Remove(tmp)
		return scan, err
	}

	return scan, os.Rename(tmp, output)
}

// Info is a summary of a GMA file.
type Info struct {
	Path            string
	Title           string
	Author          string
	Version         int32
	SteamID         uint64
	Timestamp       int64
	RequiredContent []string
	Legacy          bool
	Size            int64
	Files           []FileInfo
}

type FileInfo struct {
	Path string
	Size int64
}

// open opens a GMA file. Legacy (LZMA) addons are decompressed by go-gma into
// a "<name>_gworx.gma" sibling; cleanup removes that temporary copy.
func open(p string) (*gma.Addon, func(), error) {
	addon, err := gma.Open(p)
	if err != nil {
		if addon == nil {
			cleanupLegacy(p)
		}
		return nil, func() {}, err
	}

	cleanup := func() {}
	if addon.Legacy && addon.Location != p {
		loc := addon.Location
		cleanup = func() { os.Remove(loc) }
	}
	return addon, cleanup, nil
}

func cleanupLegacy(p string) {
	ext := filepath.Ext(p)
	os.Remove(p[:len(p)-len(ext)] + "_gworx.gma")
}

func infoFrom(p string, a *gma.Addon) *Info {
	inf := &Info{
		Path:            p,
		Title:           a.Metadata.Title,
		Author:          a.Metadata.Author,
		Version:         a.Metadata.Version,
		SteamID:         a.Metadata.SteamID,
		Timestamp:       a.Metadata.Timestamp,
		RequiredContent: a.Metadata.RequiredContent,
		Legacy:          a.Legacy,
		Size:            a.Size,
	}
	for _, f := range a.Files {
		inf.Files = append(inf.Files, FileInfo{Path: f.Path, Size: f.Size})
	}
	return inf
}

// Inspect reads the header and file table of a GMA file.
func Inspect(p string) (*Info, error) {
	addon, cleanup, err := open(p)
	defer cleanup()
	if err != nil {
		return nil, err
	}
	return infoFrom(p, addon), nil
}

// Normalize copies src to dst as a plain (non-LZMA) GMA file and returns its info.
func Normalize(src, dst string) (*Info, error) {
	addon, cleanup, err := open(src)
	if err != nil {
		cleanup()
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		cleanup()
		return nil, err
	}

	from := addon.Location
	if addon.Legacy && from != src {
		// already a decompressed temporary copy, move it into place
		if err := os.Rename(from, dst); err != nil {
			err = copyFile(from, dst)
			cleanup()
			if err != nil {
				return nil, err
			}
		}
	} else if err := copyFile(from, dst); err != nil {
		return nil, err
	}

	return Inspect(dst)
}

func copyFile(src, dst string) error {
	if filepath.Clean(src) == filepath.Clean(dst) {
		return nil
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}

	return os.Rename(tmp, dst)
}

// ExtractOptions control Extract.
type ExtractOptions struct {
	VerifyCRC bool
	Progress  func(done, total int)
	// Warn receives non-fatal problems (bad checksums, unsafe paths).
	Warn func(string)
}

// Extract unpacks every file of the GMA into dest and returns the number of files written.
func Extract(p, dest string, opt ExtractOptions) (int, error) {
	addon, cleanup, err := open(p)
	defer cleanup()
	if err != nil {
		return 0, err
	}

	warn := opt.Warn
	if warn == nil {
		warn = func(string) {}
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return 0, err
	}

	written := 0
	total := len(addon.Files)

	for i := range addon.Files {
		f := &addon.Files[i]

		rel := path.Clean(strings.ReplaceAll(f.Path, "\\", "/"))
		local := filepath.FromSlash(rel)
		if !filepath.IsLocal(local) {
			warn(fmt.Sprintf("skipped unsafe path %q", f.Path))
			continue
		}

		if opt.VerifyCRC {
			if ok, err := f.IsValid(); err != nil {
				return written, err
			} else if !ok {
				warn(fmt.Sprintf("checksum mismatch: %s", f.Path))
			}
		}

		data, err := f.ReadData()
		if err != nil {
			return written, fmt.Errorf("%s: %w", f.Path, err)
		}

		target := filepath.Join(dest, local)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return written, err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return written, err
		}

		written++
		if opt.Progress != nil {
			opt.Progress(i+1, total)
		}
	}

	return written, nil
}

// addonJSON mirrors gmad's addon.json.
type addonJSON struct {
	Title  string   `json:"title"`
	Type   string   `json:"type"`
	Tags   []string `json:"tags"`
	Ignore []string `json:"ignore"`
	// GWorX extras (ignored by gmad)
	Description     string   `json:"description,omitempty"`
	Author          string   `json:"author,omitempty"`
	Version         int32    `json:"version,omitempty"`
	RequiredContent []string `json:"required_content,omitempty"`
}

// LoadAddonJSON reads <dir>/addon.json into a project.
func LoadAddonJSON(dir string) (*Project, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "addon.json"))
	if err != nil {
		return nil, err
	}

	var a addonJSON
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}

	tags := make([]string, 0, len(a.Tags))
	for _, t := range a.Tags {
		tags = append(tags, strings.ToLower(t))
	}

	return &Project{
		Title:           a.Title,
		Description:     a.Description,
		Author:          a.Author,
		Version:         a.Version,
		Category:        strings.ToLower(a.Type),
		Tags:            tags,
		RequiredContent: a.RequiredContent,
		SourceDir:       dir,
		Ignore:          a.Ignore,
	}, nil
}

// SaveAddonJSON writes the project's metadata to <SourceDir>/addon.json.
func SaveAddonJSON(p *Project) error {
	a := addonJSON{
		Title:           p.Title,
		Type:            p.Category,
		Tags:            p.Tags,
		Ignore:          p.Ignore,
		Description:     p.Description,
		Author:          p.Author,
		Version:         p.Version,
		RequiredContent: p.RequiredContent,
	}

	raw, err := json.MarshalIndent(&a, "", "\t")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(p.SourceDir, "addon.json"), raw, 0o644)
}

// VerifyResult reports the CRC check of every file in a GMA.
type VerifyResult struct {
	// OK[i] is false when file i's data doesn't match its stored CRC32.
	// Files without a stored checksum (0) count as OK.
	OK  []bool
	Bad int
}

// Verify checks the CRC32 of every file in the GMA.
func Verify(p string, progress func(done, total int)) (*VerifyResult, error) {
	addon, cleanup, err := open(p)
	defer cleanup()
	if err != nil {
		return nil, err
	}

	res := &VerifyResult{OK: make([]bool, len(addon.Files))}
	for i := range addon.Files {
		ok, err := addon.Files[i].IsValid()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", addon.Files[i].Path, err)
		}
		res.OK[i] = ok
		if !ok {
			res.Bad++
		}
		if progress != nil {
			progress(i+1, len(addon.Files))
		}
	}
	return res, nil
}
