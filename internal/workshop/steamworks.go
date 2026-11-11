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

// Steamworks implements Backend through the Steamworks flat API (libsteam_api),
// loaded at runtime. It needs a running, logged-in Steam client that owns Garry's Mod.
// The library shipped with Garry's Mod (bin/linux64, bin/win64) works.
type Steamworks struct {
	LibPath string

	startOnce sync.Once
	jobs      chan func()

	api     *steamAPI
	persona string
}

func (s *Steamworks) Name() string { return "steamworks" }

func (s *Steamworks) CurrentUser() string { return s.persona }

// callback ids (k_iSteamUGCCallbacks = 3400, k_iSteamUtilsCallbacks = 700)
const (
	cbAPICallCompleted   = 703
	cbUGCQueryCompleted  = 3401
	cbCreateItemResult   = 3403
	cbSubmitItemUpdate   = 3404
	cbDeleteItemResult   = 3417
	invalidHandle        = ^uint64(0)
	itemStateInstalled   = 4
	itemStateNeedsUpdate = 8
	itemStateDownloading = 16
	itemStatePending     = 32
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
	lib  uintptr
	pipe int32
	ugc  uintptr
	user uintptr

	shutdown         func()
	getPipe          func() int32
	dispatchInit     func()
	runFrame         func(pipe int32)
	nextCallback     func(pipe int32, msg *callbackMsg) bool
	freeLastCallback func(pipe int32)
	getCallResult    func(pipe int32, call uint64, buf unsafe.Pointer, cub int32, expected int32, failed *bool) bool

	getSteamID     func(user uintptr) uint64
	getPersonaName func(friends uintptr) string

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

// do runs fn on the Steam thread after making sure the API is initialised.
func (s *Steamworks) do(fn func(a *steamAPI) error) error {
	s.start()
	done := make(chan error, 1)
	s.jobs <- func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("steamworks: %v", r)
			}
		}()
		if err := s.init(); err != nil {
			done <- err
			return
		}
		done <- fn(s.api)
	}
	return <-done
}

func findInterface(lib uintptr, prefix string) (uintptr, error) {
	for v := 40; v >= 1; v-- {
		if sym, err := lookupSymbol(lib, fmt.Sprintf("%s%03d", prefix, v)); err == nil && sym != 0 {
			return sym, nil
		}
	}
	return 0, fmt.Errorf("%s* not exported by steam_api library", prefix)
}

func (s *Steamworks) init() (err error) {
	if s.api != nil {
		return nil
	}
	if s.LibPath == "" {
		if found := FindSteamAPI(); len(found) > 0 {
			s.LibPath = found[0]
		} else {
			return errors.New("steam_api library not found: set its path in Settings (Garry's Mod ships one in bin/linux64 or bin/win64)")
		}
	}

	lib, err := openLibrary(s.LibPath)
	if err != nil {
		return fmt.Errorf("loading %s: %w", s.LibPath, err)
	}

	a := &steamAPI{lib: lib}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("steam_api library is missing functions (too old?): %v", r)
		}
	}()

	reg := func(fptr any, name string) { purego.RegisterLibFunc(fptr, lib, name) }

	reg(&a.shutdown, "SteamAPI_Shutdown")
	reg(&a.getPipe, "SteamAPI_GetHSteamPipe")
	reg(&a.dispatchInit, "SteamAPI_ManualDispatch_Init")
	reg(&a.runFrame, "SteamAPI_ManualDispatch_RunFrame")
	reg(&a.nextCallback, "SteamAPI_ManualDispatch_GetNextCallback")
	reg(&a.freeLastCallback, "SteamAPI_ManualDispatch_FreeLastCallback")
	reg(&a.getCallResult, "SteamAPI_ManualDispatch_GetAPICallResult")
	reg(&a.getSteamID, "SteamAPI_ISteamUser_GetSteamID")
	reg(&a.getPersonaName, "SteamAPI_ISteamFriends_GetPersonaName")

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

	ugcFn, err := findInterface(lib, "SteamAPI_SteamUGC_v")
	if err != nil {
		return err
	}
	userFn, err := findInterface(lib, "SteamAPI_SteamUser_v")
	if err != nil {
		return err
	}
	friendsFn, err := findInterface(lib, "SteamAPI_SteamFriends_v")
	if err != nil {
		return err
	}

	os.Setenv("SteamAppId", "4000")
	os.Setenv("SteamGameId", "4000")

	if sym, _ := lookupSymbol(lib, "SteamAPI_InitFlat"); sym != 0 {
		var initFlat func(msg *byte) int32
		purego.RegisterFunc(&initFlat, sym)
		msg := make([]byte, 1024)
		if res := initFlat(&msg[0]); res != 0 {
			text := string(msg[:max(bytes.IndexByte(msg, 0), 0)])
			if text == "" {
				text = fmt.Sprintf("code %d", res)
			}
			return fmt.Errorf("SteamAPI init failed (is Steam running and do you own Garry's Mod?): %s", text)
		}
	} else {
		var initOld func() bool
		reg(&initOld, "SteamAPI_Init")
		if !initOld() {
			return errors.New("SteamAPI init failed: is Steam running and do you own Garry's Mod?")
		}
	}

	var accessor func() uintptr
	purego.RegisterFunc(&accessor, ugcFn)
	a.ugc = accessor()
	purego.RegisterFunc(&accessor, userFn)
	a.user = accessor()
	purego.RegisterFunc(&accessor, friendsFn)
	friends := accessor()

	if a.ugc == 0 || a.user == 0 {
		a.shutdown()
		return errors.New("SteamAPI: failed to get ISteamUGC/ISteamUser interfaces")
	}

	a.dispatchInit()
	a.pipe = a.getPipe()

	if friends != 0 {
		s.persona = a.getPersonaName(friends)
	}

	s.api = a
	return nil
}

