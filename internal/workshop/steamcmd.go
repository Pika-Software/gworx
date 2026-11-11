package workshop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// SteamCMD implements Backend by driving the steamcmd command line tool.
//
// steamcmd caches credentials after the first successful "+login <user>"
// (password + Steam Guard), so later runs only pass the username and never
// store the password in GWorX.
type SteamCMD struct {
	Path       string
	User       string
	InstallDir string
	// Anonymous forces anonymous logins for downloads.
	Anonymous bool
	Prompt    Prompter

	mu sync.Mutex
}

func (s *SteamCMD) Name() string        { return "steamcmd" }
func (s *SteamCMD) CurrentUser() string { return s.User }

// FindSteamCMD looks for steamcmd in PATH and common install locations.
func FindSteamCMD() string {
	for _, name := range []string{"steamcmd", "steamcmd.sh", "steamcmd.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}

	home, _ := os.UserHomeDir()
	for _, p := range []string{
		filepath.Join(home, "steamcmd", "steamcmd.sh"),
		filepath.Join(home, ".steam", "steamcmd", "steamcmd.sh"),
		filepath.Join(home, ".local", "share", "Steam", "steamcmd", "steamcmd.sh"),
		`C:\steamcmd\steamcmd.exe`,
		`C:\Program Files\steamcmd\steamcmd.exe`,
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func (s *SteamCMD) exe() (string, error) {
	if s.Path != "" {
		return s.Path, nil
	}
	if p := FindSteamCMD(); p != "" {
		return p, nil
	}
	return "", errors.New("steamcmd not found: install it or set its path in Settings")
}

var (
	ansiRe  = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07]*\x07`)
	errorRe = regexp.MustCompile(`^ERROR[!( ]|FAILED \(|(?i:login failure)|(?i:invalid password)`)
	dlOkRe  = regexp.MustCompile(`Success\. Downloaded item (\d+) to "([^"]+)"`)
	vdfIDRe = regexp.MustCompile(`"publishedfileid"\s+"(\d+)"`)
	outIDRe = regexp.MustCompile(`(?i)PublishFileID\s*:?\s*(\d+)`)
	loginOk = regexp.MustCompile(`(?i)(Logged in OK|Waiting for user info\.\.\.OK)`)
)

type runOutput struct {
	lines  []string
	errors []string
}

func (o *runOutput) has(re *regexp.Regexp) []string {
	for _, l := range o.lines {
		if m := re.FindStringSubmatch(l); m != nil {
			return m
		}
	}
	return nil
}

// run executes steamcmd with "+login <login> <cmds...> +quit" and answers credential prompts.
func (s *SteamCMD) run(ctx context.Context, log Logger, login string, cmds ...string) (*runOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if log == nil {
		log = func(string) {}
	}

	exe, err := s.exe()
	if err != nil {
		return nil, err
	}

	args := []string{"+@ShutdownOnFailedCommand", "1"}
	if s.InstallDir != "" {
		dir, err := filepath.Abs(s.InstallDir)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		args = append(args, "+force_install_dir", dir)
	}
	args = append(args, "+login", login)
	args = append(args, cmds...)
	args = append(args, "+quit")

	shown := make([]string, 0, len(args))
	for _, a := range args {
		if strings.ContainsAny(a, " \t") {
			a = strconv.Quote(a)
		}
		shown = append(shown, a)
	}
	log("$ steamcmd " + strings.Join(shown, " "))

	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = append(os.Environ(), "TERM=dumb")

	term, err := startInteractive(cmd)
	if err != nil {
		return nil, fmt.Errorf("starting steamcmd: %w", err)
	}
	defer term.Close()

	out := &runOutput{}
	var secrets []string
	var promptErr error

	emit := func(line string) {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			return
		}
		for _, sec := range secrets {
			line = strings.ReplaceAll(line, sec, "********")
		}
		out.lines = append(out.lines, line)
		if errorRe.MatchString(strings.TrimSpace(line)) {
			out.errors = append(out.errors, strings.TrimSpace(line))
		}
		log(line)
	}

	answer := func(value string, ok bool) bool {
		if !ok {
			promptErr = errors.New("login cancelled")
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
			return false
		}
		if value != "" {
			secrets = append(secrets, value)
		}
		io.WriteString(term, value+"\n")
		return true
	}

	pending := ""
	buf := make([]byte, 4096)

	for {
		n, rerr := term.Read(buf)
		if n > 0 {
			chunk := ansiRe.ReplaceAllString(string(buf[:n]), "")
			pending += chunk

			for {
				i := strings.IndexAny(pending, "\r\n")
				if i < 0 {
					break
				}
				emit(pending[:i])
				pending = pending[i+1:]
			}

			low := strings.ToLower(strings.TrimSpace(pending))
			switch {
			case strings.HasSuffix(low, "password:"):
				emit(pending)
				pending = ""
				if s.Prompt == nil {
					answer("", false)
					break
				}
				pw, ok := s.Prompt.Password(login)
				answer(pw, ok)

			case strings.HasSuffix(low, "code:") && (strings.Contains(low, "guard") || strings.Contains(low, "two-factor") || strings.Contains(low, "two factor") || strings.Contains(low, "auth")):
				emit(pending)
				hint := strings.TrimSpace(pending)
				pending = ""
				if s.Prompt == nil {
					answer("", false)
					break
				}
				code, ok := s.Prompt.GuardCode(login, hint)
				answer(strings.TrimSpace(code), ok)
			}
		}
		if rerr != nil {
			break // EOF, or EIO once the pty child exits
		}
	}
	emit(pending)

	werr := cmd.Wait()
	if promptErr != nil {
		return out, promptErr
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if werr != nil {
		var ee *exec.ExitError
		if errors.As(werr, &ee) && len(out.errors) > 0 {
			return out, errors.New(out.errors[len(out.errors)-1])
		}
		// steamcmd frequently exits non-zero after a successful run; let callers decide.
		if !errors.As(werr, &ee) {
			return out, werr
		}
	}
	return out, nil
}

// Login runs "+login <user> +quit" so steamcmd caches the credentials.
func (s *SteamCMD) Login(ctx context.Context, log Logger) error {
	if s.User == "" {
		return errors.New("set your Steam username first")
	}
	out, err := s.run(ctx, log, s.User)
	if err != nil {
		return err
	}
	if out.has(loginOk) == nil {
		if len(out.errors) > 0 {
			return errors.New(out.errors[len(out.errors)-1])
		}
		return errors.New("login did not complete, see the log")
	}
	return nil
}

func (s *SteamCMD) downloadLogin() string {
	if s.Anonymous || s.User == "" {
		return "anonymous"
	}
	return s.User
}

func (s *SteamCMD) Download(ctx context.Context, id uint64, log Logger, progress Progress) (string, error) {
	if progress != nil {
		progress(-1)
	}

	ids := strconv.FormatUint(id, 10)
	out, err := s.run(ctx, log, s.downloadLogin(), "+workshop_download_item", strconv.Itoa(AppID), ids)

	if out == nil {
		return "", err
	}

	var dir string
	if m := out.has(dlOkRe); m != nil {
		dir = m[2]
	} else {
		if err != nil {
			return "", err
		}
		if len(out.errors) > 0 {
			return "", errors.New(out.errors[len(out.errors)-1])
		}
		return "", fmt.Errorf("steamcmd did not report a successful download of %d", id)
	}

	return FindAddonFile(dir)
}

// FindAddonFile returns the .gma (or legacy .bin) inside a downloaded workshop folder.
func FindAddonFile(p string) (string, error) {
	st, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return p, nil
	}

	entries, err := os.ReadDir(p)
	if err != nil {
		return "", err
	}

	var fallback string
	for _, ext := range []string{".gma", ".bin"} {
		for _, e := range entries {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ext) {
				return filepath.Join(p, e.Name()), nil
			}
		}
	}
	for _, e := range entries {
		if !e.IsDir() {
			if fallback != "" {
				fallback = ""
				break
			}
			fallback = filepath.Join(p, e.Name())
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("no addon file found in %s", p)
}

func vdfEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

// prepareContent creates a temporary content folder holding only the GMA.
// base is the parent folder for the temporary directory ("" = system temp).
func prepareContent(base, gmaPath string) (dir string, cleanup func(), err error) {
	if base != "" {
		if err := os.MkdirAll(base, 0o755); err != nil {
			return "", nil, err
		}
	}
	tmp, err := os.MkdirTemp(base, "gworx-upload-*")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.RemoveAll(tmp) }

	content := filepath.Join(tmp, "content")
	if err := os.MkdirAll(content, 0o755); err != nil {
		cleanup()
		return "", nil, err
	}

	dst := filepath.Join(content, filepath.Base(gmaPath))
	if !strings.EqualFold(filepath.Ext(dst), ".gma") {
		dst += ".gma"
	}

	if err := os.Link(gmaPath, dst); err != nil {
		if err := copyPlain(gmaPath, dst); err != nil {
			cleanup()
			return "", nil, err
		}
	}

	return tmp, cleanup, nil
}

func copyPlain(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func (s *SteamCMD) Upload(ctx context.Context, req UploadRequest, log Logger, progress Progress) (uint64, error) {
	if s.User == "" {
		return 0, errors.New("uploading needs a Steam account: set your username in Settings and log in")
	}
	if req.ID == 0 && req.Title == "" {
		return 0, errors.New("a title is required for new items")
	}
	if progress != nil {
		progress(-1)
	}

	// Stage next to the install dir rather than in /tmp: sandboxed steamcmd
	// builds (e.g. the NixOS FHS wrapper) have a private /tmp.
	base := ""
	if s.InstallDir != "" {
		abs, err := filepath.Abs(s.InstallDir)
		if err != nil {
			return 0, err
		}
		base = filepath.Join(abs, "gworx-staging")
	}
	tmp, cleanup, err := prepareContent(base, req.GMAPath)
	if err != nil {
		return 0, err
	}
	defer cleanup()

	var b strings.Builder
	kv := func(k, v string) { fmt.Fprintf(&b, "\t%q\t\t\"%s\"\n", k, vdfEscape(v)) }

	b.WriteString("\"workshopitem\"\n{\n")
	kv("appid", strconv.Itoa(AppID))
	kv("publishedfileid", strconv.FormatUint(req.ID, 10))
	kv("contentfolder", filepath.Join(tmp, "content"))
	if req.PreviewPath != "" {
		abs, err := filepath.Abs(req.PreviewPath)
		if err != nil {
			return 0, err
		}
		kv("previewfile", abs)
	}
	kv("visibility", strconv.Itoa(req.Visibility))
	if req.Title != "" {
		kv("title", req.Title)
	}
	if req.Description != "" {
		kv("description", req.Description)
	}
	kv("changenote", req.ChangeNote)
	b.WriteString("}\n")

	vdf := filepath.Join(tmp, "item.vdf")
	if err := os.WriteFile(vdf, []byte(b.String()), 0o644); err != nil {
		return 0, err
	}

	out, err := s.run(ctx, log, s.User, "+workshop_build_item", vdf)
	if err != nil {
		return 0, err
	}
	if len(out.errors) > 0 {
		return 0, errors.New(out.errors[len(out.errors)-1])
	}

	id := req.ID
	if raw, err := os.ReadFile(vdf); err == nil {
		if m := vdfIDRe.FindSubmatch(raw); m != nil {
			if v, _ := strconv.ParseUint(string(m[1]), 10, 64); v != 0 {
				id = v
			}
		}
	}
	if id == 0 {
		if m := out.has(outIDRe); m != nil {
			id, _ = strconv.ParseUint(m[1], 10, 64)
		}
	}
	if id == 0 {
		return 0, errors.New("steamcmd did not return a published file id, see the log")
	}
	return id, nil
}

func (s *SteamCMD) Delete(ctx context.Context, id uint64, log Logger) error {
	return fmt.Errorf("deleting items: %w (switch to Steamworks or delete it on the Workshop page)", ErrNotSupported)
}

func (s *SteamCMD) ListPublished(ctx context.Context, log Logger) ([]Item, error) {
	return nil, ErrNotSupported
}
