package server

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	got, err := SplitArgs(`+hostname "My Server" -p2p '+sv_tags a b'`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"+hostname", "My Server", "-p2p", "+sv_tags a b"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if _, err := SplitArgs(`"unterminated`); err == nil {
		t.Fatal("expected an error")
	}
}

func TestArgsAndToken(t *testing.T) {
	in := NewInstance("x", t.TempDir())
	in.Gamemode, in.Map, in.MaxPlayers, in.Port, in.Tickrate, in.Developer = "terrortown", "ttt_minecraft_b5", 24, 27016, 33, 1
	in.WorkshopCollection = "https://steamcommunity.com/sharedfiles/filedetails/?id=123456789"
	in.P2P = true
	in.ExtraArgs = `+hostname "My TTT"`

	args, err := in.Args("SECRET")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-port 27016", "-tickrate 33", "+maxplayers 24", "+gamemode terrortown", "+map ttt_minecraft_b5",
		"+developer 1", "+host_workshop_collection 123456789", "-p2p", "+hostname My TTT", "+sv_setsteamaccount SECRET",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
	if strings.Index(joined, "+gamemode") > strings.Index(joined, "+map") {
		t.Error("+gamemode must come before +map")
	}

	noToken, _ := in.Args("  ")
	if slices.Contains(noToken, "+sv_setsteamaccount") {
		t.Error("empty token must not add +sv_setsteamaccount")
	}

	line := in.CommandLine("SECRET", "steam-run")
	if strings.Contains(line, "SECRET") || !strings.Contains(line, "+sv_setsteamaccount ********") || !strings.HasPrefix(line, "steam-run ") {
		t.Errorf("command line = %q", line)
	}
}

func TestLauncher(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip()
	}
	dir := t.TempDir()
	in := NewInstance("x", dir)
	if in.Installed() {
		t.Fatal("empty folder must not count as installed")
	}
	for _, n := range []string{"srcds_linux", "srcds_linux64"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := in.launcher(); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("bare binaries must not count as installed: %v", err)
	}
	for _, n := range []string{"srcds_run", "srcds_run64"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	in.Branch = "x86-64"
	if exe, err := in.launcher(); err != nil || filepath.Base(exe) != "srcds_run" {
		t.Fatalf("branch must not choose the entrypoint, got %s, %v", exe, err)
	}

	in.Binary = "srcds_run64"
	if exe, err := in.launcher(); err != nil || filepath.Base(exe) != "srcds_run64" {
		t.Fatalf("explicit entrypoint = %s, %v", exe, err)
	}
	if args, err := in.Args(""); err != nil || !slices.Contains(args, "-norestart") {
		t.Fatalf("srcds_run64 args = %v, %v", args, err)
	}
	if !strings.Contains(in.CommandLine("", ""), "srcds_run64") {
		t.Fatal(in.CommandLine("", ""))
	}

	in.Binary = "srcds_linux"
	if err := in.Validate(); err == nil {
		t.Fatal("bare binary must be rejected")
	}
	if _, err := in.launcher(); err == nil {
		t.Fatal("bare binary must not launch")
	}

	in.Binary = "srcds_missing"
	if _, err := in.launcher(); err == nil || !strings.Contains(err.Error(), "srcds_run") {
		t.Fatalf("unknown entrypoint: %v", err)
	}
}

