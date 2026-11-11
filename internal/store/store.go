// Package store persists the download history and the list of published addons.
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/Pika-Software/gworx/internal/gmaops"
)

// Download is a Workshop addon downloaded through GWorX.
type Download struct {
	ID            uint64    `json:"id"`
	Title         string    `json:"title"`
	Author        string    `json:"author,omitempty"`
	GMAPath       string    `json:"gma_path"`
	ExtractedPath string    `json:"extracted_path,omitempty"`
	Size          int64     `json:"size"`
	DownloadedAt  time.Time `json:"downloaded_at"`
}

// Published is a Workshop addon created by the user.
type Published struct {
	ID          uint64          `json:"id"`
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	GMAPath     string          `json:"gma_path,omitempty"`
	PreviewPath string          `json:"preview_path,omitempty"`
	Visibility  int             `json:"visibility"`
	Project     *gmaops.Project `json:"project,omitempty"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type data struct {
	Downloads []Download  `json:"downloads"`
	Published []Published `json:"published"`
}

type Store struct {
	mu   sync.Mutex
	path string
	d    data

	// OnChange is called (without the lock held) after every modification.
	OnChange func()
}

func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, "library.json")}

	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	} else if err != nil {
		return s, err
	}

	return s, json.Unmarshal(raw, &s.d)
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	raw, err := json.MarshalIndent(&s.d, "", "\t")
	if err != nil {
		return err
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}

	return os.Rename(tmp, s.path)
}

func (s *Store) modify(fn func(d *data)) error {
	s.mu.Lock()
	fn(&s.d)
	err := s.saveLocked()
	s.mu.Unlock()

	if s.OnChange != nil {
		s.OnChange()
	}

	return err
}

// Downloads returns the history, newest first.
func (s *Store) Downloads() []Download {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := slices.Clone(s.d.Downloads)
	sort.SliceStable(out, func(i, j int) bool { return out[i].DownloadedAt.After(out[j].DownloadedAt) })
	return out
}

// AddDownload inserts or replaces the history entry for the given item.
func (s *Store) AddDownload(rec Download) error {
	return s.modify(func(d *data) {
		d.Downloads = slices.DeleteFunc(d.Downloads, func(x Download) bool { return x.ID == rec.ID })
		d.Downloads = append(d.Downloads, rec)
	})
}

func (s *Store) RemoveDownload(id uint64) error {
	return s.modify(func(d *data) {
		d.Downloads = slices.DeleteFunc(d.Downloads, func(x Download) bool { return x.ID == id })
	})
}

func (s *Store) ClearDownloads() error {
	return s.modify(func(d *data) { d.Downloads = nil })
}

// Published returns the list of created addons, most recently updated first.
func (s *Store) Published() []Published {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := slices.Clone(s.d.Published)
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func (s *Store) GetPublished(id uint64) (Published, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range s.d.Published {
		if p.ID == id {
			return p, true
		}
	}
	return Published{}, false
}

// UpsertPublished stores rec; empty fields in rec keep their previous values.
func (s *Store) UpsertPublished(rec Published) error {
	return s.modify(func(d *data) {
		for i := range d.Published {
			old := &d.Published[i]
			if old.ID != rec.ID {
				continue
			}

			if rec.Title == "" {
				rec.Title = old.Title
			}
			if rec.Description == "" {
				rec.Description = old.Description
			}
			if rec.GMAPath == "" {
				rec.GMAPath = old.GMAPath
			}
			if rec.PreviewPath == "" {
				rec.PreviewPath = old.PreviewPath
			}
			if rec.Project == nil {
				rec.Project = old.Project
			}
			if rec.UpdatedAt.IsZero() {
				rec.UpdatedAt = old.UpdatedAt
			}

			*old = rec
			return
		}

		d.Published = append(d.Published, rec)
	})
}

func (s *Store) RemovePublished(id uint64) error {
	return s.modify(func(d *data) {
		d.Published = slices.DeleteFunc(d.Published, func(x Published) bool { return x.ID == id })
	})
}
