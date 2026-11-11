// Package cli implements the GWorX command line interface.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/Pika-Software/gworx/internal/config"
	"github.com/Pika-Software/gworx/internal/core"
	"github.com/Pika-Software/gworx/internal/gmaops"
	"github.com/Pika-Software/gworx/internal/store"
	"github.com/Pika-Software/gworx/internal/workshop"
)

// Options configure Main.
type Options struct {
	Version string
	// GUI starts the graphical interface; nil when built without it.
	GUI func() error
}

var errUsage = errors.New("usage")

const usage = `GWorX - Garry's Mod toolkit

Usage:
  gworx                         start the GUI (when available)
  gworx <command> [flags] [args]

Workshop:
  download [flags] <id|url>...  download addons (-no-extract, -o <dir>)
  upload [flags] [gma|folder]   upload a GMA or build+upload a folder (-id to update)
  published [flags]             list your addons (-refresh, -add <id>, -forget <id>)
  delete [-y] <id>              delete a Workshop item (Steamworks only)
  history [flags]               downloaded addons (-remove <id>, -clear)
  login                         log steamcmd in once so uploads reuse the cached login

GMA:
  build [flags] <folder>        build a GMA (reads addon.json, flags override)
  extract [flags] <file.gma>    extract a GMA (-o <dir>, -no-verify)
  info [-files] <file.gma>      show GMA metadata

Other:
  config [get <key> | set <key> <value> | path]
  gui                           start the GUI
  version

Common flags: -backend steamcmd|steamworks, -q (quiet), -json (where supported).
Run "gworx <command> -h" for command flags.
`

// Main runs the CLI and returns the process exit code.
func Main(args []string, opt Options) int {
	if len(args) == 0 {
		if opt.GUI != nil {
			return exit(opt.GUI())
		}
		fmt.Fprint(os.Stderr, usage)
		return 2
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "-help", "--help":
		fmt.Print(usage)
		return 0
	case "version", "-version", "--version":
		fmt.Println("gworx", opt.Version)
		return 0
	case "gui":
		if opt.GUI == nil {
			fmt.Fprintln(os.Stderr, "this build of gworx has no GUI")
			return 1
		}
		return exit(opt.GUI())
	}

	fn, ok := commands[cmd]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return exit(fn(ctx, cmd, rest))
}

