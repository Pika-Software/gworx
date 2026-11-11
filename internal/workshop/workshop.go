// Package workshop implements Steam Workshop access for Garry's Mod
// through either steamcmd or the Steamworks API.
package workshop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// AppID is Garry's Mod.
const AppID = 4000

var ErrNotSupported = errors.New("not supported by this backend")

// Visibility values match ERemoteStoragePublishedFileVisibility.
const (
	VisibilityPublic      = 0
	VisibilityFriendsOnly = 1
	VisibilityPrivate     = 2
	VisibilityUnlisted    = 3
)

var VisibilityNames = []string{"Public", "Friends only", "Private", "Unlisted"}

// Logger receives human readable progress lines.
type Logger func(string)

// Progress receives a value in [0,1]; negative means indeterminate.
type Progress func(float64)

// Prompter asks the user for Steam credentials. Calls block until the user answers.
type Prompter interface {
	Password(user string) (string, bool)
	GuardCode(user string, hint string) (string, bool)
}

// Item is a published Workshop item.
type Item struct {
	ID          uint64
	Title       string
	Description string
	Creator     string
	PreviewURL  string
	FileURL     string
	FileSize    int64
	Created     int64
	Updated     int64
	Visibility  int
	Tags        []string
}

// UploadRequest describes a Workshop upload. ID == 0 creates a new item.
type UploadRequest struct {
	ID          uint64
	GMAPath     string
	Title       string
	Description string
	PreviewPath string
	ChangeNote  string
	Visibility  int
	Tags        []string
}

type Backend interface {
	Name() string
	// Download fetches the item and returns the path to its .gma (or legacy .bin) file.
	Download(ctx context.Context, id uint64, log Logger, progress Progress) (string, error)
	// Upload creates or updates an item and returns its ID.
	Upload(ctx context.Context, req UploadRequest, log Logger, progress Progress) (uint64, error)
	Delete(ctx context.Context, id uint64, log Logger) error
	// ListPublished returns the items published by the current user.
	ListPublished(ctx context.Context, log Logger) ([]Item, error)
	// CurrentUser returns a display name suitable for the GMA author field.
	CurrentUser() string
}

var idPattern = regexp.MustCompile(`^\d{1,20}$`)

// ParseID accepts a numeric ID or any Workshop / Steam URL carrying ?id=.
func ParseID(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty workshop id")
	}

	if idPattern.MatchString(s) {
		return strconv.ParseUint(s, 10, 64)
	}

	if u, err := url.Parse(s); err == nil {
		if id := u.Query().Get("id"); idPattern.MatchString(id) {
			return strconv.ParseUint(id, 10, 64)
		}

		// steam://url/CommunityFilePage/<id>
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if last := parts[len(parts)-1]; idPattern.MatchString(last) {
			return strconv.ParseUint(last, 10, 64)
		}
	}

	if m := regexp.MustCompile(`id=(\d+)`).FindStringSubmatch(s); m != nil {
		return strconv.ParseUint(m[1], 10, 64)
	}

	return 0, fmt.Errorf("can't find a workshop id in %q", s)
}

// ItemURL returns the Workshop page of an item.
func ItemURL(id uint64) string {
	return fmt.Sprintf("https://steamcommunity.com/sharedfiles/filedetails/?id=%d", id)
}

// GModTags builds the Workshop tag list Garry's Mod uses: "Addon", the type and the addon tags.
func GModTags(category string, tags []string) []string {
	out := []string{"Addon"}
	if category != "" {
		out = append(out, titleCase(category))
	}
	for _, t := range tags {
		out = append(out, titleCase(t))
	}
	return out
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	switch s {
	case "servercontent":
		return "ServerContent"
	case "npc":
		return "NPC"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
