//go:build nogui

package main

// runGUI is nil in CLI-only builds (go build -tags nogui).
var runGUI func() error