func exit(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 2
	case errors.Is(err, errUsage):
		if err != errUsage {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		return 2
	case errors.Is(err, context.Canceled):
		fmt.Fprintln(os.Stderr, "cancelled")
		return 130
	default:
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
}

type command func(ctx context.Context, name string, args []string) error

var commands map[string]command

func init() {
	commands = map[string]command{
		"download":  cmdDownload,
		"upload":    cmdUpload,
		"published": cmdPublished,
		"delete":    cmdDelete,
		"history":   cmdHistory,
		"login":     cmdLogin,
		"build":     cmdBuild,
		"extract":   cmdExtract,
		"info":      cmdInfo,
		"config":    cmdConfig,
	}
}

type env struct {
	fs      *flag.FlagSet
	backend *string
	quiet   *bool
	json    *bool

	cfg *config.Config
	svc *core.Service
	out *printer
}

func newEnv(name, args string, withJSON bool) *env {
	e := &env{fs: flag.NewFlagSet(name, flag.ContinueOnError)}
	e.backend = e.fs.String("backend", "", "workshop backend: steamcmd or steamworks (default from config)")
	e.quiet = e.fs.Bool("q", false, "only print errors and results")
	if withJSON {
		e.json = e.fs.Bool("json", false, "print JSON")
	}
	e.fs.Usage = func() {
		fmt.Fprintf(e.fs.Output(), "Usage: gworx %s [flags] %s\n\nFlags:\n", name, args)
		e.fs.PrintDefaults()
	}
	return e
}

// parse handles flags placed before and after positional arguments.
func (e *env) parse(args []string) ([]string, error) {
	var pos []string
	for {
		if err := e.fs.Parse(args); err != nil {
			return nil, err
		}
		args = e.fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func (e *env) isSet(name string) bool {
	set := false
	e.fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

func (e *env) jsonOut() bool { return e.json != nil && *e.json }

// open loads config and store.
func (e *env) open() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	switch *e.backend {
	case "":
	case string(config.BackendSteamCMD), string(config.BackendSteamworks):
		cfg.Backend = config.Backend(*e.backend)
	default:
		return fmt.Errorf("unknown backend %q", *e.backend)
	}

	st, err := store.Open(config.Dir())
	if err != nil {
		return fmt.Errorf("library: %w", err)
	}

	e.cfg = cfg
	e.out = &printer{quiet: *e.quiet || e.jsonOut(), tty: term.IsTerminal(int(os.Stderr.Fd()))}
	e.svc = core.New(cfg, st, &prompter{})
	return nil
}

func (e *env) close() {
	if e.svc != nil {
		e.svc.Close()
	}
}

func (e *env) reporter() core.Reporter {
	return core.Reporter{Log: e.out.log, Progress: e.out.progress}
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// printer writes log lines and a progress indicator to stderr.
type printer struct {
	mu       sync.Mutex
	quiet    bool
	tty      bool
	inBar    bool
	lastPct  int
	lastDraw time.Time
}

func (p *printer) log(s string) {
	if p.quiet {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inBar {
		fmt.Fprint(os.Stderr, "\r\033[K")
		p.inBar = false
	}
	fmt.Fprintln(os.Stderr, s)
}

func (p *printer) progress(f float64) {
	if p.quiet || !p.tty || f < 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	pct := int(min(f, 1) * 100)
	if pct == p.lastPct && p.inBar || (time.Since(p.lastDraw) < 100*time.Millisecond && pct < 100) {
		return
	}
	p.lastPct, p.lastDraw = pct, time.Now()

	const width = 30
	n := pct * width / 100
	fmt.Fprintf(os.Stderr, "\r\033[K[%s%s] %3d%%", strings.Repeat("#", n), strings.Repeat(" ", width-n), pct)
	p.inBar = true
}

func (p *printer) done() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inBar {
		fmt.Fprintln(os.Stderr)
		p.inBar = false
	}
}

// prompter asks for steamcmd credentials on the terminal.
type prompter struct{}

var errNoTTY = "no terminal to ask for credentials: run \"gworx login\" interactively first"

func (prompter) Password(user string) (string, bool) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		fmt.Fprintln(os.Stderr, errNoTTY)
		return "", false
	}
	fmt.Fprintf(os.Stderr, "\nSteam password for %s (not saved by gworx): ", user)
	pw, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	return string(pw), err == nil
}

func (prompter) GuardCode(user, hint string) (string, bool) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, errNoTTY)
		return "", false
	}
	fmt.Fprintf(os.Stderr, "\n%s ", hint)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line), err == nil || (errors.Is(err, io.EOF) && line != "")
}

func cmdDownload(ctx context.Context, name string, args []string) error {
	e := newEnv(name, "<id|url>...", true)
	extract := e.fs.Bool("extract", false, "extract into the uploads folder (default from config)")
	noExtract := e.fs.Bool("no-extract", false, "only keep the raw GMA")
	outDir := e.fs.String("o", "", "extract into this folder (single item only)")
	ids, err := e.parse(args)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		e.fs.Usage()
		return errUsage
	}
	if *outDir != "" && len(ids) > 1 {
		return errors.New("-o can only be used with a single item")
	}
	if err := e.open(); err != nil {
		return err
	}
	defer e.close()

	doExtract := e.cfg.AutoExtract
	if e.isSet("extract") {
		doExtract = *extract
	}
	if *noExtract {
		doExtract = false
	}
	if *outDir != "" {
		doExtract = true
	}

	var results []store.Download
	for _, raw := range ids {
		id, err := workshop.ParseID(raw)
		if err != nil {
			return err
		}
		rec, err := e.svc.Download(ctx, id, core.DownloadOptions{Extract: doExtract, ExtractDir: *outDir}, e.reporter())
		e.out.done()
		if err != nil {
			return fmt.Errorf("%d: %w", id, err)
		}
		results = append(results, rec)
		if !e.jsonOut() {
			fmt.Println(rec.GMAPath)
			if rec.ExtractedPath != "" {
				fmt.Println(rec.ExtractedPath)
			}
		}
	}
	if e.jsonOut() {
		return printJSON(results)
	}
	return nil
}

