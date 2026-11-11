//go:build !windows

package workshop

import (
	"io"
	"os/exec"

	"github.com/creack/pty"
)

// startInteractive runs cmd attached to a pseudo terminal, so steamcmd
// flushes its prompts (password, Steam Guard) immediately.
func startInteractive(cmd *exec.Cmd) (io.ReadWriteCloser, error) {
	return pty.StartWithSize(cmd, &pty.Winsize{Rows: 60, Cols: 400})
}
