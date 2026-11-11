// Package fsutil holds small file helpers shared by several packages.
package fsutil

import (
	"io"
	"os"
	"path/filepath"
)

// CopyFile copies src to dst through a temporary "<dst>.part" file, so dst
// is either complete or untouched. Copying a file onto itself is a no-op.
func CopyFile(src, dst string) error {
	if filepath.Clean(src) == filepath.Clean(dst) {
		return nil
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}

	return os.Rename(tmp, dst)
}