func parseVisibility(s string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "public", "0":
		return workshop.VisibilityPublic, nil
	case "friends", "friendsonly", "friends-only", "1":
		return workshop.VisibilityFriendsOnly, nil
	case "private", "2":
		return workshop.VisibilityPrivate, nil
	case "unlisted", "3":
		return workshop.VisibilityUnlisted, nil
	}
	return 0, fmt.Errorf("unknown visibility %q (public, friends, private, unlisted)", s)
}

func cmdUpload(ctx context.Context, name string, args []string) error {
	e := newEnv(name, "[file.gma | addon-folder]", true)
	idFlag := e.fs.String("id", "", "Workshop item to update (empty: create a new item)")
	title := e.fs.String("title", "", "Workshop title (default: addon title)")
	descFile := e.fs.String("desc-file", "", "read the Workshop description from a file")
	preview := e.fs.String("preview", "", "preview image (required for new items)")
	vis := e.fs.String("visibility", "", "public, friends, private or unlisted (default: private for new items, kept for updates)")
	note := e.fs.String("note", "", "change note")
	bf := addBuildFlags(e.fs)

	pos, err := e.parse(args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		e.fs.Usage()
		return errUsage
	}
	if err := e.open(); err != nil {
		return err
	}
	defer e.close()

	req := workshop.UploadRequest{
		Title:       *title,
		PreviewPath: *preview,
		ChangeNote:  *note,
		Visibility:  workshop.VisibilityPrivate,
	}
	if *idFlag != "" {
		if req.ID, err = workshop.ParseID(*idFlag); err != nil {
			return err
		}
	}
	req.Description = *bf.desc
	if *descFile != "" {
		raw, err := os.ReadFile(*descFile)
		if err != nil {
			return err
		}
		req.Description = string(raw)
	}

	rec, known := e.svc.Store.GetPublished(req.ID)
	if req.ID != 0 && known {
		req.Visibility = rec.Visibility
		if req.PreviewPath == "" {
			req.PreviewPath = rec.PreviewPath
		}
	}
	if *vis != "" {
		if req.Visibility, err = parseVisibility(*vis); err != nil {
			return err
		}
	}

	opt := core.UploadOptions{}

	switch {
	case len(pos) == 1:
		st, err := os.Stat(pos[0])
		if err != nil {
			return err
		}
		if st.IsDir() {
			p, err := bf.project(e, pos[0])
			if err != nil {
				return err
			}
			opt.Project, opt.Build = p, true
			req.GMAPath = e.svc.BuildOutput(p)
		} else {
			req.GMAPath = pos[0]
		}
	case req.ID != 0 && known && rec.Project != nil:
		e.out.log("Rebuilding from " + rec.Project.SourceDir)
		p := *rec.Project
		opt.Project, opt.Build = &p, true
		req.GMAPath = e.svc.BuildOutput(&p)
	case req.ID != 0 && known && rec.GMAPath != "":
		req.GMAPath = rec.GMAPath
	default:
		e.fs.Usage()
		return fmt.Errorf("%w: give a GMA file or an addon folder", errUsage)
	}

	opt.Request = req
	id, err := e.svc.Upload(ctx, opt, e.reporter())
	e.out.done()
	if err != nil {
		return err
	}
	if e.jsonOut() {
		return printJSON(map[string]any{"id": id, "url": workshop.ItemURL(id), "gma": req.GMAPath})
	}
	fmt.Println(id)
	e.out.log(workshop.ItemURL(id))
	return nil
}

