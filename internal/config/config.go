// Package config holds persistent GWorX settings.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"sync"

	"github.com/Pika-Software/gworx/internal/fsutil"
)

// Backend selects how GWorX talks to the Steam Workshop.
type Backend string

const (
	BackendSteamCMD   Backend = "steamcmd"
	BackendSteamworks Backend = "steamworks"
)

// DefaultIgnore is the default list of wildcards excluded from GMA builds.
var DefaultIgnore = []string{
	".git*",
	".svn",
	".vscode",
	".idea",
	"*.psd",
	"*.blend",
	"*.blend1",
	"*.bak",
	"*.tmp",
	"*thumbs.db",
	"*desktop.ini",
	"*.DS_Store",
	"addon.json",
}

// Config is the GWorX configuration. Its fields live in the embedded Settings
// so Reload can replace them in one assignment.
type Config struct {
	Settings

	mu   sync.Mutex
	path string
}

// Settings are the values stored in config.json.
type Settings struct {
	Backend Backend `json:"backend"`

	// steamcmd
	SteamCMDPath       string `json:"steamcmd_path"`
	SteamUsername      string `json:"steam_username"`
	AnonymousDownloads bool   `json:"anonymous_downloads"`
	SteamCMDInstallDir string `json:"steamcmd_install_dir"`

	// steamworks
	SteamAPIPath string `json:"steam_api_path"`

	// Steam Web API (optional, used to list published items without Steamworks)
	WebAPIKey string `json:"web_api_key"`
	SteamID64 string `json:"steam_id64"`

	// dedicated servers
	SteamAccountToken string `json:"steam_account_token"`
	ServerWrapper     string `json:"server_wrapper"`

	// folders
	DataDir      string `json:"data_dir"`
	DownloadsDir string `json:"downloads_dir"`
	UploadsDir   string `json:"uploads_dir"`

	AutoExtract   bool     `json:"auto_extract"`
	DefaultAuthor string   `json:"default_author"`
	DefaultIgnore []string `json:"default_ignore"`
}

// Dir returns the directory holding GWorX config and database files.
func Dir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, "gworx")
}

func defaults() *Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	author := "unknown"
	if u, err := user.Current(); err == nil && u.Username != "" {
		author = u.Username
	}

	return &Config{Settings: Settings{
		Backend:       BackendSteamworks,
		ServerWrapper: defaultServerWrapper(),
		DataDir:       filepath.Join(home, "gworx"),
		AutoExtract:   true,
		DefaultAuthor: author,
		DefaultIgnore: slices.Clone(DefaultIgnore),
	}}
}

// Load reads the config file, creating defaults if it does not exist.
func Load() (*Config, error) {
	cfg := defaults()
	cfg.path = filepath.Join(Dir(), "config.json")

	data, err := os.ReadFile(cfg.path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, cfg.Save()
	} else if err != nil {
		return cfg, err
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		fresh := defaults()
		fresh.path = cfg.path
		return fresh, fsutil.SetAsideDamaged(cfg.path, err)
	}
	cfg.normalize()
	return cfg, nil
}

// normalize replaces invalid or missing values with defaults.
func (c *Config) normalize() {
	if c.Backend != BackendSteamCMD && c.Backend != BackendSteamworks {
		c.Backend = BackendSteamworks
	}
	if c.DataDir == "" {
		c.DataDir = defaults().DataDir
	}
}

func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "\t")
	if err != nil {
		return err
	}

	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}

	return os.Rename(tmp, c.path)
}

// Reload replaces settings with the on-disk config, keeping the same Config pointer.
func (c *Config) Reload() error {
	c.mu.Lock()
	path := c.path
	c.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fresh := defaults()
	if err := json.Unmarshal(data, fresh); err != nil {
		return err
	}
	fresh.normalize()

	c.mu.Lock()
	defer c.mu.Unlock()
	c.Settings = fresh.Settings
	c.DefaultIgnore = slices.Clone(fresh.DefaultIgnore)
	return nil
}

// defaultServerWrapper runs srcds through steam-run on NixOS, where the
// server's prebuilt binaries can't find their libraries otherwise.
func defaultServerWrapper() string {
	if _, err := os.Stat("/etc/NIXOS"); err != nil {
		return ""
	}
	if _, err := exec.LookPath("steam-run"); err != nil {
		return ""
	}
	return "steam-run"
}

// Servers returns the default parent folder for dedicated server instances.
func (c *Config) Servers() string {
	return filepath.Join(c.DataDir, "servers")
}

// Downloads returns the folder raw GMA files from the Workshop are stored in.
func (c *Config) Downloads() string {
	if c.DownloadsDir != "" {
		return c.DownloadsDir
	}
	return filepath.Join(c.DataDir, "downloads")
}

// Uploads returns the folder downloaded addons get extracted into.
func (c *Config) Uploads() string {
	if c.UploadsDir != "" {
		return c.UploadsDir
	}
	return filepath.Join(c.DataDir, "uploads")
}

// Builds returns the default folder for built GMA files.
func (c *Config) Builds() string {
	return filepath.Join(c.DataDir, "builds")
}

// SteamCMDDir is the force_install_dir used for steamcmd workshop downloads.
func (c *Config) SteamCMDDir() string {
	if c.SteamCMDInstallDir != "" {
		return c.SteamCMDInstallDir
	}
	return filepath.Join(c.DataDir, "steamcmd")
}
