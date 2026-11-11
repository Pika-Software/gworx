package ui

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	portalBus      = "org.freedesktop.portal.Desktop"
	portalPath     = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	portalOpenFile = "org.freedesktop.portal.FileChooser.OpenFile"
	portalResponse = "org.freedesktop.portal.Request.Response"
)

var portalToken atomic.Uint64

// portalFilter is the D-Bus form of a file filter: (sa(us)).
type portalFilter struct {
	Name  string
	Rules []portalRule
}

type portalRule struct {
	Type    uint32 // 0 = glob pattern
	Pattern string
}

// nativePick uses the XDG desktop portal (the desktop's own file chooser on
// GNOME, KDE, ...), then zenity/kdialog, then reports errNoNativeDialog.
func nativePick(r fileRequest) (string, error) {
	p, err := portalPick(r)
	if !errors.Is(err, errNoNativeDialog) {
		return p, err
	}
	if p, zerr := zenityPick(r); zerr == nil {
		return p, nil
	}
	return "", errNoNativeDialog
}

// portalPick talks to org.freedesktop.portal.FileChooser directly. It uses a
// private bus connection and subscribes to the Response of its own request
// before opening the dialog, as the portal documentation requires, so other
// signals on the bus (Fyne listens to the settings portal) can't be mistaken
// for the answer.
func portalPick(r fileRequest) (string, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return "", errNoNativeDialog
	}
	defer conn.Close()

	token := "gworx" + strconv.FormatUint(portalToken.Add(1), 10) + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	sender := strings.ReplaceAll(strings.TrimPrefix(conn.Names()[0], ":"), ".", "_")
	expected := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token)

	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	match := []dbus.MatchOption{
		dbus.WithMatchInterface("org.freedesktop.portal.Request"),
		dbus.WithMatchMember("Response"),
		dbus.WithMatchObjectPath(expected),
	}
	if err := conn.AddMatchSignal(match...); err != nil {
		return "", errNoNativeDialog
	}

	opts := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(token),
		"modal":        dbus.MakeVariant(true),
		"directory":    dbus.MakeVariant(r.folder),
	}
	if dir := r.startFolder(); dir != "" {
		opts["current_folder"] = dbus.MakeVariant(append([]byte(dir), 0))
	}
	if !r.folder && len(r.exts) > 0 {
		f := portalFilter{}
		var names []string
		for _, e := range r.exts {
			names = append(names, "*"+e)
			f.Rules = append(f.Rules,
				portalRule{Pattern: "*" + strings.ToLower(e)},
				portalRule{Pattern: "*" + strings.ToUpper(e)})
		}
		f.Name = strings.Join(names, ", ")
		opts["filters"] = dbus.MakeVariant([]portalFilter{f})
		opts["current_filter"] = dbus.MakeVariant(f)
	}

	var handle dbus.ObjectPath
	call := conn.Object(portalBus, portalPath).Call(portalOpenFile, 0, r.parent, r.title, opts)
	if call.Err != nil {
		return "", errNoNativeDialog // no portal / no FileChooser implementation
	}
	if err := call.Store(&handle); err != nil {
		return "", errNoNativeDialog
	}
	if handle != expected {
		// very old portals ignore handle_token; follow the returned path
		conn.AddMatchSignal(
			dbus.WithMatchInterface("org.freedesktop.portal.Request"),
			dbus.WithMatchMember("Response"),
			dbus.WithMatchObjectPath(handle),
		)
	}

	// From here on the system dialog is open: never fall back to a second one.
	for sig := range signals {
		if sig.Path != handle || sig.Name != portalResponse || len(sig.Body) != 2 {
			continue
		}
		status, _ := sig.Body[0].(uint32)
		results, _ := sig.Body[1].(map[string]dbus.Variant)
		switch status {
		case 0: // success
		case 1: // cancelled
			return "", nil
		default:
			return "", errors.New("the file dialog was closed")
		}

		var uris []string
		if v, ok := results["uris"]; ok {
			uris, _ = v.Value().([]string)
		}
		if len(uris) == 0 {
			return "", nil
		}
		u, err := url.Parse(uris[0])
		if err != nil || u.Scheme != "file" {
			return "", fmt.Errorf("unsupported location %q", uris[0])
		}
		return u.Path, nil
	}
	return "", errors.New("lost the connection to the file dialog")
}
