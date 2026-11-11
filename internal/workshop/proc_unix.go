//go:build !windows

package workshop

import (
	"io"
	"os/exec"

	"github.com/creack/pty"
)

// startInteractive runs cmd attached to a pseudo terminal, so steamcmd
// flushes its prompts (password, Steam Guard) immediately. The returned
// wait function must be used instead of cmd.Wait.
func startInteractive(cmd *exec.Cmd) (io.ReadWriteCloser, func() error, error) {
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 60, Cols: 400})
	if err != nil {
		return nil, nil, err
	}
	return f, cmd.Wait, nil
}
