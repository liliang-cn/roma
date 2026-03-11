package runtime

import (
	"io"
	"os/exec"
)

// PTYProcess is the active runtime terminal session.
type PTYProcess interface {
	io.ReadWriteCloser
	Wait() error
}

// PTYProvider starts commands under a pseudo-terminal.
type PTYProvider interface {
	Start(cmd *exec.Cmd) (PTYProcess, error)
}
