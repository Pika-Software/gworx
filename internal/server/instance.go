// Package server manages Garry's Mod dedicated server instances installed with steamcmd.
package server

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode"

	"github.com/Pika-Software/gworx/internal/workshop"
)

// AppID is the Garry's Mod Dedicated Server.
const AppID = 4020

// Branches are the known Steam branches of app 4020; any other name can be typed in.
var Branches = []string{"public", "x86-64", "prerelease", "dev"}

// Gamemodes and Maps are suggestions for the instance form.
var (
	Gamemodes = []string{"sandbox", "terrortown", "base", "darkrp", "murder", "prop_hunt", "zombiesurvival"}
	Maps      = []string{"gm_construct", "gm_flatgrass", "gm_bigcity", "ttt_minecraft_b5", "rp_downtown_v4c_v2"}
)

// Instance is one dedicated server installation.
type Instance struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Dir                string `json:"dir"`
	Branch             string `json:"branch"`
	Gamemode           string `json:"gamemode"`
	Map                string `json:"map"`
	MaxPlayers         int    `json:"max_players"`
	Port               int    `json:"port"`
	Tickrate           int    `json:"tickrate"`
	Developer          int    `json:"developer"`
	WorkshopCollection string `json:"workshop_collection,omitempty"` // +host_workshop_collection
	P2P                bool   `json:"p2p,omitempty"`                 // -p2p
	ExtraArgs          string `json:"extra_args,omitempty"`
}

// NewInstance returns an instance with the usual defaults.
func NewInstance(name, dir string) Instance {
	return Instance{
		Name:       name,
		Dir:        dir,
		Branch:     "public",
		Gamemode:   "sandbox",
		Map:        "gm_construct",
		MaxPlayers: 16,
		Port:       27015,
		Tickrate:   66,
	}
}

func (i *Instance) Validate() error {
	switch {
	case strings.TrimSpace(i.Name) == "":
		return errors.New("the server needs a name")
	case strings.TrimSpace(i.Dir) == "":
		return errors.New("the server needs a location folder")
	case strings.ContainsFunc(i.Gamemode, unicode.IsSpace) || i.Gamemode == "":
		return errors.New("gamemode must be a single word, e.g. sandbox")
	case strings.ContainsFunc(i.Map, unicode.IsSpace) || i.Map == "":
		return errors.New("level must be a single map name, e.g. gm_construct")
	case strings.ContainsFunc(i.Branch, unicode.IsSpace):
		return errors.New("branch name can't contain spaces")
	case i.MaxPlayers < 1 || i.MaxPlayers > 128:
		return errors.New("max players must be between 1 and 128")
	case i.Port < 1 || i.Port > 65535:
		return errors.New("port must be between 1 and 65535")
	case i.Tickrate < 1 || i.Tickrate > 1000:
		return errors.New("tickrate must be between 1 and 1000")
	case i.Developer < 0 || i.Developer > 2:
		return errors.New("developer must be 0, 1 or 2")
	}
	if _, err := i.workshopCollectionID(); err != nil {
		return err
	}
	_, err := SplitArgs(i.ExtraArgs)
	return err
}

// workshopCollectionID returns the numeric collection id, or "" when unset.
func (i *Instance) workshopCollectionID() (string, error) {
	s := strings.TrimSpace(i.WorkshopCollection)
	if s == "" {
		return "", nil
	}
	id, err := workshop.ParseID(s)
	if err != nil {
		return "", fmt.Errorf("workshop collection: %w", err)
	}
	return strconv.FormatUint(id, 10), nil
}

// Installed reports whether the server files are present.
func (i *Instance) Installed() bool {
	_, err := i.launcher()
	return err == nil
}

// launcher returns the srcds start script/binary for the platform and branch.
func (i *Instance) launcher() (string, error) {
	var names []string
	x64 := i.Branch == "x86-64"
	switch runtime.GOOS {
	case "windows":
		names = []string{"srcds.exe", "srcds_win64.exe"}
	case "linux":
		names = []string{"srcds_run", "srcds_run_x64"}
	default:
		return "", fmt.Errorf("Garry's Mod dedicated servers don't run on %s", runtime.GOOS)
	}
	if x64 {
		names[0], names[1] = names[1], names[0]
	}
	for _, n := range names {
		p := filepath.Join(i.Dir, n)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("the server is not installed yet: click Install")
}

// Args returns the srcds arguments. A non-empty token is passed as +sv_setsteamaccount.
func (i *Instance) Args(token string) ([]string, error) {
	extra, err := SplitArgs(i.ExtraArgs)
	if err != nil {
		return nil, err
	}
	args := []string{"-game", "garrysmod", "-console"}
	if runtime.GOOS != "windows" {
		args = append(args, "-norestart") // GWorX restarts the server itself
	}
	args = append(args,
		"-port", strconv.Itoa(i.Port),
		"-tickrate", strconv.Itoa(i.Tickrate),
		"+maxplayers", strconv.Itoa(i.MaxPlayers),
		"+gamemode", i.Gamemode, // before +map so the map loads with it
		"+map", i.Map,
		"+developer", strconv.Itoa(i.Developer),
	)
	if collection, err := i.workshopCollectionID(); err != nil {
		return nil, err
	} else if collection != "" {
		args = append(args, "+host_workshop_collection", collection)
	}
	if i.P2P {
		args = append(args, "-p2p")
	}
	args = append(args, extra...)
	if token = strings.TrimSpace(token); token != "" {
		args = append(args, "+sv_setsteamaccount", token)
	}
	return args, nil
}

// Command returns the program and arguments that start the server. wrapper
// (e.g. "steam-run") is prepended when set.
func (i *Instance) Command(token, wrapper string) (string, []string, error) {
	exe, err := i.launcher()
	if err != nil {
		return "", nil, err
	}
	args, err := i.Args(token)
	if err != nil {
		return "", nil, err
	}
	args = append([]string{exe}, args...)

	w, err := SplitArgs(wrapper)
	if err != nil {
		return "", nil, fmt.Errorf("launch wrapper: %w", err)
	}
	if len(w) > 0 {
		prog, err := exec.LookPath(w[0])
		if err != nil {
			return "", nil, fmt.Errorf("launch wrapper %q not found", w[0])
		}
		return prog, append(w[1:], args...), nil
	}
	return args[0], args[1:], nil
}

// CommandLine renders the start command for display, hiding the token.
func (i *Instance) CommandLine(token, wrapper string) string {
	exe, err := i.launcher()
	if err != nil {
		exe = filepath.Join(i.Dir, "srcds_run")
		if runtime.GOOS == "windows" {
			exe = filepath.Join(i.Dir, "srcds.exe")
		}
	}
	args, err := i.Args(token)
	if err != nil {
		return "invalid extra arguments: " + err.Error()
	}

	parts := []string{}
	if w := strings.TrimSpace(wrapper); w != "" {
		parts = append(parts, w)
	}
	parts = append(parts, quote(exe))
	for n, a := range args {
		if n > 0 && args[n-1] == "+sv_setsteamaccount" {
			a = "********"
		}
		parts = append(parts, quote(a))
	}
	return strings.Join(parts, " ")
}

func quote(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\"'") {
		return strconv.Quote(s)
	}
	return s
}

// SplitArgs splits a command line on spaces, honouring "double" and 'single' quotes.
func SplitArgs(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inArg := false
	var q rune

	for _, r := range s {
		switch {
		case q != 0:
			if r == q {
				q = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			q = r
			inArg = true
		case unicode.IsSpace(r):
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	if q != 0 {
		return nil, errors.New("unterminated quote")
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}
