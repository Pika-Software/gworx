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
	in.ExtraArgs = `+hostname "My TTT"`

	args, err := in.Args("SECRET")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-port 27016", "-tickrate 33", "+maxplayers 24", "+gamemode terrortown", "+map ttt_minecraft_b5", "+developer 1", "+hostname My TTT", "+sv_setsteamaccount SECRET"} {
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
	for _, n := range []string{"srcds_run", "srcds_run_x64"} {
		os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755)
	}
	in.Branch = "x86-64"
	if exe, _ := in.launcher(); filepath.Base(exe) != "srcds_run_x64" {
		t.Errorf("x86-64 branch uses %s", exe)
	}
	in.Branch = "public"
	if exe, _ := in.launcher(); filepath.Base(exe) != "srcds_run" {
		t.Errorf("public branch uses %s", exe)
	}
}

func TestValidate(t *testing.T) {
	bad := []func(*Instance){
		func(i *Instance) { i.Name = " " },
		func(i *Instance) { i.Gamemode = "two words" },
		func(i *Instance) { i.Map = "" },
		func(i *Instance) { i.Port = 70000 },
		func(i *Instance) { i.MaxPlayers = 0 },
		func(i *Instance) { i.Developer = 3 },
		func(i *Instance) { i.ExtraArgs = `"open` },
	}
	for n, mod := range bad {
		in := NewInstance("ok", "/srv")
		mod(&in)
		if in.Validate() == nil {
			t.Errorf("case %d: expected a validation error", n)
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
	if err := m.Update(b); err != nil {
		t.Fatal(err)
	}

	// reload from disk
	m2, err := Open(cfgDir, func() Settings { return Settings{} })
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := m2.Get(b.ID); got.Port != 27020 {
		t.Errorf("persisted port = %d", got.Port)
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
