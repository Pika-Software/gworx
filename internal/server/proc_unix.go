//go:build !windows

package server

import (
	"io"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// startConsole runs cmd in a pseudo terminal (its own session and process
// group), so the console behaves like a real terminal and stdin works.
func startConsole(cmd *exec.Cmd) (io.ReadWriteCloser, func() error, error) {
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 200})
	if err != nil {
		return nil, nil, err
	}
	return f, cmd.Wait, nil
}

// terminate signals the whole process group (srcds_run and srcds_linux).
func terminate(cmd *exec.Cmd, force bool) {
	if cmd.Process == nil {
		return
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil {
		cmd.Process.Signal(sig)
	}
}
