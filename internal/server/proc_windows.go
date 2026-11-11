//go:build windows

package server

import (
	"io"
	"os/exec"
)

type pipeRW struct {
	io.Reader
	io.WriteCloser
}

// startConsole starts cmd with piped stdin/stdout. Note that srcds.exe writes
// most of its console to its own window rather than stdout.
func startConsole(cmd *exec.Cmd) (io.ReadWriteCloser, func() error, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		pw.Close()
	}()
	return pipeRW{Reader: pr, WriteCloser: stdin}, func() error { return <-done }, nil
}

func terminate(cmd *exec.Cmd, force bool) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}