// Close shuts the Steam API down.
func (s *Steamworks) Close() {
	if s.jobs == nil {
		return
	}
	done := make(chan struct{})
	s.jobs <- func() {
		if s.api != nil {
			s.api.shutdown()
			s.api = nil
		}
		close(done)
	}
	<-done
}

// pump drains pending callbacks and returns the result of call if it completed.
func (a *steamAPI) pump(call uint64, expected int32) (buf []byte, done bool, err error) {
	a.runFrame(a.pipe)

	var msg callbackMsg
	for a.nextCallback(a.pipe, &msg) {
		if call != 0 && msg.Callback == cbAPICallCompleted && msg.CubParam >= 16 && msg.Param != nil {
			p := unsafe.Slice(msg.Param, msg.CubParam)
			if binary.LittleEndian.Uint64(p[0:8]) == call {
				cub := binary.LittleEndian.Uint32(p[12:16])
				out := make([]byte, max(int(cub), 1))
				var failed bool
				ok := a.getCallResult(a.pipe, call, unsafe.Pointer(&out[0]), int32(cub), expected, &failed)
				a.freeLastCallback(a.pipe)
				if !ok || failed {
					return nil, true, errors.New("steam API call failed (I/O failure)")
				}
				return out, true, nil
			}
		}
		a.freeLastCallback(a.pipe)
	}
	return nil, false, nil
}

