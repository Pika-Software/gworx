//go:build !linux

package ui

import "net/url"

func (a *App) openExternal(u *url.URL) error {
	return a.fa.OpenURL(u)
}
