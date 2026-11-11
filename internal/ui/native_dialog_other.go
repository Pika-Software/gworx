//go:build !linux

package ui

// nativePick uses the system dialog (Win32 / macOS via the zenity package).
func nativePick(r fileRequest) (string, error) {
	p, err := zenityPick(r)
	if err != nil {
		return "", errNoNativeDialog
	}
	return p, nil
}
