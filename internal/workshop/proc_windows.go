//go:build windows

package workshop

import (
	"io"
	"os/exec"
)

type pipeRW struct {
	io.Reader
	io.WriteCloser
}

// startInteractive runs cmd with piped stdin/stdout. The returned wait
// function must be used instead of cmd.Wait (which is already called here to
// close the output pipe once the process exits).
func startInteractive(cmd *exec.Cmd) (io.ReadWriteCloser, func() error, error) {
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
