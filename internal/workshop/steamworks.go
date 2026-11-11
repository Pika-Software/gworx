package workshop

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Steamworks implements Backend through the Steamworks flat API, the same way
// nwmpublisher does: Valve's redistributable libsteam_api ships next to the
// gworx binary, the client initialises as Garry's Mod (app 4000) and talks to the
// running Steam client. The account must own Garry's Mod.
//
// The library is loaded at runtime with purego, so no cgo or SDK headers are needed.
type Steamworks struct {
	// StagingDir is where upload content folders are created ("" = system temp).
	StagingDir string

	startOnce sync.Once
	jobs      chan func()

	api *steamAPI // only used on the Steam thread

	// mu guards the fields below, which are shared with other goroutines.
	mu      sync.Mutex
	libPath string
	loaded  string
	persona string
}

// NewSteamworks returns a backend using the steam_api library at libPath
// ("" = FindSteamAPI).
func NewSteamworks(libPath, stagingDir string) *Steamworks {
	return &Steamworks{libPath: libPath, StagingDir: stagingDir}
}

func (s *Steamworks) Name() string { return "steamworks" }

func (s *Steamworks) CurrentUser() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persona
}

// Library returns the path of the loaded steam_api library ("" before the first use).
func (s *Steamworks) Library() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loaded
}

// SetLibPath changes the steam_api library ("" = FindSteamAPI). It has no
// effect once the library has been loaded.
func (s *Steamworks) SetLibPath(p string) {
	s.mu.Lock()
	s.libPath = p
	s.mu.Unlock()
}

// callback ids (k_iSteamUtilsCallbacks = 700, k_iSteamUGCCallbacks = 3400)
const (
	cbAPICallCompleted   = 703
	cbUGCQueryCompleted  = 3401
	cbCreateItemResult   = 3403
	cbSubmitItemUpdate   = 3404
	cbDownloadItemResult = 3406
	cbDeleteItemResult   = 3417

	invalidHandle = ^uint64(0)

	itemStateInstalled   = 4
	itemStateNeedsUpdate = 8
	itemStateDownloading = 16
	itemStatePending     = 32

	ugcMatchingItemsReadyToUse = 2
	userListPublished          = 0
	// LastUpdatedDesc (3) makes Steam answer "No connection" for Garry's Mod user
	// queries, so sort by creation; callers sort by update time themselves.
	userListSortCreationDesc = 0

	// Give up when Steam reports no progress for this long (nwmpublisher uses the same limit).
	stallTimeout = 120 * time.Second
	// How long to wait for the Steam client to start / log on.
	connectTimeout = 60 * time.Second
)

// Callback structs use #pragma pack(4) on Linux/macOS and pack(8) on Windows.
var structPack = func() int {
	if runtime.GOOS == "windows" {
		return 8
	}
	return 4
}()

type callbackMsg struct {
	User     int32
	Callback int32
	Param    *byte
	CubParam int32
}

type paramStringArray struct {
	Strings **byte
	Num     int32
}