func cmdPublished(ctx context.Context, name string, args []string) error {
	e := newEnv(name, "", true)
	refresh := e.fs.Bool("refresh", false, "import your items from Steam first")
	add := e.fs.String("add", "", "add an existing item by id or link")
	forget := e.fs.String("forget", "", "remove an item from the list (the Workshop item is kept)")
	if _, err := e.parse(args); err != nil {
		return err
	}
	if err := e.open(); err != nil {
		return err
	}
	defer e.close()

	if *add != "" {
		id, err := workshop.ParseID(*add)
		if err != nil {
			return err
		}
		if _, err := e.svc.AddPublished(ctx, id); err != nil {
			return err
		}
	}
	if *forget != "" {
		id, err := workshop.ParseID(*forget)
		if err != nil {
			return err
		}
		if err := e.svc.Store.RemovePublished(id); err != nil {
			return err
		}
	}
	if *refresh {
		if _, err := e.svc.RefreshPublished(ctx, e.reporter()); err != nil {
			return err
		}
	}

	items := e.svc.Store.Published()
	if e.jsonOut() {
		return printJSON(items)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tUPDATED\tVISIBILITY\tTITLE")
	for _, it := range items {
		updated := "-"
		if !it.UpdatedAt.IsZero() {
			updated = it.UpdatedAt.Format(time.DateOnly)
		}
		vis := "?"
		if it.Visibility >= 0 && it.Visibility < len(workshop.VisibilityNames) {
			vis = workshop.VisibilityNames[it.Visibility]
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", it.ID, updated, vis, it.Title)
	}
	return w.Flush()
}

func confirm(question string) bool {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", question)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

func cmdDelete(ctx context.Context, name string, args []string) error {
	e := newEnv(name, "<id|url>", false)
	yes := e.fs.Bool("y", false, "don't ask for confirmation")
	pos, err := e.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		e.fs.Usage()
		return errUsage
	}
	id, err := workshop.ParseID(pos[0])
	if err != nil {
		return err
	}
	if !*yes && !confirm(fmt.Sprintf("Permanently delete Workshop item %d?", id)) {
		return errors.New("not confirmed (use -y)")
	}
	if err := e.open(); err != nil {
		return err
	}
	defer e.close()
	return e.svc.Delete(ctx, id, e.reporter())
}

func cmdHistory(ctx context.Context, name string, args []string) error {
	e := newEnv(name, "", true)
	remove := e.fs.String("remove", "", "remove an entry")
	deleteFiles := e.fs.Bool("delete-files", false, "with -remove: also delete the GMA and extracted files")
	clearAll := e.fs.Bool("clear", false, "remove all entries (files are kept)")
	if _, err := e.parse(args); err != nil {
		return err
	}
	if err := e.open(); err != nil {
		return err
	}
	defer e.close()

	st := e.svc.Store
	if *remove != "" {
		id, err := workshop.ParseID(*remove)
		if err != nil {
			return err
		}
		if *deleteFiles {
			for _, d := range st.Downloads() {
				if d.ID == id {
					if d.GMAPath != "" {
						os.Remove(d.GMAPath)
					}
					if d.ExtractedPath != "" {
						os.RemoveAll(d.ExtractedPath)
					}
				}
			}
		}
		if err := st.RemoveDownload(id); err != nil {
			return err
		}
	}
	if *clearAll {
		if err := st.ClearDownloads(); err != nil {
			return err
		}
	}

	items := st.Downloads()
	if e.jsonOut() {
		return printJSON(items)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tDOWNLOADED\tSIZE\tTITLE\tPATH")
	for _, d := range items {
		path := d.GMAPath
		if d.ExtractedPath != "" {
			path = d.ExtractedPath
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", d.ID, d.DownloadedAt.Format("2006-01-02 15:04"), core.HumanSize(d.Size), d.Title, path)
	}
	return w.Flush()
}

func cmdLogin(ctx context.Context, name string, args []string) error {
	e := newEnv(name, "", false)
	user := e.fs.String("user", "", "Steam username (saved to the config)")
	if _, err := e.parse(args); err != nil {
		return err
	}
	if err := e.open(); err != nil {
		return err
	}
	defer e.close()

	if *user != "" && *user != e.cfg.SteamUsername {
		e.cfg.SteamUsername = *user
		if err := e.cfg.Save(); err != nil {
			return err
		}
	}
	if e.cfg.SteamUsername == "" {
		return errors.New("no Steam username: use -user <name>")
	}
	e.out.log("Logging in as " + e.cfg.SteamUsername + ". Confirm on your phone if Steam Guard uses the mobile app.")
	if err := e.svc.SteamCMD().Login(ctx, e.out.log); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Logged in. steamcmd cached the credentials for later uploads.")
	return nil
}

type buildFlags struct {
	title, desc, author, typ, tags, require, ignore *string
	version                                         *int
	noDefaultIgnore                                 *bool
}

func addBuildFlags(fs *flag.FlagSet) *buildFlags {
	return &buildFlags{
		title:           fs.String("name", "", "addon title (default: addon.json)"),
		desc:            fs.String("description", "", "addon / Workshop description"),
		author:          fs.String("author", "", "addon author (default: config/Steam name)"),
		typ:             fs.String("type", "", "addon type: "+strings.Join(gmaopsCategories(), ", ")),
		tags:            fs.String("tags", "", "comma separated tags (1-3): "+strings.Join(gmaopsTags(), ", ")),
		require:         fs.String("require", "", "comma separated required Workshop ids"),
		ignore:          fs.String("ignore", "", "comma separated extra ignore wildcards"),
		version:         fs.Int("addon-version", 0, "addon version (integer, default 1)"),
		noDefaultIgnore: fs.Bool("no-default-ignore", false, "don't apply the default ignore list from the config"),
	}
}

func splitList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// project builds a gmaops.Project from addon.json, config defaults and flags.
func (b *buildFlags) project(e *env, dir string) (*gmaops.Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	p, err := gmaops.LoadAddonJSON(abs)
	if errors.Is(err, os.ErrNotExist) {
		p = &gmaops.Project{SourceDir: abs}
	} else if err != nil {
		return nil, fmt.Errorf("addon.json: %w", err)
	}
	p.SourceDir = abs

	var ignore []string
	if !*b.noDefaultIgnore {
		ignore = append(ignore, e.cfg.DefaultIgnore...)
	}
	for _, i := range append(p.Ignore, splitList(*b.ignore)...) {
		if !slices.Contains(ignore, i) {
			ignore = append(ignore, i)
		}
	}
	p.Ignore = ignore

	if *b.title != "" {
		p.Title = *b.title
	}
	if *b.desc != "" {
		p.Description = *b.desc
	}
	if *b.author != "" {
		p.Author = *b.author
	}
	if p.Author == "" {
		p.Author = e.svc.DefaultAuthor()
	}
	if *b.version > 0 {
		p.Version = int32(*b.version)
	}
	if p.Version < 1 {
		p.Version = 1
	}
	if *b.typ != "" {
		p.Category = strings.ToLower(*b.typ)
	}
	if *b.tags != "" {
		p.Tags = nil
		for _, t := range splitList(*b.tags) {
			p.Tags = append(p.Tags, strings.ToLower(t))
		}
	}
	if *b.require != "" {
		p.RequiredContent = nil
		for _, r := range splitList(*b.require) {
			id, err := workshop.ParseID(r)
			if err != nil {
				return nil, err
			}
			p.RequiredContent = append(p.RequiredContent, strconv.FormatUint(id, 10))
		}
	}
	if p.Title == "" {
		p.Title = filepath.Base(abs)
	}
	return p, p.Validate()
}

func cmdBuild(ctx context.Context, name string, args []string) error {
	e := newEnv(name, "<addon-folder>", true)
	out := e.fs.String("o", "", "output .gma (default: <data>/builds/<title>.gma)")
	saveJSON := e.fs.Bool("save-json", false, "write the final metadata to <folder>/addon.json")
	dryRun := e.fs.Bool("n", false, "only list the files that would be packed")
	bf := addBuildFlags(e.fs)
	pos, err := e.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		e.fs.Usage()
		return errUsage
	}
	if err := e.open(); err != nil {
		return err
	}
	defer e.close()

	p, err := bf.project(e, pos[0])
	if err != nil {
		return err
	}
	if *out != "" {
		p.Output = *out
	}

	if *dryRun {
		res, err := gmaops.Scan(p)
		if err != nil {
			return err
		}
		if e.jsonOut() {
			return printJSON(res)
		}
		for _, f := range res.Included {
			fmt.Printf("+ %s\n", f.Internal)
		}
		for _, f := range res.Ignored {
			fmt.Printf("- %s (ignored)\n", f)
		}
		for _, f := range res.Disallowed {
			fmt.Printf("! %s (not allowed)\n", f)
		}
		return nil
	}

	if *saveJSON {
		if err := gmaops.SaveAddonJSON(p); err != nil {
			return err
		}
	}

	dest := e.svc.BuildOutput(p)
	res, err := e.svc.Build(p, dest, e.reporter())
	e.out.done()
	if err != nil {
		return err
	}
	if e.jsonOut() {
		return printJSON(map[string]any{"output": dest, "files": len(res.Included), "skipped": res.Disallowed})
	}
	fmt.Println(dest)
	return nil
}

func cmdExtract(ctx context.Context, name string, args []string) error {
	e := newEnv(name, "<file.gma>", false)
	out := e.fs.String("o", "", "destination folder (default: <uploads>/<file name>)")
	noVerify := e.fs.Bool("no-verify", false, "skip CRC checks")
	pos, err := e.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		e.fs.Usage()
		return errUsage
	}
	if err := e.open(); err != nil {
		return err
	}
	defer e.close()

	dest := *out
	if dest == "" {
		base := strings.TrimSuffix(filepath.Base(pos[0]), filepath.Ext(pos[0]))
		dest = filepath.Join(e.cfg.Uploads(), base)
	}
	n, err := e.svc.Extract(pos[0], dest, !*noVerify, e.reporter())
	e.out.done()
	if err != nil {
		return err
	}
	e.out.log(fmt.Sprintf("%d files extracted", n))
	fmt.Println(dest)
	return nil
}

func cmdInfo(ctx context.Context, name string, args []string) error {
	e := newEnv(name, "<file.gma>", true)
	files := e.fs.Bool("files", false, "list files")
	pos, err := e.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		e.fs.Usage()
		return errUsage
	}

	inf, err := gmaops.Inspect(pos[0])
	if err != nil {
		return err
	}
	if e.jsonOut() {
		if !*files {
			inf.Files = nil
		}
		return printJSON(inf)
	}

	var total int64
	for _, f := range inf.Files {
		total += f.Size
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "Title:\t%s\n", inf.Title)
	fmt.Fprintf(w, "Author:\t%s\n", inf.Author)
	fmt.Fprintf(w, "Version:\t%d\n", inf.Version)
	if inf.Timestamp > 0 {
		fmt.Fprintf(w, "Created:\t%s\n", time.Unix(inf.Timestamp, 0).Format(time.DateTime))
	}
	if len(inf.RequiredContent) > 0 {
		fmt.Fprintf(w, "Requires:\t%s\n", strings.Join(inf.RequiredContent, ", "))
	}
	fmt.Fprintf(w, "Legacy:\t%v\n", inf.Legacy)
	fmt.Fprintf(w, "Files:\t%d (%s)\n", len(inf.Files), core.HumanSize(total))
	if err := w.Flush(); err != nil {
		return err
	}
	if *files {
		for _, f := range inf.Files {
			fmt.Printf("%10s  %s\n", core.HumanSize(f.Size), f.Path)
		}
	}
	return nil
}

func cmdConfig(ctx context.Context, name string, args []string) error {
	e := newEnv(name, "[get <key> | set <key> <value> | path]", false)
	pos, err := e.parse(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}

	switch {
	case len(pos) == 0:
		return printJSON(m)
	case pos[0] == "path" && len(pos) == 1:
		fmt.Println(filepath.Join(config.Dir(), "config.json"))
		return nil
	case pos[0] == "get" && len(pos) == 2:
		v, ok := m[pos[1]]
		if !ok {
			return fmt.Errorf("unknown key %q", pos[1])
		}
		if s, ok := v.(string); ok {
			fmt.Println(s)
			return nil
		}
		return printJSON(v)
	case pos[0] == "set" && len(pos) == 3:
		key, value := pos[1], pos[2]
		old, ok := m[key]
		if !ok {
			return fmt.Errorf("unknown key %q", key)
		}
		switch old.(type) {
		case bool:
			b, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("%s expects true or false", key)
			}
			m[key] = b
		case []any, nil:
			if _, isList := old.([]any); isList || key == "default_ignore" {
				m[key] = splitList(value)
			} else {
				m[key] = value
			}
		default:
			m[key] = value
		}
		if key == "backend" && value != string(config.BackendSteamCMD) && value != string(config.BackendSteamworks) {
			return fmt.Errorf("backend must be %s or %s", config.BackendSteamCMD, config.BackendSteamworks)
		}
		raw, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, cfg); err != nil {
			return err
		}
		return cfg.Save()
	}

	e.fs.Usage()
	return errUsage
}
