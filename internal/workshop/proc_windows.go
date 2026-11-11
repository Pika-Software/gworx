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

func startInteractive(cmd *exec.Cmd) (io.ReadWriteCloser, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	go func() {
		cmd.Wait()
		pw.Close()
	}()

	return pipeRW{Reader: pr, WriteCloser: stdin}, nil
}