type steamAPI struct {
	lib     uintptr
	pipe    int32
	ugc     uintptr
	user    uintptr
	friends uintptr

	initFlat func(msg *byte) int32
	initOld  func() bool
	shutdown func()
	getPipe  func() int32

	dispatchInit     func()
	runFrame         func(pipe int32)
	nextCallback     func(pipe int32, msg *callbackMsg) bool
	freeLastCallback func(pipe int32)
	getCallResult    func(pipe int32, call uint64, buf unsafe.Pointer, cub int32, expected int32, failed *bool) bool

	ugcFn, userFn, friendsFn uintptr

	getSteamID      func(user uintptr) uint64
	loggedOn        func(user uintptr) bool
	getPersonaName  func(friends uintptr) string
	setRichPresence func(friends uintptr, key, value string) bool

	createItem           func(ugc uintptr, app uint32, fileType int32) uint64
	startItemUpdate      func(ugc uintptr, app uint32, id uint64) uint64
	setItemTitle         func(ugc uintptr, h uint64, s string) bool
	setItemDescription   func(ugc uintptr, h uint64, s string) bool
	setItemVisibility    func(ugc uintptr, h uint64, v int32) bool
	setItemTags          func(ugc uintptr, h uint64, tags *paramStringArray, allowAdmin bool) bool
	setItemContent       func(ugc uintptr, h uint64, folder string) bool
	setItemPreview       func(ugc uintptr, h uint64, file string) bool
	submitItemUpdate     func(ugc uintptr, h uint64, note string) uint64
	getItemUpdateProgres func(ugc uintptr, h uint64, done *uint64, total *uint64) int32
	deleteItem           func(ugc uintptr, id uint64) uint64
	downloadItem         func(ugc uintptr, id uint64, high bool) bool
	getItemState         func(ugc uintptr, id uint64) uint32
	getItemDownloadInfo  func(ugc uintptr, id uint64, done *uint64, total *uint64) bool
	getItemInstallInfo   func(ugc uintptr, id uint64, size *uint64, folder *byte, folderSize uint32, ts *uint32) bool
	createUserQuery      func(ugc uintptr, account uint32, list int32, matching int32, sort int32, creator uint32, consumer uint32, page uint32) uint64
	sendQuery            func(ugc uintptr, q uint64) uint64
	getQueryResult       func(ugc uintptr, q uint64, index uint32, details unsafe.Pointer) bool
	releaseQuery         func(ugc uintptr, q uint64) bool

	initialized   bool
	warnedOffline bool
}

func (s *Steamworks) start() {
	s.startOnce.Do(func() {
		s.jobs = make(chan func())
		go func() {
			// Steam API calls are made from a single OS thread.
			runtime.LockOSThread()
			for job := range s.jobs {
				job()
			}
		}()
	})
}

// do runs fn on the Steam thread once the client is connected and logged on.
func (s *Steamworks) do(ctx context.Context, log Logger, fn func(a *steamAPI) error) error {
	if log == nil {
		log = nopLog
	}
	s.start()
	done := make(chan error, 1)
	s.jobs <- func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("steamworks: %v", r)
			}
		}()
		if err := s.connect(ctx, log); err != nil {
			done <- err
			return
		}
		done <- fn(s.api)
	}
	return <-done
}

// steamAPIName is the redistributable library file name for this platform.
func steamAPIName() string {
	switch runtime.GOOS {
	case "windows":
		return "steam_api64.dll"
	case "darwin":
		return "libsteam_api.dylib"
	}
	return "libsteam_api.so"
}

var libraryPathRe = regexp.MustCompile(`"path"\s+"([^"]+)"`)

// FindSteamAPI returns candidate steam_api libraries, best first: the one shipped
// next to the gworx binary (or in ../lib/gworx for packages), then Garry's Mod installs.
func FindSteamAPI() []string {
	var found []string
	seen := map[string]bool{}
	add := func(p string) {
		p = filepath.Clean(p)
		if seen[p] {
			return
		}
		seen[p] = true
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			found = append(found, p)
		}
	}

	name := steamAPIName()
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dir := filepath.Dir(exe)
		add(filepath.Join(dir, name))
		add(filepath.Join(dir, "..", "lib", "gworx", name))
		add(filepath.Join(dir, "..", "Frameworks", name)) // macOS .app bundles
	}

	for _, p := range findGModSteamAPI() {
		add(p)
	}
	return found
}

