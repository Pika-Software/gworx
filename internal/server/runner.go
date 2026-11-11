package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// State of a server instance.
type State int

const (
	Stopped State = iota
	Installing
	Starting
	Running
	Stopping
)

func (s State) String() string {
	return [...]string{"Stopped", "Installing", "Starting", "Running", "Stopping"}[s]
}

const (
	maxConsoleLines = 5000
	stopTimeout     = 20 * time.Second
)

// ansiDropRe strips cursor/erase/OSC sequences but keeps SGR color codes (\x1b[...m)
// so the UI can render coloured console lines.
var ansiDropRe = regexp.MustCompile(`\x1b\][^\x07]*\x07|\x1b[()][A-Z0-9]|\x1b\[[0-9;?]*[A-ln-z]`)

// Runner owns the process and console of one instance.
type Runner struct {
	mu      sync.Mutex
	state   State
	lines   []string
	partial string

	cmd    *exec.Cmd
	term   io.ReadWriteCloser
	exited chan struct{}

	installCancel context.CancelFunc

	// OnLine and OnState are called from background goroutines.
	onLine  func(string)
	onState func(State)
}

// SetHandlers registers callbacks for console output and state changes.
func (r *Runner) SetHandlers(onLine func(string), onState func(State)) {
	r.mu.Lock()
	r.onLine, r.onState = onLine, onState
	r.mu.Unlock()
}

func (r *Runner) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// Lines returns a copy of the console buffer.
func (r *Runner) Lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

func (r *Runner) ClearConsole() {
	r.mu.Lock()
	r.lines = nil
	r.mu.Unlock()
}

func (r *Runner) setState(s State) {
	r.mu.Lock()
	r.state = s
	cb := r.onState
	r.mu.Unlock()
	if cb != nil {
		cb(s)
	}
}

// Print adds a line to the console.
func (r *Runner) Print(line string) {
	r.mu.Lock()
	r.lines = append(r.lines, line)
	if len(r.lines) > maxConsoleLines {
		r.lines = r.lines[len(r.lines)-maxConsoleLines:]
	}
	cb := r.onLine
	r.mu.Unlock()
	if cb != nil {
		cb(line)
	}
}

// feed splits raw terminal output into console lines.
func (r *Runner) feed(chunk string) {
	r.partial += ansiDropRe.ReplaceAllString(chunk, "")
	for {
		i := strings.IndexAny(r.partial, "\r\n")
		if i < 0 {
			break
		}
		line := strings.TrimRight(r.partial[:i], " \t")
		r.partial = r.partial[i+1:]
		if line != "" {
			r.Print(line)
		}
	}
}

// start launches the server.
func (r *Runner) start(inst Instance, token, wrapper string) error {
	r.mu.Lock()
	if r.state != Stopped {
		st := r.state
		r.mu.Unlock()
		return fmt.Errorf("server is %s", strings.ToLower(st.String()))
	}
	r.state = Starting
	r.mu.Unlock()

	fail := func(err error) error {
		r.setState(Stopped)
		return err
	}

	if err := inst.Validate(); err != nil {
		return fail(err)
	}
	prog, args, err := inst.Command(token, wrapper)
	if err != nil {
		return fail(err)
	}

	cmd := exec.Command(prog, args...)
	cmd.Dir = inst.Dir
	cmd.Env = append(os.Environ(), "TERM=dumb")

	r.setState(Starting)
	r.Print("> " + inst.CommandLine(token, wrapper))

	term, wait, err := startConsole(cmd)
	if err != nil {
		return fail(fmt.Errorf("starting server: %w", err))
	}

	exited := make(chan struct{})
	r.mu.Lock()
	r.cmd, r.term, r.exited = cmd, term, exited
	r.partial = ""
	r.mu.Unlock()
	r.setState(Running)

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 4096)
		for {
			n, err := term.Read(buf)
			if n > 0 {
				r.feed(string(buf[:n]))
			}
			if err != nil {
				return // EOF, or EIO once the pty child exits
			}
		}
	}()

	go func() {
		err := wait()
		// let the reader drain the remaining output before closing the terminal
		select {
		case <-readerDone:
		case <-time.After(2 * time.Second):
		}
		term.Close()
		<-readerDone
		if r.partial != "" {
			r.feed("\n")
		}
		if err != nil {
			r.Print("* Server exited: " + err.Error())
		} else {
			r.Print("* Server stopped")
		}
		r.mu.Lock()
		r.cmd, r.term = nil, nil
		r.mu.Unlock()
		close(exited)
		r.setState(Stopped)
	}()

	return nil
}

// Send writes a console command to the running server.
func (r *Runner) Send(command string) error {
	r.mu.Lock()
	term, state := r.term, r.state
	r.mu.Unlock()
	if term == nil || state != Running {
		return errors.New("the server is not running")
	}
	if runtime.GOOS == "windows" {
		r.Print("] " + command) // the pty echoes input on other platforms
	}
	_, err := io.WriteString(term, command+"\n")
	return err
}

// Stop asks the server to quit and kills it if it doesn't within stopTimeout.
func (r *Runner) Stop() error {
	r.mu.Lock()
	switch r.state {
	case Installing:
		cancel := r.installCancel
		r.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return nil
	case Running:
	default:
		r.mu.Unlock()
		return nil
	}
	cmd, term, exited := r.cmd, r.term, r.exited
	r.state = Stopping
	cb := r.onState
	r.mu.Unlock()
	if cb != nil {
		cb(Stopping)
	}
	if cmd == nil {
		return nil
	}

	r.Print("* Stopping server...")
	io.WriteString(term, "quit\n")

	select {
	case <-exited:
		return nil
	case <-time.After(stopTimeout):
	}
	r.Print("* Server did not quit, terminating")
	terminate(cmd, false)
	select {
	case <-exited:
		return nil
	case <-time.After(5 * time.Second):
	}
	terminate(cmd, true)
	<-exited
	return nil
}

// Wait blocks until the server process has exited.
func (r *Runner) Wait() {
	r.mu.Lock()
	exited := r.exited
	r.mu.Unlock()
	if exited != nil {
		<-exited
	}
}
