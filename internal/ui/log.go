package ui

import (
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const maxLogLines = 5000

// logView is a scrolling list of log lines, safe to append to from any goroutine.
type logView struct {
	mu      sync.Mutex
	lines   []string
	pending bool
	list    *widget.List
}

func newLogView() *logView {
	lv := &logView{}
	lv.list = widget.NewList(
		func() int {
			lv.mu.Lock()
			defer lv.mu.Unlock()
			return len(lv.lines)
		},
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.TextStyle = fyne.TextStyle{Monospace: true}
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			lv.mu.Lock()
			text := ""
			if id < len(lv.lines) {
				text = lv.lines[id]
			}
			lv.mu.Unlock()
			o.(*widget.Label).SetText(text)
		},
	)
	lv.list.HideSeparators = true
	return lv
}

func (lv *logView) add(line string) {
	lv.mu.Lock()
	lv.lines = append(lv.lines, time.Now().Format("15:04:05 ")+line)
	if len(lv.lines) > maxLogLines {
		lv.lines = lv.lines[len(lv.lines)-maxLogLines:]
	}
	schedule := !lv.pending
	lv.pending = true
	lv.mu.Unlock()

	if schedule {
		// coalesce refreshes when output is fast
		time.AfterFunc(80*time.Millisecond, func() {
			lv.mu.Lock()
			lv.pending = false
			lv.mu.Unlock()
			fyne.Do(func() {
				lv.list.Refresh()
				lv.list.ScrollToBottom()
			})
		})
	}
}

func (lv *logView) text() string {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	return strings.Join(lv.lines, "\n")
}

func (lv *logView) clear() {
	lv.mu.Lock()
	lv.lines = nil
	lv.mu.Unlock()
	lv.list.Refresh()
}

func (lv *logView) object(a *App) fyne.CanvasObject {
	copyBtn := widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
		a.fa.Clipboard().SetContent(lv.text())
	})
	clearBtn := widget.NewButtonWithIcon("Clear", theme.DeleteIcon(), lv.clear)
	return container.NewBorder(container.NewHBox(copyBtn, clearBtn), nil, nil, nil, lv.list)
}