func TestValidate(t *testing.T) {
	if in := NewInstance("ok", "/srv"); in.Validate() != nil {
		t.Fatalf("defaults must be valid: %v", in.Validate())
	}
	if names := Entrypoints(); len(names) > 0 {
		named := NewInstance("ok", "/srv")
		named.Binary = names[len(names)-1]
		if err := named.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for name, mod := range map[string]func(*Instance){
		"blank name":        func(i *Instance) { i.Name = " " },
		"no folder":         func(i *Instance) { i.Dir = "" },
		"two-word gamemode": func(i *Instance) { i.Gamemode = "two words" },
		"no map":            func(i *Instance) { i.Map = "" },
		"branch spaces":     func(i *Instance) { i.Branch = "x 86" },
		"port too high":     func(i *Instance) { i.Port = 70000 },
		"0 players":         func(i *Instance) { i.MaxPlayers = 0 },
		"129 players":       func(i *Instance) { i.MaxPlayers = 129 },
		"tickrate 0":        func(i *Instance) { i.Tickrate = 0 },
		"developer 3":       func(i *Instance) { i.Developer = 3 },
		"bad workshop id":   func(i *Instance) { i.WorkshopCollection = "not-an-id" },
		"unterminated arg":  func(i *Instance) { i.ExtraArgs = `"open` },
		"bare binary":       func(i *Instance) { i.Binary = "srcds_linux" },
		"binary path":       func(i *Instance) { i.Binary = "bin/srcds_linux" },
	} {
		in := NewInstance("ok", "/srv")
		mod(&in)
		if in.Validate() == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestManager(t *testing.T) {
	cfgDir := t.TempDir()
	m, err := Open(cfgDir, func() Settings { return Settings{} })
	if err != nil {
		t.Fatal(err)
	}

	a, err := m.Create(NewInstance("My Server", filepath.Join(t.TempDir(), "a")))
	if err != nil || a.ID != "my-server" {
		t.Fatalf("create: %+v %v", a, err)
	}
	if _, err := m.Create(NewInstance("my server", filepath.Join(t.TempDir(), "b"))); err == nil {
		t.Error("duplicate names must be rejected")
	}
	if _, err := m.Create(NewInstance("Other", a.Dir)); err == nil {
		t.Error("two servers in one folder must be rejected")
	}
	b, err := m.Create(NewInstance("My-Server!", filepath.Join(t.TempDir(), "c")))
	if err != nil || b.ID != "my-server-2" {
		t.Fatalf("second id = %q, %v", b.ID, err)
	}

	b.Port = 27020
	wantBinary := ""
	if names := Entrypoints(); len(names) > 0 {
		wantBinary = names[len(names)-1]
	}
	b.Binary = wantBinary
	if err := m.Update(b); err != nil {
		t.Fatal(err)
	}
	renamed := b
	renamed.Name = "MY SERVER"
	if err := m.Update(renamed); err == nil {
		t.Error("update must not take another server's name")
	}

	// reload from disk
	m2, err := Open(cfgDir, func() Settings { return Settings{} })
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := m2.Get(b.ID); got.Port != 27020 || got.Binary != wantBinary {
		t.Errorf("persisted instance = %+v", got)
	}

	if err := m2.Start(a.ID); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("start without files: %v", err)
	}
	if m2.Runner(a.ID).State() != Stopped {
		t.Error("failed start must leave the server stopped")
	}

	if err := m2.Delete(a.ID, false); err != nil {
		t.Fatal(err)
	}
	if len(m2.Instances()) != 1 {
		t.Errorf("instances after delete: %d", len(m2.Instances()))
	}
}

func TestDeleteFilesRefusesForeignFolder(t *testing.T) {
	m, err := Open(t.TempDir(), func() Settings { return Settings{} })
	if err != nil {
		t.Fatal(err)
	}

	foreign := t.TempDir()
	keep := filepath.Join(foreign, "notes.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := m.Create(NewInstance("Foreign", foreign))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(in.ID, true); err == nil {
		t.Fatal("deleting a folder that isn't a server install must be refused")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("foreign file was removed: %v", err)
	}
	if _, ok := m.Get(in.ID); !ok {
		t.Fatal("instance must be kept when the delete is refused")
	}

	server := filepath.Join(t.TempDir(), "srv")
	if err := os.MkdirAll(filepath.Join(server, "garrysmod"), 0o755); err != nil {
		t.Fatal(err)
	}
	in2, err := m.Create(NewInstance("Real", server))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(in2.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(server); !os.IsNotExist(err) {
		t.Fatalf("server folder still exists: %v", err)
	}
}