func findGModSteamAPI() []string {
	home, _ := os.UserHomeDir()

	var steamRoots []string
	switch runtime.GOOS {
	case "windows":
		steamRoots = []string{`C:\Program Files (x86)\Steam`, `C:\Program Files\Steam`}
	case "darwin":
		steamRoots = []string{filepath.Join(home, "Library", "Application Support", "Steam")}
	default:
		steamRoots = []string{
			filepath.Join(home, ".local", "share", "Steam"),
			filepath.Join(home, ".steam", "steam"),
			filepath.Join(home, ".steam", "root"),
			filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", ".local", "share", "Steam"),
		}
	}

	var libs []string
	for _, root := range steamRoots {
		libs = append(libs, root)
		raw, err := os.ReadFile(filepath.Join(root, "steamapps", "libraryfolders.vdf"))
		if err != nil {
			continue
		}
		for _, m := range libraryPathRe.FindAllStringSubmatch(string(raw), -1) {
			libs = append(libs, filepath.FromSlash(string(bytes.ReplaceAll([]byte(m[1]), []byte(`\\`), []byte(`\`)))))
		}
	}

	var rel []string
	switch runtime.GOOS {
	case "windows":
		rel = []string{`bin\win64\steam_api64.dll`}
	case "darwin":
		rel = []string{"GarrysMod_Signed.app/Contents/MacOS/libsteam_api.dylib", "bin/osx64/libsteam_api.dylib"}
	default:
		if runtime.GOARCH != "amd64" {
			return nil // Garry's Mod only ships x86 libraries
		}
		rel = []string{"bin/linux64/libsteam_api.so"}
	}

	var out []string
	for _, lib := range libs {
		for _, r := range rel {
			out = append(out, filepath.Join(lib, "steamapps", "common", "GarrysMod", r))
		}
	}
	var existing []string
	for _, p := range out {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			existing = append(existing, p)
		}
	}
	return existing
}

func findInterface(lib uintptr, prefix string) (uintptr, error) {
	for v := 40; v >= 1; v-- {
		if sym, err := lookupSymbol(lib, fmt.Sprintf("%s%03d", prefix, v)); err == nil && sym != 0 {
			return sym, nil
		}
	}
	return 0, fmt.Errorf("%s* not exported by the steam_api library", prefix)
}

// load opens the library and resolves every function. Errors are not retryable.
func (s *Steamworks) load() (a *steamAPI, err error) {
	s.mu.Lock()
	path := s.libPath
	s.mu.Unlock()
	if path == "" {
		found := FindSteamAPI()
		if len(found) == 0 {
			return nil, fmt.Errorf("%s not found: it ships next to gworx in release builds; otherwise set its path in Settings", steamAPIName())
		}
		path = found[0]
	}

	lib, err := openLibrary(path)
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", path, err)
	}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s is missing functions (too old?): %v", path, r)
		}
	}()

	a = &steamAPI{lib: lib}
	reg := func(fptr any, name string) { purego.RegisterLibFunc(fptr, lib, name) }

	if sym, _ := lookupSymbol(lib, "SteamAPI_InitFlat"); sym != 0 {
		purego.RegisterFunc(&a.initFlat, sym)
	} else {
		reg(&a.initOld, "SteamAPI_Init")
	}
	reg(&a.shutdown, "SteamAPI_Shutdown")
	reg(&a.getPipe, "SteamAPI_GetHSteamPipe")
	reg(&a.dispatchInit, "SteamAPI_ManualDispatch_Init")
	reg(&a.runFrame, "SteamAPI_ManualDispatch_RunFrame")
	reg(&a.nextCallback, "SteamAPI_ManualDispatch_GetNextCallback")
	reg(&a.freeLastCallback, "SteamAPI_ManualDispatch_FreeLastCallback")
	reg(&a.getCallResult, "SteamAPI_ManualDispatch_GetAPICallResult")

	reg(&a.getSteamID, "SteamAPI_ISteamUser_GetSteamID")
	reg(&a.loggedOn, "SteamAPI_ISteamUser_BLoggedOn")
	reg(&a.getPersonaName, "SteamAPI_ISteamFriends_GetPersonaName")
	reg(&a.setRichPresence, "SteamAPI_ISteamFriends_SetRichPresence")

	reg(&a.createItem, "SteamAPI_ISteamUGC_CreateItem")
	reg(&a.startItemUpdate, "SteamAPI_ISteamUGC_StartItemUpdate")
	reg(&a.setItemTitle, "SteamAPI_ISteamUGC_SetItemTitle")
	reg(&a.setItemDescription, "SteamAPI_ISteamUGC_SetItemDescription")
	reg(&a.setItemVisibility, "SteamAPI_ISteamUGC_SetItemVisibility")
	reg(&a.setItemTags, "SteamAPI_ISteamUGC_SetItemTags")
	reg(&a.setItemContent, "SteamAPI_ISteamUGC_SetItemContent")
	reg(&a.setItemPreview, "SteamAPI_ISteamUGC_SetItemPreview")
	reg(&a.submitItemUpdate, "SteamAPI_ISteamUGC_SubmitItemUpdate")
	reg(&a.getItemUpdateProgres, "SteamAPI_ISteamUGC_GetItemUpdateProgress")
	reg(&a.deleteItem, "SteamAPI_ISteamUGC_DeleteItem")
	reg(&a.downloadItem, "SteamAPI_ISteamUGC_DownloadItem")
	reg(&a.getItemState, "SteamAPI_ISteamUGC_GetItemState")
	reg(&a.getItemDownloadInfo, "SteamAPI_ISteamUGC_GetItemDownloadInfo")
	reg(&a.getItemInstallInfo, "SteamAPI_ISteamUGC_GetItemInstallInfo")
	reg(&a.createUserQuery, "SteamAPI_ISteamUGC_CreateQueryUserUGCRequest")
	reg(&a.sendQuery, "SteamAPI_ISteamUGC_SendQueryUGCRequest")
	reg(&a.getQueryResult, "SteamAPI_ISteamUGC_GetQueryUGCResult")
	reg(&a.releaseQuery, "SteamAPI_ISteamUGC_ReleaseQueryUGCRequest")

	if a.ugcFn, err = findInterface(lib, "SteamAPI_SteamUGC_v"); err != nil {
		return nil, err
	}
	if a.userFn, err = findInterface(lib, "SteamAPI_SteamUser_v"); err != nil {
		return nil, err
	}
	if a.friendsFn, err = findInterface(lib, "SteamAPI_SteamFriends_v"); err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.loaded = path
	s.mu.Unlock()
	return a, nil
}

// tryInit makes one attempt to initialise the client as Garry's Mod.
func (a *steamAPI) tryInit() error {
	os.Setenv("SteamAppId", "4000")
	os.Setenv("SteamGameId", "4000")
	// Don't let processes we start later (browser, file manager) inherit
	// Garry's Mod's Steam identity; the variables are only read during init.
	defer os.Unsetenv("SteamAppId")
	defer os.Unsetenv("SteamGameId")

	if a.initFlat != nil {
		msg := make([]byte, 1024)
		if res := a.initFlat(&msg[0]); res != 0 {
			text := cString(msg)
			if text == "" {
				text = fmt.Sprintf("code %d", res)
			}
			return errors.New(text)
		}
		return nil
	}
	if !a.initOld() {
		return errors.New("SteamAPI_Init failed")
	}
	return nil
}

// connect loads the library, initialises the client (retrying while Steam
// starts, like nwmpublisher) and waits until the user is logged on.
func (s *Steamworks) connect(ctx context.Context, log Logger) error {
	if s.api == nil {
		a, err := s.load()
		if err != nil {
			return err
		}
		s.api = a
	}
	a := s.api

	if !a.initialized {
		deadline := time.Now().Add(connectTimeout)
		delay := time.Second
		last := ""
		for {
			err := a.tryInit()
			if err == nil {
				break
			}
			if msg := err.Error(); msg != last {
				log("Waiting for Steam: " + msg)
				last = msg
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("can't connect to Steam (is it running and logged in with an account that owns Garry's Mod?): %w", err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			delay = min(delay*2, 5*time.Second)
		}

		a.dispatchInit()
		a.pipe = a.getPipe()

		var accessor func() uintptr
		purego.RegisterFunc(&accessor, a.ugcFn)
		a.ugc = accessor()
		purego.RegisterFunc(&accessor, a.userFn)
		a.user = accessor()
		purego.RegisterFunc(&accessor, a.friendsFn)
		a.friends = accessor()
		if a.ugc == 0 || a.user == 0 {
			a.shutdown()
			return errors.New("SteamAPI: failed to get the ISteamUGC/ISteamUser interfaces")
		}
		a.initialized = true
		log("Connected to Steam through " + s.Library())

		if a.friends != 0 {
			name := a.getPersonaName(a.friends)
			s.mu.Lock()
			s.persona = name
			s.mu.Unlock()
			// Shows up on the friends list as "In the Workshop", under Garry's Mod's status token
			a.setRichPresence(a.friends, "steam_display", "#Status_Generic")
			a.setRichPresence(a.friends, "generic", "the Workshop")
		}
	}

	// Like nwmpublisher, use Steam's logged-on state only as a hint: the client can
	// report "not logged on" while Workshop requests still work, so wait briefly and
	// let the request itself fail if Steam really is offline.
	if a.loggedOn(a.user) || a.warnedOffline {
		return nil
	}
	deadline := time.Now().Add(5 * time.Second)
	for !a.loggedOn(a.user) {
		a.pump(nil)
		if time.Now().After(deadline) {
			log("Steam reports it is not logged on (offline mode?); trying anyway")
			a.warnedOffline = true
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil
}

// closeTimeout bounds how long Close waits for a running Steam job.
const closeTimeout = 5 * time.Second

// Close shuts the Steam API down. If a job is still running (cancel it
// first), Close gives up after closeTimeout so quitting never hangs.
func (s *Steamworks) Close() {
	if s.Library() == "" {
		return // never loaded
	}
	done := make(chan struct{})
	job := func() {
		if s.api != nil && s.api.initialized {
			s.api.shutdown()
			s.api.initialized = false
		}
		close(done)
	}
	timeout := time.After(closeTimeout)
	select {
	case s.jobs <- job:
	case <-timeout:
		return
	}
	select {
	case <-done:
	case <-timeout:
	}
}

// Initialized reports whether the Steam API has been loaded (the library path
// can no longer change for this process once it has).
func (s *Steamworks) Initialized() bool { return s.Library() != "" }

// pump runs a callback frame and passes every queued callback to fn. The data
// slice is only valid during the call.
func (a *steamAPI) pump(fn func(id int32, data []byte)) {
	a.runFrame(a.pipe)

	var msg callbackMsg
	for a.nextCallback(a.pipe, &msg) {
		if fn != nil && msg.Param != nil && msg.CubParam > 0 {
			fn(msg.Callback, unsafe.Slice(msg.Param, msg.CubParam))
		}
		a.freeLastCallback(a.pipe)
	}
}

// wait pumps callbacks until the API call completes. tick runs every iteration;
// a zero timeout waits until ctx is done.
func (a *steamAPI) wait(ctx context.Context, call uint64, expected int32, timeout time.Duration, tick func()) ([]byte, error) {
	if call == 0 || call == invalidHandle {
		return nil, errors.New("Steam refused the request")
	}

	started := time.Now()
	for {
		var out []byte
		var err error
		done := false

		a.pump(func(id int32, data []byte) {
			if done || id != cbAPICallCompleted || len(data) < 16 || binary.LittleEndian.Uint64(data[0:8]) != call {
				return
			}
			done = true
			cub := binary.LittleEndian.Uint32(data[12:16])
			buf := make([]byte, max(int(cub), 1))
			var failed bool
			if !a.getCallResult(a.pipe, call, unsafe.Pointer(&buf[0]), int32(cub), expected, &failed) || failed {
				err = errors.New("Steam API call failed (I/O failure)")
				return
			}
			out = buf
		})
		if done {
			return out, err
		}

		if tick != nil {
			tick()
		}
		if timeout > 0 && time.Since(started) > timeout {
			return nil, errors.New("Steam did not answer in time")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func alignUp(off, size int) int {
	a := min(size, structPack)
	return (off + a - 1) / a * a
}

var eResultNames = map[int32]string{
	1: "OK", 2: "Failure", 3: "No connection", 5: "Invalid password", 6: "Logged in elsewhere",
	8: "Invalid parameter", 9: "File not found", 10: "Busy", 15: "Access denied", 16: "Timeout",
	17: "Banned", 20: "Service unavailable", 21: "Not logged on", 25: "Limit exceeded",
	33: "Insufficient privilege", 42: "Duplicate request", 84: "Rate limit exceeded",
}

func eResultErr(r int32) error {
	if r == 1 {
		return nil
	}
	if name, ok := eResultNames[r]; ok {
		return fmt.Errorf("steam error: %s (EResult %d)", name, r)
	}
	return fmt.Errorf("steam error: EResult %d", r)
}

func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

func nopLog(string)       {}
func nopProgress(float64) {}

func (s *Steamworks) Download(ctx context.Context, id uint64, log Logger, progress Progress) (string, error) {
	if log == nil {
		log = nopLog
	}
	if progress == nil {
		progress = nopProgress
	}
	var folder string

	err := s.do(ctx, log, func(a *steamAPI) error {
		ready := func() bool {
			st := a.getItemState(a.ugc, id)
			return st&itemStateInstalled != 0 && st&(itemStateNeedsUpdate|itemStateDownloading|itemStatePending) == 0
		}

		if ready() {
			log(fmt.Sprintf("Item %d is already installed and up to date", id))
		} else {
			log(fmt.Sprintf("Requesting download of %d through Steam...", id))
			if !a.downloadItem(a.ugc, id, true) {
				return errors.New("Steam refused the download: Garry's Mod must be installed in this Steam client (or use the steamcmd backend), and the item must exist")
			}

			idOff := alignUp(4, 8)
			var result error
			finished := false
			lastBytes, lastChange := uint64(0), time.Now()
			lastLog := time.Time{}

			for !finished {
				a.pump(func(cb int32, data []byte) {
					if cb != cbDownloadItemResult || len(data) < idOff+12 {
						return
					}
					// DownloadItemResult_t { AppId_t app; PublishedFileId_t id; EResult result }
					if binary.LittleEndian.Uint32(data[0:4]) != AppID || binary.LittleEndian.Uint64(data[idOff:idOff+8]) != id {
						return
					}
					finished = true
					result = eResultErr(int32(binary.LittleEndian.Uint32(data[idOff+8 : idOff+12])))
				})
				if result != nil {
					return fmt.Errorf("download failed: %w", result)
				}

				var done, total uint64
				if a.getItemDownloadInfo(a.ugc, id, &done, &total) && total > 0 {
					progress(float64(done) / float64(total))
					if done != lastBytes {
						lastBytes, lastChange = done, time.Now()
					}
					if time.Since(lastLog) > 2*time.Second {
						log(fmt.Sprintf("Downloading... %d / %d bytes", done, total))
						lastLog = time.Now()
					}
				} else {
					progress(-1)
				}

				if ready() {
					break
				}
				if time.Since(lastChange) > stallTimeout {
					return errors.New("download stalled (item may be private or removed)")
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(100 * time.Millisecond):
				}
			}
		}

		var size uint64
		var ts uint32
		buf := make([]byte, 4096)
		if !a.getItemInstallInfo(a.ugc, id, &size, &buf[0], uint32(len(buf)), &ts) {
			return errors.New("item installed but Steam did not report its location")
		}
		folder = cString(buf)
		log("Steam stored the item at " + folder)
		return nil
	})
	if err != nil {
		return "", err
	}
	return FindAddonFile(folder)
}

var updateStatusNames = map[int32]string{
	1: "Preparing config", 2: "Preparing content", 3: "Uploading content",
	4: "Uploading preview", 5: "Committing changes",
}

func (s *Steamworks) Upload(ctx context.Context, req UploadRequest, log Logger, progress Progress) (uint64, error) {
	if req.ID == 0 && req.Title == "" {
		return 0, errors.New("a title is required for new items")
	}
	if log == nil {
		log = nopLog
	}
	if progress == nil {
		progress = nopProgress
	}

	// The content folder must hold exactly one .gma and stay in place until
	// Steam's completion callback has returned.
	tmp, cleanup, err := prepareContent(s.StagingDir, req.GMAPath)
	if err != nil {
		return 0, err
	}
	defer cleanup()
	content := filepath.Join(tmp, "content")

	preview := ""
	if req.PreviewPath != "" {
		if preview, err = filepath.Abs(req.PreviewPath); err != nil {
			return 0, err
		}
	}

	id := req.ID
	err = s.do(ctx, log, func(a *steamAPI) error {
		progress(-1)

		if id == 0 {
			log("Creating a new Workshop item...")
			buf, err := a.wait(ctx, a.createItem(a.ugc, AppID, 0), cbCreateItemResult, stallTimeout, nil)
			if err != nil {
				return err
			}
			if err := eResultErr(int32(binary.LittleEndian.Uint32(buf[0:4]))); err != nil {
				return fmt.Errorf("creating item: %w", err)
			}
			off := alignUp(4, 8)
			id = binary.LittleEndian.Uint64(buf[off : off+8])
			if len(buf) > off+8 && buf[off+8] != 0 {
				log("NOTE: you need to accept the Steam Workshop legal agreement for this item to become visible.")
			}
			log(fmt.Sprintf("Created item %d", id))
		}

		h := a.startItemUpdate(a.ugc, AppID, id)
		if h == invalidHandle {
			return errors.New("StartItemUpdate failed")
		}

		if req.Title != "" && !a.setItemTitle(a.ugc, h, req.Title) {
			return errors.New("SetItemTitle failed (too long?)")
		}
		if req.Description != "" && !a.setItemDescription(a.ugc, h, req.Description) {
			return errors.New("SetItemDescription failed (too long?)")
		}
		vis := req.Visibility
		if vis < 0 && req.ID == 0 {
			vis = VisibilityPrivate
		}
		if vis >= 0 && !a.setItemVisibility(a.ugc, h, int32(vis)) {
			return errors.New("SetItemVisibility failed")
		}
		if !a.setItemContent(a.ugc, h, content) {
			return errors.New("SetItemContent failed")
		}
		if preview != "" && !a.setItemPreview(a.ugc, h, preview) {
			return errors.New("SetItemPreview failed (file missing or too large, keep it under 1 MB)")
		}
		if len(req.Tags) > 0 {
			bufs := make([][]byte, len(req.Tags))
			ptrs := make([]*byte, len(req.Tags))
			for i, t := range req.Tags {
				bufs[i] = append([]byte(t), 0)
				ptrs[i] = &bufs[i][0]
			}
			arr := paramStringArray{Strings: &ptrs[0], Num: int32(len(ptrs))}
			ok := a.setItemTags(a.ugc, h, &arr, false)
			runtime.KeepAlive(bufs)
			runtime.KeepAlive(ptrs)
			if !ok {
				return errors.New("SetItemTags failed")
			}
		}

		log("Submitting update...")
		lastStatus := int32(-1)
		lastBytes, lastChange := uint64(0), time.Now()
		warned := false
		buf, err := a.wait(ctx, a.submitItemUpdate(a.ugc, h, req.ChangeNote), cbSubmitItemUpdate, 0, func() {
			var done, total uint64
			st := a.getItemUpdateProgres(a.ugc, h, &done, &total)
			if st != lastStatus {
				if name, ok := updateStatusNames[st]; ok {
					log(name + "...")
				}
				lastStatus, lastChange = st, time.Now()
			}
			if done != lastBytes {
				lastBytes, lastChange = done, time.Now()
			}
			if total > 0 {
				progress(float64(done) / float64(total))
			} else {
				progress(-1)
			}
			// The outcome of a submitted update is unknown until Steam answers, so keep
			// waiting instead of failing; the user can still cancel.
			if time.Since(lastChange) > stallTimeout && !warned {
				log("Steam has reported no progress for 2 minutes; still waiting (cancel to stop)")
				warned = true
			}
		})
		if err != nil {
			return err
		}
		if err := eResultErr(int32(binary.LittleEndian.Uint32(buf[0:4]))); err != nil {
			return fmt.Errorf("submitting item %d: %w", id, err)
		}
		if buf[4] != 0 {
			log("NOTE: you need to accept the Steam Workshop legal agreement for this item to become visible.")
		}
		progress(1)
		return nil
	})

	return id, err
}

func (s *Steamworks) Delete(ctx context.Context, id uint64, log Logger) error {
	if log == nil {
		log = nopLog
	}
	return s.do(ctx, log, func(a *steamAPI) error {
		log(fmt.Sprintf("Deleting item %d...", id))
		buf, err := a.wait(ctx, a.deleteItem(a.ugc, id), cbDeleteItemResult, stallTimeout, nil)
		if err != nil {
			return err
		}
		return eResultErr(int32(binary.LittleEndian.Uint32(buf[0:4])))
	})
}

func (s *Steamworks) ListPublished(ctx context.Context, log Logger) ([]Item, error) {
	if log == nil {
		log = nopLog
	}
	var items []Item

	err := s.do(ctx, log, func(a *steamAPI) error {
		account := uint32(a.getSteamID(a.user) & 0xffffffff)
		details := make([]byte, 32*1024)

		// SteamUGCDetails_t: id @0, result @8, type @12, creator app @16, consumer app @20,
		// title[129] @24, description[8000] @153, then owner (uint64), created, updated,
		// added-to-list (uint32) and visibility (enum).
		owner := alignUp(153+8000, 8)
		updatedOff, visOff := owner+12, owner+20

		for page := uint32(1); ; page++ {
			q := a.createUserQuery(a.ugc, account, userListPublished, ugcMatchingItemsReadyToUse, userListSortCreationDesc, AppID, AppID, page)
			if q == invalidHandle {
				return errors.New("CreateQueryUserUGCRequest failed")
			}

			buf, err := a.wait(ctx, a.sendQuery(a.ugc, q), cbUGCQueryCompleted, stallTimeout, nil)
			if err != nil {
				a.releaseQuery(a.ugc, q)
				return err
			}
			if err := eResultErr(int32(binary.LittleEndian.Uint32(buf[8:12]))); err != nil {
				a.releaseQuery(a.ugc, q)
				return err
			}
			n := binary.LittleEndian.Uint32(buf[12:16])
			total := binary.LittleEndian.Uint32(buf[16:20])

			for i := uint32(0); i < n; i++ {
				clear(details)
				if !a.getQueryResult(a.ugc, q, i, unsafe.Pointer(&details[0])) {
					continue
				}
				if int32(binary.LittleEndian.Uint32(details[8:12])) != 1 {
					continue
				}
				items = append(items, Item{
					ID:          binary.LittleEndian.Uint64(details[0:8]),
					Title:       cString(details[24 : 24+129]),
					Description: cString(details[153 : 153+8000]),
					Updated:     int64(binary.LittleEndian.Uint32(details[updatedOff : updatedOff+4])),
					Visibility:  int(binary.LittleEndian.Uint32(details[visOff : visOff+4])),
				})
			}
			a.releaseQuery(a.ugc, q)

			if n == 0 || uint32(len(items)) >= total {
				return nil
			}
		}
	})

	return items, err
}