func (a *steamAPI) wait(ctx context.Context, call uint64, expected int32, tick func()) ([]byte, error) {
	if call == 0 || call == invalidHandle {
		return nil, errors.New("steam API refused the request")
	}
	for {
		buf, done, err := a.pump(call, expected)
		if done {
			return buf, err
		}
		if tick != nil {
			tick()
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

	err := s.do(func(a *steamAPI) error {
		log(fmt.Sprintf("Requesting download of %d through Steam...", id))
		if !a.downloadItem(a.ugc, id, true) {
			return errors.New("Steam refused the download: Garry's Mod must be installed in this Steam client (or use the steamcmd backend), and the item must exist")
		}

		started := time.Now()
		seen := false
		lastLog := time.Time{}

		for {
			if _, _, err := a.pump(0, 0); err != nil {
				return err
			}

			st := a.getItemState(a.ugc, id)
			busy := st&(itemStateDownloading|itemStatePending|itemStateNeedsUpdate) != 0
			if busy {
				seen = true
				var done, total uint64
				if a.getItemDownloadInfo(a.ugc, id, &done, &total) && total > 0 {
					progress(float64(done) / float64(total))
					if time.Since(lastLog) > 2*time.Second {
						log(fmt.Sprintf("Downloading... %d / %d bytes", done, total))
						lastLog = time.Now()
					}
				} else {
					progress(-1)
				}
			} else if st&itemStateInstalled != 0 {
				break
			} else if !seen && time.Since(started) > 60*time.Second {
				return errors.New("download did not start (item may be private, removed or not a Garry's Mod item)")
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
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

	tmp, cleanup, err := prepareContent("", req.GMAPath)
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
	err = s.do(func(a *steamAPI) error {
		progress(-1)

		if id == 0 {
			log("Creating a new Workshop item...")
			buf, err := a.wait(ctx, a.createItem(a.ugc, AppID, 0), cbCreateItemResult, nil)
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
		if !a.setItemVisibility(a.ugc, h, int32(req.Visibility)) {
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
		buf, err := a.wait(ctx, a.submitItemUpdate(a.ugc, h, req.ChangeNote), cbSubmitItemUpdate, func() {
			var done, total uint64
			st := a.getItemUpdateProgres(a.ugc, h, &done, &total)
			if st != lastStatus {
				if name, ok := updateStatusNames[st]; ok {
					log(name + "...")
				}
				lastStatus = st
			}
			if total > 0 {
				progress(float64(done) / float64(total))
			} else {
				progress(-1)
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
	return s.do(func(a *steamAPI) error {
		log(fmt.Sprintf("Deleting item %d...", id))
		buf, err := a.wait(ctx, a.deleteItem(a.ugc, id), cbDeleteItemResult, nil)
		if err != nil {
			return err
		}
		return eResultErr(int32(binary.LittleEndian.Uint32(buf[0:4])))
	})
}

func (s *Steamworks) ListPublished(ctx context.Context, log Logger) ([]Item, error) {
	var items []Item

	err := s.do(func(a *steamAPI) error {
		account := uint32(a.getSteamID(a.user) & 0xffffffff)
		details := make([]byte, 32*1024)

		for page := uint32(1); ; page++ {
			// list: Published (0), matching: Items (0), sort: CreationOrderDesc (0).
			// (matching "All" (~0) makes Steam answer with EResult 3.)
			q := a.createUserQuery(a.ugc, account, 0, 0, 0, AppID, AppID, page)
			if q == invalidHandle {
				return errors.New("CreateQueryUserUGCRequest failed")
			}

			buf, err := a.wait(ctx, a.sendQuery(a.ugc, q), cbUGCQueryCompleted, nil)
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
				// SteamUGCDetails_t: id @0, result @8, type @12, creator app @16,
				// consumer app @20, title[129] @24, description[8000] @153
				items = append(items, Item{
					ID:          binary.LittleEndian.Uint64(details[0:8]),
					Title:       cString(details[24 : 24+129]),
					Description: cString(details[153 : 153+8000]),
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

var libraryPathRe = regexp.MustCompile(`"path"\s+"([^"]+)"`)

// FindSteamAPI returns candidate steam_api libraries from Garry's Mod installs.
func FindSteamAPI() []string {
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

	libs := map[string]bool{}
	var order []string
	add := func(p string) {
		p = filepath.Clean(p)
		if !libs[p] {
			libs[p] = true
			order = append(order, p)
		}
	}

	for _, root := range steamRoots {
		add(root)
		raw, err := os.ReadFile(filepath.Join(root, "steamapps", "libraryfolders.vdf"))
		if err != nil {
			continue
		}
		for _, m := range libraryPathRe.FindAllStringSubmatch(string(raw), -1) {
			add(filepath.FromSlash(stringsReplaceVDF(m[1])))
		}
	}

	var rel []string
	switch runtime.GOOS {
	case "windows":
		rel = []string{`bin\win64\steam_api64.dll`, `steam_api64.dll`}
	case "darwin":
		rel = []string{"GarrysMod_Signed.app/Contents/MacOS/libsteam_api.dylib", "bin/osx64/libsteam_api.dylib"}
	default:
		rel = []string{"bin/linux64/libsteam_api.so"}
	}

	var found []string
	for _, lib := range order {
		for _, r := range rel {
			p := filepath.Join(lib, "steamapps", "common", "GarrysMod", r)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				found = append(found, p)
			}
		}
	}
	return found
}

func stringsReplaceVDF(s string) string {
	return string(bytes.ReplaceAll([]byte(s), []byte(`\\`), []byte(`\`)))
}

// Initialized reports whether the Steam API has been loaded (the library path
// can no longer change for this process once it has).
func (s *Steamworks) Initialized() bool {
	if s.jobs == nil {
		return false
	}
	res := make(chan bool, 1)
	s.jobs <- func() { res <- s.api != nil }
	return <-res
}
