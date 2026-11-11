package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/Pika-Software/gworx/internal/workshop"
)

// Settings are the global values needed to install and start servers.
type Settings struct {
	SteamCMD *workshop.SteamCMD
	Token    string // Steam Game Server Account Token
	Wrapper  string // launch wrapper, e.g. "steam-run"
}

// Manager stores instances and their runners.
type Manager struct {
	mu        sync.Mutex
	path      string
	instances []Instance
	runners   map[string]*Runner

	// Settings returns the current global settings.
	Settings func() Settings
}

// Open loads <dir>/servers.json.
func Open(dir string, settings func() Settings) (*Manager, error) {
	m := &Manager{path: filepath.Join(dir, "servers.json"), runners: map[string]*Runner{}, Settings: settings}
	raw, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	} else if err != nil {
		return m, err
	}
	return m, json.Unmarshal(raw, &m.instances)
}

func (m *Manager) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m.instances, "", "\t")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

// Instances returns all instances in creation order.
func (m *Manager) Instances() []Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.instances)
}

func (m *Manager) Get(id string) (Instance, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, in := range m.instances {
		if in.ID == id {
			return in, true
		}
	}
	return Instance{}, false
}

var idRe = regexp.MustCompile(`[^a-z0-9]+`)

func (m *Manager) checkUniqueLocked(in Instance) error {
	for _, other := range m.instances {
		if other.ID == in.ID {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(other.Name), strings.TrimSpace(in.Name)) {
			return fmt.Errorf("a server named %q already exists", in.Name)
		}
		if a, b := filepath.Clean(other.Dir), filepath.Clean(in.Dir); a == b {
			return fmt.Errorf("server %q already uses %s", other.Name, in.Dir)
		}
	}
	return nil
}

// Create adds a new instance and returns it with its ID set.
func (m *Manager) Create(in Instance) (Instance, error) {
	if err := in.Validate(); err != nil {
		return in, err
	}
	abs, err := filepath.Abs(in.Dir)
	if err != nil {
		return in, err
	}
	in.Dir = abs

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkUniqueLocked(in); err != nil {
		return in, err
	}

	base := strings.Trim(idRe.ReplaceAllString(strings.ToLower(in.Name), "-"), "-")
	if base == "" {
		base = "server"
	}
	in.ID = base
	for n := 2; slices.ContainsFunc(m.instances, func(x Instance) bool { return x.ID == in.ID }); n++ {
		in.ID = fmt.Sprintf("%s-%d", base, n)
	}

	m.instances = append(m.instances, in)
	return in, m.saveLocked()
}

// Update replaces the stored instance with the same ID.
func (m *Manager) Update(in Instance) error {
	if err := in.Validate(); err != nil {
		return err
	}
	abs, err := filepath.Abs(in.Dir)
	if err != nil {
		return err
	}
	in.Dir = abs

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkUniqueLocked(in); err != nil {
		return err
	}
	for i := range m.instances {
		if m.instances[i].ID == in.ID {
			m.instances[i] = in
			return m.saveLocked()
		}
	}
	return fmt.Errorf("unknown server %q", in.ID)
}

// Delete removes an instance (it must be stopped). With files, its folder is deleted too.
func (m *Manager) Delete(id string, files bool) error {
	if st := m.Runner(id).State(); st != Stopped {
		return fmt.Errorf("stop the server first (it is %s)", strings.ToLower(st.String()))
	}

	m.mu.Lock()
	var dir string
	m.instances = slices.DeleteFunc(m.instances, func(x Instance) bool {
		if x.ID == id {
			dir = x.Dir
			return true
		}
		return false
	})
	delete(m.runners, id)
	err := m.saveLocked()
	m.mu.Unlock()

	if err == nil && files && dir != "" {
		err = os.RemoveAll(dir)
	}
	return err
}

// Runner returns the runner of an instance, creating it on first use.
func (m *Manager) Runner(id string) *Runner {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runners[id]
	if !ok {
		r = &Runner{}
		m.runners[id] = r
	}
	return r
}

// Start launches an instance after checking that no running instance uses its port.
func (m *Manager) Start(id string) error {
	in, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("unknown server %q", id)
	}
	for _, other := range m.Instances() {
		if other.ID != id && other.Port == in.Port && m.Runner(other.ID).State() != Stopped {
			return fmt.Errorf("port %d is already used by running server %q", in.Port, other.Name)
		}
	}
	s := m.Settings()
	return m.Runner(id).start(in, s.Token, s.Wrapper)
}

// Restart stops (if needed) and starts the instance again.
func (m *Manager) Restart(id string) error {
	if err := m.Runner(id).Stop(); err != nil {
		return err
	}
	return m.Start(id)
}

// Install installs or updates the server files with steamcmd. progress gets 0..1 or -1.
func (m *Manager) Install(ctx context.Context, id string, validate bool, progress workshop.Progress) error {
	in, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("unknown server %q", id)
	}
	r := m.Runner(id)

	r.mu.Lock()
	if r.state != Stopped {
		st := r.state
		r.mu.Unlock()
		return fmt.Errorf("stop the server first (it is %s)", strings.ToLower(st.String()))
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.state = Installing
	r.installCancel = cancel
	cb := r.onState
	r.mu.Unlock()
	if cb != nil {
		cb(Installing)
	}
	defer func() {
		r.mu.Lock()
		r.installCancel = nil
		r.mu.Unlock()
		r.setState(Stopped)
	}()

	if err := os.MkdirAll(in.Dir, 0o755); err != nil {
		return err
	}

	branch := in.Branch
	if branch == "" {
		branch = "public"
	}
	r.Print(fmt.Sprintf("* Installing Garry's Mod Dedicated Server (%s branch) into %s", branch, in.Dir))
	err := m.Settings().SteamCMD.AppUpdate(ctx, in.Dir, AppID, branch, validate, r.Print, progress)
	if err != nil {
		r.Print("* Install failed: " + err.Error())
		return err
	}
	r.Print("* Server files are up to date")
	return nil
}

// Running returns the number of instances that are not stopped.
func (m *Manager) Running() int {
	n := 0
	for _, in := range m.Instances() {
		if m.Runner(in.ID).State() != Stopped {
			n++
		}
	}
	return n
}

// StopAll stops every running instance in parallel.
func (m *Manager) StopAll() {
	var wg sync.WaitGroup
	for _, in := range m.Instances() {
		r := m.Runner(in.ID)
		if r.State() == Stopped {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Stop()
			r.Wait()
		}()
	}
	wg.Wait()
}
